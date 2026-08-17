package orchestrator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// expandFanouts instantiates every fan-out whose source has finished.
//
// # Why the graph grows instead of the scheduler learning a new concept
//
// A fan-out expands into the *static* DAG: waves between items are ordinary
// depends_on edges, and one item's pipeline is ordinary nodes. So the wave loop,
// the parallelism bound and the SKIPPED cascade all keep working unchanged —
// the only new machinery is that the node set grows once, here.
//
// # Why here
//
// This runs on the scheduler goroutine between waves, which is the one place in
// walkDAG where no worker goroutine is alive (runWaveParallel's barrier). The
// invariant it is protecting is quiet but real: schedule.tasks and
// schedule.run.DAG are read by every step, and swapping them while a step is
// running would be a data race on the graph itself.
func (o *Orchestrator) expandFanouts(s *schedule) error {
	changed := false
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.Fanout == nil || t.Expanded || s.terminal[t.ID] {
			continue
		}
		if !dependenciesMet(t, s.run.Completed) {
			continue
		}
		if err := o.expandOne(s, t); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}

	// Rebuild the graph the scheduler walks, then persist it. tasks.md is the
	// state of record, so a run that dies after expanding must come back to the
	// same node set rather than rediscovering a possibly different one.
	d := dag.NewDAG(s.tasks)
	if err := d.Validate(); err != nil {
		return fmt.Errorf("validating DAG after fan-out expansion: %w", err)
	}
	s.run.DAG = d
	if err := persistExpansion(s); err != nil {
		return fmt.Errorf("persisting fan-out expansion: %w", err)
	}
	return nil
}

// persistExpansion writes the grown graph without clobbering task status.
//
// The scheduler's in-memory task list is structure, not state: status writes go
// straight to tasks.md through the Bookkeeper and are never mirrored back onto
// these structs. So a full rewrite from memory would hand every already-finished
// task back its PENDING status from load time — which is exactly what the first
// version of this function did, and what the resume test caught.
//
// Disk is the authority for status; memory is the authority for shape.
func persistExpansion(s *schedule) error {
	onDisk, _, err := task.ParseTasksFile(s.run.TasksPath)
	if err != nil {
		return err
	}
	status := make(map[string]types.TaskStatus, len(onDisk))
	items := make(map[string][]string, len(onDisk))
	for _, t := range onDisk {
		status[t.ID] = t.Status
		if len(t.Items) > 0 {
			items[t.ID] = t.Items
		}
	}

	merged := make([]types.Task, len(s.tasks))
	deps := make(map[string][]string, len(s.tasks))
	for i, t := range s.tasks {
		if st, ok := status[t.ID]; ok {
			t.Status = st
		}
		if it, ok := items[t.ID]; ok && len(t.Items) == 0 {
			t.Items = it
		}
		if t.DependsOn == nil {
			t.DependsOn = []string{}
		}
		merged[i] = t
		deps[t.ID] = t.DependsOn
	}
	return task.WriteTasksFile(s.run.TasksPath, merged, types.DAGSpec{
		GeneratedBy:  s.generatedBy,
		Dependencies: deps,
	})
}

// generatedBy reads back the tasks.md frontmatter marker so a fan-out rewrite
// preserves it. Losing it would make a recipe run look like a planner run — and
// that marker is what run identity reads to know which recipe it is executing.
func generatedBy(tasksPath string) string {
	_, spec, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return ""
	}
	return spec.GeneratedBy
}

func dependenciesMet(t *types.Task, completed map[string]bool) bool {
	for _, dep := range t.DependsOn {
		if !completed[dep] {
			return false
		}
	}
	return true
}

// expandOne turns one fan-out node into N instances of its template.
//
// The fan-out node itself stays in the graph and becomes the join: it now
// depends on the last step of every item, so anything that declared
// `depends_on: [S06]` keeps meaning "after all the items". Rewriting the
// dependents instead would work too and would be worse — the id the recipe
// author wrote would stop existing.
func (o *Orchestrator) expandOne(s *schedule, fan *types.Task) error {
	f := fan.Fanout
	src := findTask(s.tasks, f.Over)
	if src == nil {
		return fmt.Errorf("fan-out %s: source stage %s is not in the task list", fan.ID, f.Over)
	}
	items := src.Items

	if max := f.EffectiveMaxItems(); len(items) > max {
		// Hard failure naming the knob, not a silent truncation. Each item can
		// be a code step, so a discovery command that returns four thousand
		// paths is four thousand LLM calls; the global cost ceiling is a net
		// that catches you after the money is gone, which is not the same thing
		// as a door that does not open.
		return fmt.Errorf("fan-out %s discovered %d items but max_items is %d — raise `max_items` on the stage if that is really intended",
			fan.ID, len(items), max)
	}

	waves, err := groupIntoWaves(items, f.WaveBy)
	if err != nil {
		return fmt.Errorf("fan-out %s: %w", fan.ID, err)
	}

	fan.Expanded = true
	fan.Kind = string(types.KindTool)
	fan.Command = ":" // the join is a no-op; its dependencies are the work
	outerDeps := append([]string(nil), fan.DependsOn...)

	if len(items) == 0 {
		// Zero items is a legitimate normal case ("no pending migrations"), so
		// it passes — but silence would read as success, so it is said out loud
		// on the ledger.
		fan.DependsOn = outerDeps
		o.emit(Event{Type: EventTaskWarn, TaskID: fan.ID, Message: "fan-out discovered 0 items; nothing to expand"})
		charmbraceletlog.Warn("fan-out discovered no items", "stage", fan.ID, "over", f.Over)
		return nil
	}

	var expanded []types.Task
	var joinDeps []string
	var prevWaveLeaves []string
	index := 0

	for _, wave := range waves {
		var waveLeaves []string
		// Computed once per wave rather than inline in the item loop: appending
		// to outerDeps in the loop would hand successive items slices that
		// share a backing array, which is a latent aliasing bug even where the
		// values happen to match today.
		waveRoots := dedupeStrings(append(append([]string(nil), outerDeps...), prevWaveLeaves...))
		for _, item := range wave.items {
			nodes, leaves := instantiate(fan, item, index, waveRoots)
			expanded = append(expanded, nodes...)
			waveLeaves = append(waveLeaves, leaves...)
			index++
		}
		joinDeps = append(joinDeps, waveLeaves...)
		prevWaveLeaves = waveLeaves
	}

	// The join waits only on the final wave's leaves — everything earlier is
	// already transitively required — but listing every leaf is cheaper to read
	// in tasks.md than making the reader compute the closure.
	fan.DependsOn = dedupeStrings(joinDeps)
	s.tasks = append(s.tasks, expanded...)
	o.emit(Event{Type: EventDAGResolved, TaskID: fan.ID, Total: len(items),
		Message: fmt.Sprintf("fan-out %s expanded %d item(s) into %d node(s)", fan.ID, len(items), len(expanded))})
	return nil
}

// instantiate builds one item's nodes and returns them plus its leaf ids.
func instantiate(fan *types.Task, item string, index int, roots []string) ([]types.Task, []string) {
	prefix := fmt.Sprintf("%s/%03d", fan.ID, index)
	nodes := make([]types.Task, 0, len(fan.Fanout.Template))
	hasDependent := make(map[string]bool, len(fan.Fanout.Template))
	for _, ts := range fan.Fanout.Template {
		for _, dep := range ts.DependsOn {
			hasDependent[dep] = true
		}
	}

	var leaves []string
	for _, ts := range fan.Fanout.Template {
		id := prefix + "/" + ts.ID
		deps := make([]string, 0, len(ts.DependsOn))
		for _, dep := range ts.DependsOn {
			deps = append(deps, prefix+"/"+dep)
		}
		if len(deps) == 0 {
			// A template step with no sibling dependency starts this item, so
			// it inherits the fan-out's own edges (its source, and the previous
			// wave when there is one).
			deps = append(deps, dedupeStrings(roots)...)
		}
		typ := types.TaskType(ts.Type)
		if typ == "" {
			typ = types.TypeGeneral
		}
		nodes = append(nodes, types.Task{
			ID:          id,
			Title:       substituteItem(ts.Title, item),
			Status:      types.StatusPending,
			Type:        typ,
			DependsOn:   deps,
			Description: substituteItem(ts.Description, item),
			Criteria:    ts.Criteria,
			Kind:        ts.Kind,
			Command:     substituteItem(ts.Command, item),
			Gates:       ts.Gates,
			Evidence:    ts.Evidence,
			Item:        item,
			FanoutOf:    fan.ID,
		})
		if !hasDependent[ts.ID] {
			leaves = append(leaves, id)
		}
	}
	return nodes, leaves
}

// substituteItem replaces the `{{ item }}` placeholder.
//
// Deliberately not a template engine: one placeholder, textual, no expressions.
// A recipe is configuration a person reads at a gate, and a language inside it
// is a language somebody has to debug at three in the morning.
func substituteItem(s, item string) string {
	if s == "" {
		return s
	}
	for _, form := range []string{"{{ item }}", "{{item}}", "{{ .item }}", "{{.item}}"} {
		s = strings.ReplaceAll(s, form, item)
	}
	return s
}

// itemWave is one dependency wave of a fan-out.
type itemWave struct {
	key   string
	items []string
}

// groupIntoWaves splits the work list into ordered waves.
//
// Without wave_by there is one wave and every item runs concurrently, which is
// the common case. With it, items are grouped by a field of the structured item
// and the waves run in sorted key order — that is what "dependency between
// items" means: wave 2 does not start until wave 1 is done.
func groupIntoWaves(items []string, waveBy string) ([]itemWave, error) {
	field := strings.TrimSpace(waveBy)
	field = strings.TrimPrefix(field, "item.")
	if field == "" {
		if len(items) == 0 {
			return nil, nil
		}
		return []itemWave{{items: items}}, nil
	}

	grouped := map[string][]string{}
	var keys []string
	for _, item := range items {
		var obj map[string]any
		if err := json.Unmarshal([]byte(item), &obj); err != nil {
			return nil, fmt.Errorf("wave_by %q needs structured items, but %q is not a JSON object "+
				"(have the discovery step emit a JSON array of objects)", waveBy, truncateForError(item))
		}
		raw, ok := obj[field]
		if !ok {
			return nil, fmt.Errorf("wave_by %q: item %q has no field %q", waveBy, truncateForError(item), field)
		}
		key := fmt.Sprint(raw)
		if _, seen := grouped[key]; !seen {
			keys = append(keys, key)
		}
		grouped[key] = append(grouped[key], item)
	}
	sort.Strings(keys)
	waves := make([]itemWave, 0, len(keys))
	for _, k := range keys {
		waves = append(waves, itemWave{key: k, items: grouped[k]})
	}
	return waves, nil
}

func truncateForError(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[:57] + "..."
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// fanoutParallelism is the tightest max_parallel any task in this wave belongs
// to, or 0 when none of them declares one.
//
// A fan-out that says max_parallel: 3 is usually protecting something outside
// corvex — a database that will not take ten concurrent migrations. Honouring it
// by tightening the wave's own bound is enough: a wave never contains items from
// two different fan-outs that both need loosening.
func fanoutParallelism(s *schedule, ready []string) int {
	best := 0
	for _, id := range ready {
		t := findTask(s.tasks, id)
		if t == nil || t.FanoutOf == "" {
			continue
		}
		owner := findTask(s.tasks, t.FanoutOf)
		if owner == nil || owner.Fanout == nil || owner.Fanout.MaxParallel <= 0 {
			continue
		}
		if best == 0 || owner.Fanout.MaxParallel < best {
			best = owner.Fanout.MaxParallel
		}
	}
	return best
}

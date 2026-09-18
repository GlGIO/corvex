package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/sandbox"
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
			if f.IsolatesItems() {
				var isoErr error
				nodes, leaves, isoErr = o.isolateItem(fan, nodes, leaves, index)
				if isoErr != nil {
					return fmt.Errorf("fan-out %s, item %d: %w", fan.ID, index, isoErr)
				}
			}
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

// isolateItem gives one item its own checkout and its own way back.
//
// Two things happen here, and they are one decision: every node of the item is
// pinned to a fresh git worktree, and a `merge` node is appended that depends on
// the item's leaves, runs in the RUN's checkout, and brings the branch home.
//
// The merge is a node rather than something the runner does behind the graph,
// and that is the whole design:
//
//   - it only runs if the item's own steps passed, because a failed dependency
//     blocks it — so a story that broke never lands on the feature branch, with
//     no bookkeeping needed to arrange that;
//   - a conflict is a step that FAILED, with git's conflict output in the step's
//     own output, on the screen where every other failure already appears;
//   - the operator can read, in tasks.md, exactly which branches will be merged
//     and in which order, before any of it happens.
//
// What it deliberately does NOT do is resolve a conflict. The reference flow
// hands that to an agent ("Sincronizar"); here it stops and says so. Two stories
// that edited the same lines is a fact about the decomposition, and the person
// who owns the decomposition is not this process.
func (o *Orchestrator) isolateItem(fan *types.Task, nodes []types.Task, leaves []string, index int) ([]types.Task, []string, error) {
	suffix := fmt.Sprintf("%s-%03d", sanitiseWorktreeName(fan.ID), index)
	wt, err := sandbox.CreateWorktree(context.Background(), o.workDir, suffix)
	if err != nil {
		return nil, nil, fmt.Errorf("creating the worktree for this item: %w", err)
	}
	for i := range nodes {
		nodes[i].WorkDir = wt.Path
	}

	mergeID := fmt.Sprintf("%s/%03d/merge", fan.ID, index)
	// `--no-ff` so the item keeps a shape in history: one merge commit per
	// story, which is what makes "which story broke this" answerable later by
	// reading the log instead of by guessing from a flat sequence of commits.
	// `git worktree remove` only after the merge succeeded — a failed merge
	// leaves the tree on disk ON PURPOSE, because the work in it is the only
	// copy and a person is about to need it.
	command := fmt.Sprintf("set -e\ngit merge --no-ff -m %q %s\ngit worktree remove --force %q",
		"corvex: merge "+mergeID, wt.Branch, wt.Path)
	nodes = append(nodes, types.Task{
		ID:        mergeID,
		Title:     "Trazer o trabalho de volta (" + wt.Branch + ")",
		Status:    types.StatusPending,
		Type:      types.TypeGeneral,
		Kind:      string(types.KindTool),
		DependsOn: append([]string(nil), leaves...),
		Command:   command,
		Item:      nodes[0].Item,
		FanoutOf:  fan.ID,
		// It is a command, and it writes the tree every other merge writes. The
		// runner cannot see that from the string, so the node says it: without
		// this, two merges in one wave race on `.git/index.lock` and the loser
		// reports exit 128 as a failed step.
		WritesRunTree: true,
		// No WorkDir: the merge belongs to the run's checkout, which is the
		// tree it merges INTO. Pinning it to the item's worktree would merge a
		// branch into itself and leave the feature branch untouched.
	})
	return nodes, []string{mergeID}, nil
}

// sanitiseWorktreeName keeps a stage id usable as a directory and a branch
// segment. Stage ids are `S03`-shaped today; a slash in one would otherwise
// create a directory nobody asked for.
func sanitiseWorktreeName(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, id)
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

// substituteItem replaces the `{{ item }}` placeholder, and `{{ item.field }}`
// for one field of a structured item.
//
// Deliberately not a template engine: two forms, textual, no expressions, no
// nesting. A recipe is configuration a person reads at a gate, and a language
// inside it is a language somebody has to debug at three in the morning.
//
// The field form was not in the first version and the first REAL fan-out is what
// asked for it. Items from a board are objects, so every title written with
// `{{ item }}` came out as
//
//	Investigar {"id":"59821","type":"User Story","state":"New","title":…}
//
// in the run screen, in the gate inbox and in the ledger — the three places a
// person reads to decide something. `{{ item.id }}` is the difference between a
// queue somebody can scan and a wall of JSON.
//
// An unresolved placeholder stays LITERAL rather than becoming empty: a title
// that reads `Investigar {{ item.titel }}` is a typo somebody fixes in seconds,
// and `Investigar ` is a typo nobody ever sees.
func substituteItem(s, item string) string {
	if s == "" {
		return s
	}
	if strings.Contains(s, "item.") {
		s = substituteItemFields(s, item)
	}
	for _, form := range []string{"{{ item }}", "{{item}}", "{{ .item }}", "{{.item}}"} {
		s = strings.ReplaceAll(s, form, item)
	}
	return s
}

// itemFieldRef matches `{{ item.field }}` and `{{.item.field}}`, with or without
// the spaces and the leading dot — the same four spellings the whole-item form
// already accepted, because a recipe author who learned one should not discover
// that the other silently does nothing.
var itemFieldRef = regexp.MustCompile(`\{\{ *\.?item\.([A-Za-z0-9_]+) *\}\}`)

// substituteItemFields resolves the field form against a structured item. A
// non-object item (a path, a migration name — the common shape before boards
// entered the picture) resolves nothing and keeps its text.
func substituteItemFields(s, item string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(item), &obj); err != nil {
		return s
	}
	return itemFieldRef.ReplaceAllStringFunc(s, func(match string) string {
		key := itemFieldRef.FindStringSubmatch(match)[1]
		v, ok := obj[key]
		if !ok || v == nil {
			return match
		}
		switch typed := v.(type) {
		case string:
			return typed
		case float64:
			// A wave or an id that arrived as a number must not come back as
			// 5.9337e+04: %v on a float64 is exactly how that happens.
			return strconv.FormatFloat(typed, 'f', -1, 64)
		default:
			// A nested object or list has no one-line spelling that is not a
			// decision about formatting, so it keeps the JSON it arrived as.
			raw, merr := json.Marshal(typed)
			if merr != nil {
				return match
			}
			return string(raw)
		}
	})
}

// sortWaveKeys puts the waves in the order they run.
//
// It was `sort.Strings`, and that is right for a key like `backend`/`frontend`
// and WRONG for the key every real fan-out uses: a wave NUMBER. Lexicographic
// order puts 10 before 2, so a feature with ten or more waves would run its
// eleventh wave third — dependencies inverted, silently, and only on the graphs
// big enough that nobody checks them by hand.
//
// Found from the other side: the tool that reads an Azure Feature and emits
// `wave` per story (scripts/flow/az-feature-dag.sh in the SmartCare repo) is
// this function's first real client, and writing it is what exposed the
// ordering. Numeric when every key is a number, lexicographic otherwise —
// mixed sets stay on the old rule rather than inventing a third order.
func sortWaveKeys(keys []string) {
	// The key and its number travel TOGETHER. The first version of this sorted
	// `keys` with a comparator that indexed a parallel `nums` slice — which the
	// sort never permutes, so every comparison after the first swap read the
	// number of a different key. It passed the two-wave case and would have
	// scrambled anything larger.
	type waveKey struct {
		key string
		num float64
	}
	parsed := make([]waveKey, len(keys))
	for i, k := range keys {
		n, err := strconv.ParseFloat(k, 64)
		if err != nil {
			sort.Strings(keys)
			return
		}
		parsed[i] = waveKey{key: k, num: n}
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].num < parsed[j].num })
	for i, p := range parsed {
		keys[i] = p.key
	}
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
	sortWaveKeys(keys)
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

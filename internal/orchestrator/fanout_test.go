package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// fanoutTasksMD is a discovery step feeding a two-step template, plus a
// consolidation step that depends on the fan-out by the id the author wrote.
func fanoutTasksMD(discover string, extra string) string {
	return fmt.Sprintf(`---
generated_by: corvex-recipe:fanout-demo
dag:
    S01: []
    S02:
        - S01
    S03:
        - S02
---

## S01 — Discover ⬜ PENDING

`+"```"+`yaml
kind: tool
command: %q
produces: items
`+"```"+`

---

## S02 — Per item ⬜ PENDING

`+"```"+`yaml
depends_on: [S01]
fanout:
    over: S01
%s    template:
        - id: apply
          kind: tool
          command: "true"
        - id: verify
          kind: test
          command: "true"
          depends_on:
            - apply
`+"```"+`

---

## S03 — Consolidate ⬜ PENDING

`+"```"+`yaml
kind: tool
command: "true"
depends_on: [S02]
`+"```"+`
`, discover, extra)
}

func runFanout(t *testing.T, project, discover, extra string) (string, []types.Task, error) {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(t, dir)
	setupProject(t, dir, project, fanoutTasksMD(discover, extra))
	gitCommitAll(t, dir, "add fanout tasks")

	events := make(chan Event, 500)
	go func() {
		for range events {
		}
	}()
	cfg := config.Default()
	cfg.Project.Name = project

	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events})
	err := orch.Run(context.Background(), project)
	close(events)

	tasks, _, parseErr := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	if parseErr != nil {
		t.Fatalf("tasks.md is unreadable after expansion: %v", parseErr)
	}
	return dir, tasks, err
}

func byID(tasks []types.Task) map[string]types.Task {
	m := make(map[string]types.Task, len(tasks))
	for _, t := range tasks {
		m[t.ID] = t
	}
	return m
}

// TestFanout_ExpandsIntoTheStaticDAG is the primitive the phase exists to add:
// N items discovered at run time, each through the same pipeline, joined back
// so that whatever depended on the fan-out still means "after all the items".
func TestFanout_ExpandsIntoTheStaticDAG(t *testing.T) {
	_, tasks, err := runFanout(t, "fan-basic", "printf 'a\\nb\\nc\\n'", "")
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	m := byID(tasks)

	// Three items, two template steps each, plus the three declared nodes.
	if len(tasks) != 9 {
		t.Fatalf("expanded to %d nodes, want 9:\n%v", len(tasks), taskIDs(tasks))
	}
	for i, item := range []string{"a", "b", "c"} {
		apply := fmt.Sprintf("S02/%03d/apply", i)
		verify := fmt.Sprintf("S02/%03d/verify", i)
		if _, ok := m[apply]; !ok {
			t.Fatalf("missing %s in %v", apply, taskIDs(tasks))
		}
		if m[apply].Item != item {
			t.Errorf("%s carries item %q, want %q", apply, m[apply].Item, item)
		}
		if m[apply].FanoutOf != "S02" {
			t.Errorf("%s is not attributed to its fan-out: %q", apply, m[apply].FanoutOf)
		}
		if got := m[verify].DependsOn; len(got) != 1 || got[0] != apply {
			t.Errorf("%s depends on %v, want [%s]", verify, got, apply)
		}
		// An item's first step inherits the fan-out's own edges.
		if got := m[apply].DependsOn; len(got) != 1 || got[0] != "S01" {
			t.Errorf("%s depends on %v, want [S01]", apply, got)
		}
	}

	// The join kept the author's id and now waits on every item's leaf.
	join := m["S02"]
	if !join.Expanded {
		t.Error("the fan-out node is not marked expanded; a resume would expand it again")
	}
	if len(join.DependsOn) != 3 {
		t.Errorf("join depends on %v, want the three verify leaves", join.DependsOn)
	}
	for _, id := range join.DependsOn {
		if !strings.HasSuffix(id, "/verify") {
			t.Errorf("join depends on %q, which is not an item leaf", id)
		}
	}
	if got := m["S03"].DependsOn; len(got) != 1 || got[0] != "S02" {
		t.Errorf("the consolidation step's dependency was rewritten to %v; the id the author wrote must keep working", got)
	}

	for _, tk := range tasks {
		if tk.Status != types.StatusPassed {
			t.Errorf("%s = %s, want PASSED", tk.ID, tk.Status)
		}
	}
}

// TestFanout_ItemSubstitution: the template's `{{ item }}` is what makes one
// template serve N different pieces of work.
func TestFanout_ItemSubstitution(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "fan-subst"
	md := strings.ReplaceAll(fanoutTasksMD("printf 'alpha\\n'", ""), `command: "true"`, `command: "echo {{ item }}"`)
	setupProject(t, dir, project, md)
	gitCommitAll(t, dir, "add fanout tasks")

	events := make(chan Event, 500)
	go func() {
		for range events {
		}
	}()
	cfg := config.Default()
	cfg.Project.Name = project
	if err := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events}).Run(context.Background(), project); err != nil {
		t.Fatalf("Run = %v", err)
	}
	close(events)

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	m := byID(tasks)
	if got := m["S02/000/apply"].Command; got != "echo alpha" {
		t.Errorf("command = %q, want the item substituted in", got)
	}
}

// TestFanout_ZeroItemsPassesButSaysSo. "No pending migrations" is the normal
// case, so it passes — but silence would read as success, so the ledger says it.
func TestFanout_ZeroItemsPassesButSaysSo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "fan-zero"
	setupProject(t, dir, project, fanoutTasksMD("true", ""))
	gitCommitAll(t, dir, "add fanout tasks")

	events := make(chan Event, 500)
	var warned bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			if ev.Type == EventTaskWarn && strings.Contains(ev.Message, "0 items") {
				warned = true
			}
		}
	}()
	cfg := config.Default()
	cfg.Project.Name = project
	err := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events}).Run(context.Background(), project)
	close(events)
	<-done

	if err != nil {
		t.Fatalf("zero items must not fail the run, got %v", err)
	}
	if !warned {
		t.Error("zero items passed without saying so — that is a silent success")
	}
	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	if byID(tasks)["S03"].Status != types.StatusPassed {
		t.Error("the consolidation step did not run after an empty fan-out")
	}
}

// TestFanout_MaxItemsIsADoorNotANet: exceeding the ceiling fails loudly and
// names the knob, because each item can be a code step and the cost ceiling only
// catches you after the money is spent.
func TestFanout_MaxItemsIsADoorNotANet(t *testing.T) {
	_, tasks, err := runFanout(t, "fan-max", "printf 'a\\nb\\nc\\nd\\n'", "    max_items: 2\n")
	if err == nil || !strings.Contains(err.Error(), "max_items") {
		t.Fatalf("Run = %v, want a failure naming max_items", err)
	}
	for _, tk := range tasks {
		if strings.Contains(tk.ID, "/") {
			t.Errorf("nothing should have been expanded, found %s", tk.ID)
		}
	}
}

// TestFanout_WavesOrderItemsAgainstEachOther is the part that separates a
// fan-out from a parallel map: wave 2 does not start until wave 1 is done.
func TestFanout_WavesOrderItemsAgainstEachOther(t *testing.T) {
	discover := `printf '[{"name":"a","wave":"1"},{"name":"b","wave":"2"},{"name":"c","wave":"1"}]'`
	_, tasks, err := runFanout(t, "fan-waves", discover, "    wave_by: item.wave\n")
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	m := byID(tasks)
	// Sorted key order: wave "1" holds a and c (indices 0,1), wave "2" holds b.
	second := m["S02/002/apply"]
	if second.Item == "" || !strings.Contains(second.Item, `"b"`) {
		t.Fatalf("wave ordering put %q third, want the wave-2 item", second.Item)
	}
	if len(second.DependsOn) < 2 {
		t.Fatalf("a wave-2 item depends on %v; it must wait for wave 1's leaves", second.DependsOn)
	}
	var waitsOnWaveOne bool
	for _, dep := range second.DependsOn {
		if strings.HasPrefix(dep, "S02/000/") || strings.HasPrefix(dep, "S02/001/") {
			waitsOnWaveOne = true
		}
	}
	if !waitsOnWaveOne {
		t.Errorf("wave 2 does not wait on wave 1: %v", second.DependsOn)
	}
}

// TestFanout_WaveByOnUnstructuredItemsFailsClearly: a plain line has no field to
// group on, and guessing would silently collapse every item into one wave.
func TestFanout_WaveByOnUnstructuredItemsFailsClearly(t *testing.T) {
	_, _, err := runFanout(t, "fan-badwave", "printf 'a\\nb\\n'", "    wave_by: item.wave\n")
	if err == nil || !strings.Contains(err.Error(), "structured items") {
		t.Fatalf("Run = %v, want a failure explaining the item shape", err)
	}
}

// TestFanout_ExpansionSurvivesResume: the expanded set is written to tasks.md,
// so a second run continues the same nodes rather than rediscovering — which
// would pick up whatever changed on disk in between.
func TestFanout_ExpansionSurvivesResume(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "fan-resume"
	// The discovery command returns a different list the second time it runs.
	marker := filepath.Join(dir, "seen")
	discover := fmt.Sprintf(`sh -c 'if [ -f %q ]; then printf "x\ny\nz\n"; else touch %q; printf "a\nb\n"; fi'`, marker, marker)
	setupProject(t, dir, project, fanoutTasksMD(discover, ""))
	gitCommitAll(t, dir, "add fanout tasks")

	events := make(chan Event, 500)
	go func() {
		for range events {
		}
	}()
	cfg := config.Default()
	cfg.Project.Name = project
	if err := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events}).Run(context.Background(), project); err != nil {
		t.Fatalf("first Run = %v", err)
	}

	tasksPath := filepath.Join(dir, ".corvex", "tasks", project, "tasks.md")
	tasks, spec, _ := task.ParseTasksFile(tasksPath)
	if spec.GeneratedBy != "corvex-recipe:fanout-demo" {
		t.Errorf("the fan-out rewrite lost the recipe marker: %q", spec.GeneratedBy)
	}
	m := byID(tasks)
	if m["S01"].Items == nil {
		t.Fatal("the discovery step did not persist its work list")
	}
	if len(m["S01"].Items) != 2 {
		t.Fatalf("persisted items = %v, want two", m["S01"].Items)
	}
	if _, ok := m["S02/000/apply"]; !ok {
		t.Fatal("expansion did not reach disk")
	}

	// Re-running must not expand again, even though the discovery command
	// would now answer differently.
	if err := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events}).Run(context.Background(), project); err != nil {
		t.Fatalf("second Run = %v", err)
	}
	close(events)
	after, _, _ := task.ParseTasksFile(tasksPath)
	if len(after) != len(tasks) {
		t.Errorf("resume changed the node count from %d to %d: %v", len(tasks), len(after), taskIDs(after))
	}
	for _, tk := range after {
		if strings.Contains(tk.ID, "/002/") {
			t.Errorf("resume re-expanded against a fresh discovery: found %s", tk.ID)
		}
	}
}

func taskIDs(tasks []types.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func TestGroupIntoWaves(t *testing.T) {
	if got, err := groupIntoWaves(nil, ""); err != nil || got != nil {
		t.Fatalf("groupIntoWaves(nil) = %v, %v", got, err)
	}
	got, err := groupIntoWaves([]string{"a", "b"}, "")
	if err != nil || len(got) != 1 || len(got[0].items) != 2 {
		t.Fatalf("no wave_by must mean one wave, got %v (%v)", got, err)
	}
	got, err = groupIntoWaves([]string{`{"w":"2"}`, `{"w":"1"}`}, "item.w")
	if err != nil {
		t.Fatalf("groupIntoWaves: %v", err)
	}
	if len(got) != 2 || got[0].key != "1" || got[1].key != "2" {
		t.Fatalf("waves are not in sorted key order: %v", got)
	}
	if _, err := groupIntoWaves([]string{`{"w":"1"}`}, "item.missing"); err == nil {
		t.Error("a missing wave field must be reported, not defaulted")
	}
}

func TestExpandedTasksMDStaysParseable(t *testing.T) {
	dir, tasks, err := runFanout(t, "fan-parse", "printf 'a\\n'", "")
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, ".corvex", "tasks", "fan-parse", "tasks.md"))
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	// The minted ids are the reason headingRe was opened; if they cannot be
	// read back, everything above is theatre.
	if !strings.Contains(string(raw), "## S02/000/apply") {
		t.Fatalf("minted heading missing from tasks.md:\n%s", raw)
	}
	if len(tasks) != 5 {
		t.Fatalf("round trip kept %d tasks, want 5: %v", len(tasks), taskIDs(tasks))
	}
}

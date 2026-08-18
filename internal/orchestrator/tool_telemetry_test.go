package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// newLedgerOrch builds an Orchestrator with an open ledger and no event
// consumer, which is the smallest thing that exercises emit's persistence
// decision without running a whole project.
func newLedgerOrch(t *testing.T) (*Orchestrator, string, string) {
	t.Helper()
	dir := t.TempDir()
	project := "telemetry"
	if err := os.MkdirAll(filepath.Join(dir, ".corvex", "tasks", project), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Project.Name = project
	o := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir})
	o.openLedger(project)
	if o.ledger == nil {
		t.Fatal("ledger did not open — the test would assert on an empty file")
	}
	return o, dir, project
}

func readLedger(t *testing.T, dir, project string) ([]activity.Entry, string) {
	t.Helper()
	entries, err := activity.Read(dir, project)
	if err != nil {
		t.Fatalf("reading ledger: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".corvex", "tasks", project, "activity.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading raw ledger: %v", err)
	}
	return entries, string(raw)
}

func toolStream(taskID string, se types.StreamEvent, at time.Time) Event {
	return Event{Type: EventTaskStream, TaskID: taskID, Stream: &se, Timestamp: at}
}

// TestToolLine_CarriesNameNeverInputOrPath is the tripwire for invariant 5:
// activity.jsonl is committed to the user's repository, so the tool's input
// summary and the path it touched must never reach it. It fails if someone
// starts persisting StreamEvent.Content or StreamEvent.File — by value and by
// key, so smuggling them into `message` fails too.
func TestToolLine_CarriesNameNeverInputOrPath(t *testing.T) {
	o, dir, project := newLedgerOrch(t)

	const secretPath = "/Users/someone/projects/private/deploy.yaml"
	const inputSummary = "cat /Users/someone/.aws/credentials"
	const resultBody = "AKIA0000EXAMPLE token=hunter2"

	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	o.emit(toolStream("S01", types.StreamEvent{
		Type: types.EventToolUse, Tool: "Bash", File: secretPath, Content: inputSummary,
	}, now))
	o.emit(toolStream("S01", types.StreamEvent{
		Type: types.EventToolResult, Content: resultBody,
	}, now.Add(time.Second)))

	entries, raw := readLedger(t, dir, project)
	if len(entries) != 2 {
		t.Fatalf("want 2 tool lines, got %d: %s", len(entries), raw)
	}
	for _, forbidden := range []string{secretPath, inputSummary, resultBody, "someone", "AKIA"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("ledger leaked %q — activity.jsonl is committed:\n%s", forbidden, raw)
		}
	}
	if entries[0].Tool != "Bash" {
		t.Errorf("tool_use line lost the tool name: %+v", entries[0])
	}

	// Key-level tripwire: a new key on a tool line means someone widened what
	// this committed file publishes and has to say so here.
	allowed := map[string]bool{"ts": true, "type": true, "task_id": true, "tool": true, "duration_ms": true, "run_id": true, "recipe": true, "phase": true}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		for k := range m {
			if !allowed[k] {
				t.Errorf("tool line published new key %q — justify it before allowing it: %s", k, line)
			}
		}
	}
}

// TestLedgerEntry_CarriesPhaseAndTool: `phase` has been a column of the on-disk
// schema since before F1 with nobody writing to it, which is why every cost in
// every ledger on disk is unattributed. The producer fills the event, this hop
// only has to not drop it — on ordinary lines and on tool lines alike. The
// expected values are spelled out as literals on purpose: comparing against the
// constant would still pass if someone renamed it and re-bucketed every past run.
func TestLedgerEntry_CarriesPhaseAndTool(t *testing.T) {
	o, dir, project := newLedgerOrch(t)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	o.emit(Event{Type: EventTaskComplete, TaskID: "S01", Status: types.StatusPassed,
		Phase: event.PhaseWorker, Tool: "Bash", CostUSD: 0.5, Timestamp: now})
	o.emit(Event{Type: EventReviewResult, TaskID: "S01", Phase: event.PhaseReview, Timestamp: now})
	ev := toolStream("S01", types.StreamEvent{Type: types.EventToolUse, Tool: "Read"}, now)
	ev.Phase = event.PhaseWorker
	o.emit(ev)

	entries, raw := readLedger(t, dir, project)
	if len(entries) != 3 {
		t.Fatalf("want 3 lines, got %d: %s", len(entries), raw)
	}
	if entries[0].Phase != "worker" || entries[0].Tool != "Bash" {
		t.Errorf("task_complete line dropped phase/tool: phase=%q tool=%q", entries[0].Phase, entries[0].Tool)
	}
	if entries[1].Phase != "review" {
		t.Errorf("review line phase = %q, want review", entries[1].Phase)
	}
	if entries[2].Phase != "worker" {
		t.Errorf("tool line must pass the producer's phase through, got %q", entries[2].Phase)
	}
}

// TestToolLine_PairsStartAndEndForDuration proves the closing line carries how
// long the call took and what it was called, from event timestamps rather than
// a wall clock read inside the rule.
func TestToolLine_PairsStartAndEndForDuration(t *testing.T) {
	o, dir, project := newLedgerOrch(t)
	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	o.emit(toolStream("S01", types.StreamEvent{Type: types.EventToolUse, Tool: "Read"}, base))
	o.emit(toolStream("S01", types.StreamEvent{Type: types.EventToolResult}, base.Add(1500*time.Millisecond)))

	entries, raw := readLedger(t, dir, project)
	if len(entries) != 2 {
		t.Fatalf("want 2 lines, got %d: %s", len(entries), raw)
	}
	if entries[0].Type != "tool_use" || entries[1].Type != "tool_result" {
		t.Fatalf("start and end must be distinguishable by type alone: %q / %q", entries[0].Type, entries[1].Type)
	}
	if entries[0].DurationMs != 0 {
		t.Errorf("the opening line cannot know a duration, got %d", entries[0].DurationMs)
	}
	if entries[1].DurationMs != 1500 {
		t.Errorf("duration_ms = %d, want 1500", entries[1].DurationMs)
	}
	// The provider drops the tool name on tool_result (protocol.go), so the
	// pairing is what makes per-tool accounting possible at all.
	if entries[1].Tool != "Read" {
		t.Errorf("closing line tool = %q, want Read from the paired start", entries[1].Tool)
	}
}

// TestToolLine_UnpairedEndHasNoFabricatedDuration: a result with no start on
// record (resumed run, dropped start) is written without a duration instead of
// with a zero that aggregates as "instant".
func TestToolLine_UnpairedEndHasNoFabricatedDuration(t *testing.T) {
	o, dir, project := newLedgerOrch(t)
	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	o.emit(toolStream("S01", types.StreamEvent{Type: types.EventToolResult, Tool: "Grep"}, base))

	entries, _ := readLedger(t, dir, project)
	if len(entries) != 1 || entries[0].DurationMs != 0 {
		t.Fatalf("want one line with no duration, got %+v", entries)
	}
}

// TestEmit_TextChunksStayOutOfTheLedger locks the rule that motivated the
// original blanket drop: text is the volume, and it is still not persisted.
func TestEmit_TextChunksStayOutOfTheLedger(t *testing.T) {
	o, dir, project := newLedgerOrch(t)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 100; i++ {
		o.emit(toolStream("S01", types.StreamEvent{Type: types.EventText, Content: "thinking out loud"}, now))
	}
	o.emit(toolStream("S01", types.StreamEvent{Type: types.EventDone, Content: "finished"}, now))
	o.emit(Event{Type: EventTaskStream, TaskID: "S01", Timestamp: now}) // no Stream payload at all

	entries, raw := readLedger(t, dir, project)
	if len(entries) != 0 {
		t.Fatalf("text/done chunks must not reach disk, got %d lines: %s", len(entries), raw)
	}
}

// TestToolLine_TruncatesLoudlyAtTheCeiling: the disk budget is enforced per
// task, and a truncated task says so in the file instead of going quiet.
func TestToolLine_TruncatesLoudlyAtTheCeiling(t *testing.T) {
	o, dir, project := newLedgerOrch(t)
	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	for i := 0; i < maxToolLinesPerTask*2; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		o.emit(toolStream("S01", types.StreamEvent{Type: types.EventToolUse, Tool: "Bash"}, at))
		o.emit(toolStream("S01", types.StreamEvent{Type: types.EventToolResult}, at.Add(time.Second)))
	}
	// A second task has its own budget: truncating one must not silence another.
	o.emit(toolStream("S02", types.StreamEvent{Type: types.EventToolUse, Tool: "Read"}, base))

	entries, _ := readLedger(t, dir, project)
	var toolLines, warns, s02 int
	for _, e := range entries {
		switch {
		case e.TaskID == "S02":
			s02++
		case e.Type == "tool_use" || e.Type == "tool_result":
			toolLines++
		case e.Type == string(EventTaskWarn):
			warns++
			if !strings.Contains(e.Message, fmt.Sprint(maxToolLinesPerTask)) {
				t.Errorf("truncation notice must carry the number: %q", e.Message)
			}
		}
	}
	if toolLines != maxToolLinesPerTask {
		t.Errorf("wrote %d tool lines, cap is %d", toolLines, maxToolLinesPerTask)
	}
	if warns != 1 {
		t.Errorf("want exactly one truncation notice, got %d", warns)
	}
	if s02 != 1 {
		t.Errorf("task S02 got %d lines — the budget is per task", s02)
	}
}

// TestToolTelemetry_DoesNotGrowWithoutBound covers the two memory bounds: a
// tool that never reports an end cannot pile up past the cap, and finishing a
// task releases what it was still holding.
func TestToolTelemetry_DoesNotGrowWithoutBound(t *testing.T) {
	o, _, _ := newLedgerOrch(t)
	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	for i := 0; i < maxPendingToolsPerTask*4; i++ {
		o.emit(toolStream("S01", types.StreamEvent{Type: types.EventToolUse, Tool: "Bash"}, base))
	}
	o.tools.mu.Lock()
	pending := len(o.tools.pending["S01"])
	o.tools.mu.Unlock()
	if pending != maxPendingToolsPerTask {
		t.Errorf("pending starts = %d, want the cap %d", pending, maxPendingToolsPerTask)
	}

	o.emit(Event{Type: EventTaskComplete, TaskID: "S01", Status: types.StatusPassed, Timestamp: base})
	o.tools.mu.Lock()
	_, still := o.tools.pending["S01"]
	o.tools.mu.Unlock()
	if still {
		t.Error("task_complete must release the unmatched starts of that task")
	}
}

// TestToolLine_StaysWithinDiskBudget is the measurement behind the number in
// maxToolLinesPerTask: if a tool line grows, the per-run ceiling written in that
// comment stops being true and has to be recomputed.
func TestToolLine_StaysWithinDiskBudget(t *testing.T) {
	dir := t.TempDir()
	project := "telemetry"
	if err := os.MkdirAll(filepath.Join(dir, ".corvex", "tasks", project), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	o := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir,
		Identity: activity.Identity{RunID: "ab12cd34", Recipe: "reference"}})
	o.openLedger(project)

	base := time.Now().UTC()
	o.emit(toolStream("S01", types.StreamEvent{Type: types.EventToolUse, Tool: "Bash"}, base))
	o.emit(toolStream("S01", types.StreamEvent{Type: types.EventToolResult}, base.Add(4200*time.Millisecond)))

	_, raw := readLedger(t, dir, project)
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if len(line)+1 > 160 {
			t.Errorf("tool line is %d bytes (budget 160) — recompute the ceiling in maxToolLinesPerTask: %s", len(line)+1, line)
		}
	}
}

// TestEmit_ParallelTasksPairToolsIndependently is the -race test: emit is called
// from one goroutine per running task, and pairing state shared across them
// would both race and mis-attribute durations. Each task's expected duration is
// distinct, so a cross-task pairing shows up as a wrong number, not just a
// detected race.
func TestEmit_ParallelTasksPairToolsIndependently(t *testing.T) {
	o, dir, project := newLedgerOrch(t)
	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	const tasks, calls = 8, 20
	var wg sync.WaitGroup
	for k := 0; k < tasks; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			taskID := fmt.Sprintf("S%02d", k)
			want := time.Duration(k+1) * 10 * time.Millisecond
			for i := 0; i < calls; i++ {
				at := base.Add(time.Duration(i) * time.Second)
				o.emit(toolStream(taskID, types.StreamEvent{Type: types.EventToolUse, Tool: "Bash"}, at))
				o.emit(toolStream(taskID, types.StreamEvent{Type: types.EventToolResult}, at.Add(want)))
			}
		}(k)
	}
	wg.Wait()

	entries, raw := readLedger(t, dir, project)
	if len(entries) != tasks*calls*2 {
		t.Fatalf("want %d lines, got %d", tasks*calls*2, len(entries))
	}
	seen := map[string]int{}
	for _, e := range entries {
		if e.Type != "tool_result" {
			continue
		}
		var k int
		if _, err := fmt.Sscanf(e.TaskID, "S%d", &k); err != nil {
			t.Fatalf("unexpected task id %q", e.TaskID)
		}
		want := int64(k+1) * 10
		if e.DurationMs != want {
			t.Fatalf("task %s: duration_ms = %d, want %d — pairing crossed tasks", e.TaskID, e.DurationMs, want)
		}
		seen[e.TaskID]++
	}
	if len(seen) != tasks {
		t.Errorf("closed calls for %d tasks, want %d (ledger has %d bytes)", len(seen), tasks, len(raw))
	}
}

// streamingProvider is a worker that reports tool calls, which the plain
// mockProvider cannot: the Worker only streams when the provider implements
// provider.ProgressStreamer.
type streamingProvider struct {
	*mockProvider
	stream []types.StreamEvent
	pause  time.Duration
}

func (s *streamingProvider) ExecuteWithProgress(ctx context.Context, req types.ExecuteRequest, onEvent func(types.StreamEvent)) (*types.ExecuteResult, error) {
	if onEvent != nil {
		for _, ev := range s.stream {
			if ev.Type == types.EventToolResult {
				time.Sleep(s.pause)
			}
			onEvent(ev)
		}
	}
	return s.mockProvider.Execute(ctx, req)
}

// TestRun_StubbedWorkerPersistsToolLines is the end-to-end proof: a real run,
// with a provider that reports tool calls the way the Claude CLI does, leaves
// tool telemetry in activity.jsonl with names and durations and without the
// paths those calls touched.
func TestRun_StubbedWorkerPersistsToolLines(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "tooling"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	const workerPath = "/Users/someone/projects/secret/main.go"
	base := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Looks good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "done" + taskReportBlock}, nil
		},
	}
	prov := &streamingProvider{
		mockProvider: base,
		pause:        2 * time.Millisecond,
		stream: []types.StreamEvent{
			{Type: types.EventText, Content: "let me look at the file"},
			{Type: types.EventToolUse, Tool: "Read", File: workerPath, Content: "Read(" + workerPath + ")"},
			{Type: types.EventToolResult, Content: "package main\n// secret sauce"},
			{Type: types.EventToolUse, Tool: "Bash", Content: "git status --porcelain"},
			{Type: types.EventToolResult, Content: " M main.go"},
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	events := make(chan Event, 500)
	orch := New(Options{Config: cfg, Provider: prov, WorkDir: dir, Events: events,
		Identity: activity.Identity{RunID: "ab12cd34", Recipe: "reference"}})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)
	for range events {
	}

	entries, raw := readLedger(t, dir, project)
	var uses, results, withDuration int
	tools := map[string]bool{}
	for _, e := range entries {
		switch e.Type {
		case "tool_use":
			uses++
			tools[e.Tool] = true
		case "tool_result":
			results++
			if e.DurationMs > 0 {
				withDuration++
			}
			tools[e.Tool] = true
		}
	}
	if uses == 0 || results == 0 {
		t.Fatalf("no tool telemetry reached the ledger (uses=%d results=%d):\n%s", uses, results, raw)
	}
	if uses != results {
		t.Errorf("every started tool should be closed: %d starts, %d ends", uses, results)
	}
	if !tools["Read"] || !tools["Bash"] {
		t.Errorf("tool names missing from the ledger, got %v", tools)
	}
	if withDuration != results {
		t.Errorf("%d of %d closing lines carry a duration", withDuration, results)
	}
	for _, forbidden := range []string{workerPath, "secret sauce", "git status --porcelain", "someone"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("run leaked %q into the committed ledger", forbidden)
		}
	}
	// The stream also carried text chunks; none of them may be on disk.
	if strings.Contains(raw, "let me look at the file") {
		t.Error("text chunks reached the ledger")
	}
}

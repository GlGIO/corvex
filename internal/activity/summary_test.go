package activity_test

import (
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// appendAll writes entries through a real Ledger, so every test in this file
// reduces over bytes that went to disk and came back — the aggregation is a
// claim about a file, not about a slice someone built in memory.
func appendAll(t *testing.T, workDir, project string, entries ...activity.Entry) {
	t.Helper()
	l, err := activity.New(workDir, project, activity.Identity{RunID: "run_f5a1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i, e := range entries {
		if err := l.Append(e); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
}

// The reduction recognises three ledger `type` values by literal string, because
// internal/activity must stay the leaf that everything else writes through and
// cannot import the event vocabulary. This test closes that gap from the outside:
// it builds the fixtures out of the producers' own constants, so renaming
// event.GateDecided (or the tool stream types) turns the aggregation red here
// instead of silently zeroing HumanWaitMs in every future run.
func TestSummarize_LiteralTypeNamesTrackTheProducerVocabulary(t *testing.T) {
	workDir, project := setupProjectDir(t)
	appendAll(t, workDir, project,
		activity.Entry{Type: string(event.TaskComplete), TaskID: "S01", Status: "PASSED", CostUSD: 0.30, DurationMs: 1000, Phase: event.PhaseWorker, Timestamp: time.Unix(1, 0).UTC()},
		activity.Entry{Type: string(event.GateDecided), TaskID: "S01", Status: "PASSED", DurationMs: 900_000, Phase: event.PhaseGate, Timestamp: time.Unix(2, 0).UTC()},
		activity.Entry{Type: string(types.EventToolUse), TaskID: "S01", Tool: "Bash", Phase: event.PhaseWorker, Timestamp: time.Unix(3, 0).UTC()},
		activity.Entry{Type: string(types.EventToolResult), TaskID: "S01", Tool: "Bash", DurationMs: 120, Phase: event.PhaseWorker, Timestamp: time.Unix(4, 0).UTC()},
	)

	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.HumanWaitMs != 900_000 {
		t.Errorf("HumanWaitMs = %d, want 900000: the gate_decided literal no longer matches event.GateDecided", s.HumanWaitMs)
	}
	if got := s.PerTool["Bash"]; got.Uses != 1 || got.DurationMs != 120 {
		t.Errorf("PerTool[Bash] = %+v, want 1 use / 120ms: the tool_use literal no longer matches types.EventToolUse", got)
	}
	if len(s.PerTask) != 1 {
		t.Errorf("PerTask = %+v, want S01: the task_complete literal no longer matches event.TaskComplete", s.PerTask)
	}
}

// The 2f bar: cost, clock and line count per nature, with the phase read off
// each line and every line counted exactly once.
func TestSummarize_PerPhaseSplitsCostAndClockByNature(t *testing.T) {
	workDir, project := setupProjectDir(t)
	appendAll(t, workDir, project,
		activity.Entry{Type: "plan_complete", Phase: event.PhasePlan, CostUSD: 0.04, DurationMs: 5_000, Timestamp: time.Unix(1, 0).UTC()},
		activity.Entry{Type: "task_start", TaskID: "S01", Phase: event.PhaseWorker, Timestamp: time.Unix(2, 0).UTC()},
		activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED", Phase: event.PhaseWorker, CostUSD: 0.20, DurationMs: 60_000, Timestamp: time.Unix(3, 0).UTC()},
		activity.Entry{Type: "review_result", TaskID: "S01", Phase: event.PhaseReview, CostUSD: 0.06, DurationMs: 9_000, Timestamp: time.Unix(4, 0).UTC()},
		activity.Entry{Type: "task_complete", TaskID: "S02", Status: "PASSED", Phase: event.PhaseWorker, CostUSD: 0.10, DurationMs: 30_000, Timestamp: time.Unix(5, 0).UTC()},
	)

	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	worker := s.PerPhase[event.PhaseWorker]
	if !sameCost(worker.CostUSD, 0.30) || worker.DurationMs != 90_000 || worker.Entries != 3 {
		t.Errorf("worker phase = %+v, want $0.30 / 90000ms / 3 lines (task_start counts as a line and costs nothing)", worker)
	}
	review := s.PerPhase[event.PhaseReview]
	if !sameCost(review.CostUSD, 0.06) || review.DurationMs != 9_000 || review.Entries != 1 {
		t.Errorf("review phase = %+v, want $0.06 / 9000ms / 1 line", review)
	}
	plan := s.PerPhase[event.PhasePlan]
	if !sameCost(plan.CostUSD, 0.04) || plan.Entries != 1 {
		t.Errorf("plan phase = %+v, want $0.04 / 1 line", plan)
	}
	if _, ok := s.PerPhase[activity.PhaseUnattributed]; ok {
		t.Errorf("every line carried a phase; the unattributed bucket must not exist: %+v", s.PerPhase)
	}
	if got := s.PerPhase[event.PhaseWorker].Phase; got != event.PhaseWorker {
		t.Errorf("PhaseMetric.Phase = %q, want the bucket to name itself so a marshalled row stands alone", got)
	}

	// The header keeps its own arithmetic: task-keyed winners only, so the
	// review and plan money is NOT in it. The two numbers answer different
	// questions and the test says so rather than asserting they agree.
	if !sameCost(s.TotalCostUSD, 0.30) {
		t.Errorf("TotalCostUSD = %v, want 0.30 (the task-keyed winners, unchanged by the new columns)", s.TotalCostUSD)
	}
}

// A line with no `phase` is every line written before F5. It must stay in the
// count, and it must land in a bucket that says it is unlabelled — not in
// "worker", which would assert something the file does not say.
func TestSummarize_PhaselessLinesGetTheirOwnNamedBucket(t *testing.T) {
	workDir, project := setupProjectDir(t)
	appendAll(t, workDir, project,
		// Pre-F5 shape: no phase key at all.
		activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED", CostUSD: 0.15, DurationMs: 8_000, Timestamp: time.Unix(1, 0).UTC()},
		activity.Entry{Type: "task_complete", TaskID: "S02", Status: "PASSED", CostUSD: 0.25, DurationMs: 4_000, Timestamp: time.Unix(2, 0).UTC()},
		// One migrated line alongside them: an upgrade mid-project produces exactly this file.
		activity.Entry{Type: "task_complete", TaskID: "S03", Status: "PASSED", Phase: event.PhaseWorker, CostUSD: 0.05, DurationMs: 1_000, Timestamp: time.Unix(3, 0).UTC()},
	)

	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	un := s.PerPhase[activity.PhaseUnattributed]
	if !sameCost(un.CostUSD, 0.40) || un.DurationMs != 12_000 || un.Entries != 2 {
		t.Errorf("unattributed bucket = %+v, want $0.40 / 12000ms / 2 lines: phase-less history must not vanish from the breakdown", un)
	}
	if _, ok := s.PerPhase[""]; ok {
		t.Error(`the empty string is a bucket key: a bar with a blank legend is how a whole population becomes invisible`)
	}
	if w := s.PerPhase[event.PhaseWorker]; !sameCost(w.CostUSD, 0.05) {
		t.Errorf("worker phase = %+v, want only the line that actually said worker ($0.05)", w)
	}
	if activity.PhaseUnattributed == event.PhaseWorker {
		t.Fatal("the unattributed bucket is named after a real phase; it would be indistinguishable from a labelled line")
	}
}

// Tool rows for 2d/2f: one use per tool_use line, and the clock taken from the
// result line that knows how long it took.
func TestSummarize_PerToolCountsUsesNotResultLines(t *testing.T) {
	workDir, project := setupProjectDir(t)
	appendAll(t, workDir, project,
		activity.Entry{Type: "tool_use", TaskID: "S01", Phase: event.PhaseWorker, Tool: "Read", Timestamp: time.Unix(1, 0).UTC()},
		activity.Entry{Type: "tool_result", TaskID: "S01", Phase: event.PhaseWorker, Tool: "Read", DurationMs: 30, Timestamp: time.Unix(2, 0).UTC()},
		activity.Entry{Type: "tool_use", TaskID: "S01", Phase: event.PhaseWorker, Tool: "Read", Timestamp: time.Unix(3, 0).UTC()},
		activity.Entry{Type: "tool_result", TaskID: "S01", Phase: event.PhaseWorker, Tool: "Read", DurationMs: 70, Timestamp: time.Unix(4, 0).UTC()},
		activity.Entry{Type: "tool_use", TaskID: "S01", Phase: event.PhaseWorker, Tool: "Bash", Timestamp: time.Unix(5, 0).UTC()},
		activity.Entry{Type: "tool_result", TaskID: "S01", Phase: event.PhaseWorker, Tool: "Bash", DurationMs: 4_000, Timestamp: time.Unix(6, 0).UTC()},
		// A tool that never returned — the run died holding it. The use still counts.
		activity.Entry{Type: "tool_use", TaskID: "S01", Phase: event.PhaseWorker, Tool: "Bash", Timestamp: time.Unix(7, 0).UTC()},
	)

	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got := s.PerTool["Read"]; got.Uses != 2 || got.DurationMs != 100 {
		t.Errorf("PerTool[Read] = %+v, want 2 uses / 100ms (counting result lines too would say 4)", got)
	}
	if got := s.PerTool["Bash"]; got.Uses != 2 || got.DurationMs != 4_000 {
		t.Errorf("PerTool[Bash] = %+v, want 2 uses / 4000ms: a tool that never returned is still a use", got)
	}
	if len(s.PerTool) != 2 {
		t.Errorf("PerTool = %+v, want exactly Read and Bash", s.PerTool)
	}
	if got := s.PerTool["Read"].Tool; got != "Read" {
		t.Errorf("ToolMetric.Tool = %q, want the row to name itself", got)
	}
}

// A tool's clock is nested inside the step that invoked it. Both live in the
// same phase, so adding the tool lines to the phase would count those seconds
// twice — the phase bar would then exceed the run and nobody could tell why.
func TestSummarize_ToolClockIsNestedAndDoesNotInflateItsPhase(t *testing.T) {
	workDir, project := setupProjectDir(t)
	appendAll(t, workDir, project,
		activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED", Phase: event.PhaseWorker, CostUSD: 0.20, DurationMs: 60_000, Timestamp: time.Unix(1, 0).UTC()},
		activity.Entry{Type: "tool_use", TaskID: "S01", Phase: event.PhaseWorker, Tool: "Bash", Timestamp: time.Unix(2, 0).UTC()},
		activity.Entry{Type: "tool_result", TaskID: "S01", Phase: event.PhaseWorker, Tool: "Bash", DurationMs: 45_000, Timestamp: time.Unix(3, 0).UTC()},
	)

	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	w := s.PerPhase[event.PhaseWorker]
	if w.DurationMs != 60_000 {
		t.Errorf("worker phase clock = %dms, want 60000: the 45s inside the tool is already part of the task's 60s", w.DurationMs)
	}
	if w.Entries != 3 {
		t.Errorf("worker phase lines = %d, want 3: a line count is not a nested quantity and still counts the tool lines", w.Entries)
	}
	if s.PerTool["Bash"].DurationMs != 45_000 {
		t.Errorf("PerTool[Bash] clock = %dms, want 45000: the nested number is not dropped, it is reported where it belongs", s.PerTool["Bash"].DurationMs)
	}
}

// 2g. The point of the whole field: a run that took four hours of which three
// hours fifty was a person asleep is not a slow run, and today the two clocks
// are the same number.
func TestSummarize_HumanWaitIsSeparableFromTheMachineClock(t *testing.T) {
	workDir, project := setupProjectDir(t)
	const (
		machineMs = 10 * 60 * 1000          // ten minutes of actual work
		waitMs    = (3*60 + 50) * 60 * 1000 // three hours fifty of nobody
	)
	appendAll(t, workDir, project,
		activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED", Phase: event.PhaseWorker, CostUSD: 0.20, DurationMs: machineMs, Timestamp: time.Unix(1, 0).UTC()},
		activity.Entry{Type: "gate_pending", TaskID: "S01", Phase: event.PhaseGate, Timestamp: time.Unix(2, 0).UTC()},
		activity.Entry{Type: "gate_decided", TaskID: "S01", Status: "PASSED", Phase: event.PhaseGate, DurationMs: waitMs, Timestamp: time.Unix(3, 0).UTC()},
	)

	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.HumanWaitMs != waitMs {
		t.Errorf("HumanWaitMs = %d, want %d", s.HumanWaitMs, waitMs)
	}
	// The machine clock is what is left once the gate phase is set aside. If the
	// human wait had leaked into a working phase there would be no way back to it.
	var machine int64
	for phase, m := range s.PerPhase {
		if phase == event.PhaseGate {
			continue
		}
		machine += m.DurationMs
	}
	if machine != machineMs {
		t.Errorf("machine clock = %dms, want %dms: the human wait is inside a working phase and no longer subtractable", machine, machineMs)
	}
	if s.HumanWaitMs <= machine {
		t.Fatalf("fixture is not the interesting case any more (wait %d <= work %d): the whole field exists for the run where waiting dominates", s.HumanWaitMs, machine)
	}
}

// The --approve-gates path decides the gate without anybody reading it, and
// emits the line with no clock. Zero is the honest answer, not a missing line.
func TestSummarize_AutoApprovedGateAddsNoHumanWait(t *testing.T) {
	workDir, project := setupProjectDir(t)
	appendAll(t, workDir, project,
		activity.Entry{Type: "gate_decided", TaskID: "S01", Status: "PASSED", Phase: event.PhaseGate, Timestamp: time.Unix(1, 0).UTC()},
	)

	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.HumanWaitMs != 0 {
		t.Errorf("HumanWaitMs = %d, want 0: nobody waited", s.HumanWaitMs)
	}
	if s.PerPhase[event.PhaseGate].Entries != 1 {
		t.Errorf("the gate line must still be counted: %+v", s.PerPhase)
	}
}

// The divergence between the two reductions, pinned deliberately: a task re-run
// by a later run replaces its own numbers in the task-keyed total (that is what
// keeps resume from reading $0.00) and adds to the line-keyed breakdown (that
// money was really spent). Anyone who "fixes" this so the two agree has either
// made the header double count resumes or made the breakdown pretend the first
// attempt was free.
func TestSummarize_SupersededAttemptCountsInThePhaseBarAndNotInTheHeader(t *testing.T) {
	workDir, project := setupProjectDir(t)
	appendAll(t, workDir, project,
		activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED", Phase: event.PhaseWorker, CostUSD: 0.20, DurationMs: 2_000, Timestamp: time.Unix(1, 0).UTC()},
		// A later run re-runs the same task. Same task_id, new money.
		activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED", Phase: event.PhaseWorker, CostUSD: 0.30, DurationMs: 3_000, Timestamp: time.Unix(2, 0).UTC()},
	)

	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !sameCost(s.TotalCostUSD, 0.30) || s.PerTask["S01"].DurationMs != 3_000 {
		t.Errorf("header = %v / %+v, want the latest attempt only ($0.30)", s.TotalCostUSD, s.PerTask["S01"])
	}
	w := s.PerPhase[event.PhaseWorker]
	if !sameCost(w.CostUSD, 0.50) || w.DurationMs != 5_000 || w.Entries != 2 {
		t.Errorf("worker phase = %+v, want $0.50 / 5000ms / 2 lines: superseded effort is effort", w)
	}
	if sameCost(w.CostUSD, s.TotalCostUSD) {
		t.Fatal("the fixture stopped being a resume: pick different costs or this proves nothing")
	}
}

// The new columns are computed inside the same reduction as the totals, over the
// same entry slice, so they inherit the cumulative/per-run split instead of
// inventing a second scoping rule — which is exactly how the "$0.00 after
// resume" hole would reopen in a new column.
func TestSummarizeRun_PhaseAndToolFollowTheRunScope(t *testing.T) {
	workDir, project := setupProjectDir(t)
	for _, runID := range []string{"run_aaaa", "run_bbbb"} {
		l, err := activity.New(workDir, project, activity.Identity{RunID: runID})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := l.Append(activity.Entry{Type: "task_complete", TaskID: "S-" + runID, Status: "PASSED", Phase: event.PhaseWorker, CostUSD: 0.10, DurationMs: 1_000}); err != nil {
			t.Fatal(err)
		}
		if err := l.Append(activity.Entry{Type: "tool_use", TaskID: "S-" + runID, Phase: event.PhaseWorker, Tool: "Read"}); err != nil {
			t.Fatal(err)
		}
		if err := l.Append(activity.Entry{Type: "gate_decided", TaskID: "S-" + runID, Status: "PASSED", Phase: event.PhaseGate, DurationMs: 60_000}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !sameCost(all.PerPhase[event.PhaseWorker].CostUSD, 0.20) || all.PerTool["Read"].Uses != 2 || all.HumanWaitMs != 120_000 {
		t.Errorf("cumulative view = %+v: must span both runs, like the totals do", all)
	}

	one, err := activity.SummarizeRun(workDir, project, "run_bbbb")
	if err != nil {
		t.Fatalf("SummarizeRun: %v", err)
	}
	if !sameCost(one.PerPhase[event.PhaseWorker].CostUSD, 0.10) || one.PerTool["Read"].Uses != 1 || one.HumanWaitMs != 60_000 {
		t.Errorf("per-run view = %+v: must hold one run's share", one)
	}
}

// An empty ledger has to hand back usable maps, not nil ones: the caller
// marshals this straight to the UI and `null` where `{}` belongs is a crash on
// the other side of the wire.
func TestSummarize_EmptyLedgerStillReturnsUsableMaps(t *testing.T) {
	workDir, project := setupProjectDir(t)
	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.PerPhase == nil || s.PerTool == nil || s.PerTask == nil {
		t.Errorf("summary of an empty ledger has a nil map: %+v", s)
	}
	if len(s.PerPhase) != 0 || len(s.PerTool) != 0 || s.HumanWaitMs != 0 {
		t.Errorf("summary of an empty ledger is not empty: %+v", s)
	}
}

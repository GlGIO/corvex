package cmd

// Characterization goldens for `corvex run` (plus its gate helpers in
// run_gate.go and the escalation listing in review.go).
//
// Everything here uses the harness in characterize_test.go: one golden per
// invocation, transcript() so argv + both streams + the returned error live in
// the same file, and scrub() for the genuinely non-deterministic bits.
//
// HARD RULES observed in this file:
//   - never t.Parallel() (the harness chdirs and Setenvs);
//   - stubClaude() BEFORE any invocation that can reach the provider — `run`
//     without --dry-run calls the real Claude CLI otherwise;
//   - the goldens record what the CLI does today, bugs included (e.g. --dry-run
//     silently ignoring --task, or "failed . " with a dangling separator).
//
// One deviation from "everything in the transcript": stdout produced by the
// PlainRenderer is NOT in the golden, because runRun never waits for the drain
// goroutine and the lines are lost or kept depending on the scheduler. See
// runRendererPlaceholder / runAssertRendererLines below.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/ops"
)

// ── local fixtures ───────────────────────────────────────────────────────────

// runTasksAllPassedMD is a one-task DAG already PASSED. It is the only way to
// walk `run` end to end (DAG resolve → scheduler → PostRun hook → EventDone)
// without a single provider call: the scheduler finds nothing ready and exits
// through the success path.
const runTasksAllPassedMD = "---\ngenerated_by: characterize\ndag:\n  S01: []\n---\n\n" +
	"## S01 — First Task ✅ PASSED\n\n" +
	"```yaml\ntype: general\n```\n\n" +
	"### O que fazer\nFirst task\n\n" +
	"### Critérios de sucesso\n- [ ] Done\n"

// runTasksBothPendingMD keeps S01 PENDING so S02 (which depends on it) is not
// schedulable — the input for the "--task <id> dependencies not met" path.
const runTasksBothPendingMD = "---\ngenerated_by: characterize\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
	"## S01 — First Task ⬜ PENDING\n\n" +
	"```yaml\ntype: general\n```\n\n" +
	"### O que fazer\nFirst task\n\n" +
	"### Critérios de sucesso\n- [ ] Done\n\n---\n\n" +
	"## S02 — Second Task ⬜ PENDING\n\n" +
	"```yaml\ntype: backend\ndepends_on: [S01]\n```\n\n" +
	"### O que fazer\nSecond task\n\n" +
	"### Critérios de sucesso\n- [ ] Done\n"

// runSeedAnchor writes an anchor.yaml whose spec_hash matches spec, which is
// what makes orchestrator.needsPlanning() return false. Without it, any `run`
// on a project that has spec.md goes straight to the planner (an AI call), so
// this is the switch that unlocks the post-planning paths for testing.
func runSeedAnchor(f *fixture, project, spec string) *fixture {
	sum := sha256.Sum256([]byte(spec))
	return f.Write(
		filepath.Join(".corvex", "tasks", project, "anchor.yaml"),
		fmt.Sprintf("project: %s\nupdated_at: \"2026-01-02T03:04:05Z\"\nspec_hash: %s\nintent: characterize\n",
			project, hex.EncodeToString(sum[:])),
	)
}

// runLedgerSpend is a fixed pair of ledger entries so the "already spent"
// clause of the preview has a deterministic amount.
func runLedgerSpend() []activity.Entry {
	return []activity.Entry{
		{Type: "task_complete", TaskID: "S01", Status: "PASSED",
			DurationMs: 8000, CostUSD: 0.15, TokensIn: 600, TokensOut: 300},
	}
}

// runFileExists reports whether path exists (used to assert the destructive
// side effect of --force).
func runFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runMkdirAll creates dir, failing the test on error.
func runMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

// runScrubDir is scrub() with the fixture root collapsed FIRST, so the
// interesting tail of a path (.corvex/tasks/alpha/tasks.md, the -alpha worktree
// suffix) survives into the golden instead of being swallowed whole by scrub's
// temp-root pattern.
// The symlink-resolved form goes first: on macOS the CLI prints
// /private/var/folders/... while fixture.Dir is /var/folders/..., and replacing
// the shorter one first would leave a stray "/private" prefix in the golden.
func runScrubDir(s, dir string, skip ...string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		s = scrubPath(s, resolved)
	}
	s = scrubPath(s, dir)
	return scrubExcept(s, skip...)
}

// runRendererPlaceholder stands in for stdout in goldens whose stdout comes from
// the PlainRenderer.
//
// WHY this is not the real stdout: runRun starts renderer.Drain(events) on a
// goroutine, then returns as soon as orc.Run does — it never closes the event
// channel and never waits for the drain. Whether the queued lines reach stdout
// is therefore decided by the Go scheduler: with GOMAXPROCS>1 they usually make
// it, with GOMAXPROCS=1 they are always lost. Putting them in the golden makes
// the golden flake, so the deterministic half of the invocation (the preview
// line and warnings on stderr, plus the returned error) lives in the golden and
// the renderer lines are checked by runAssertRendererLines instead.
const runRendererPlaceholder = "<PlainRenderer output — drained on an unawaited goroutine; see runAssertRendererLines>"

// runRendererTranscript is transcript() with the stdout block replaced by the
// placeholder above.
func runRendererTranscript(args []string, stderr string, err error) string {
	return transcript(args, runRendererPlaceholder, stderr, err)
}

// runAssertRendererLines checks the PlainRenderer lines that did reach stdout.
//
// The renderer writes events in order and can be cut off at any point by the
// race described above, so the only invariant that holds is "what arrived is a
// prefix of what was expected". That still fails loudly when a line's text or
// order changes (a real output regression); it only tolerates truncation.
//
// DO NOT turn the t.Logf below into t.Errorf. It looks like a hole and it is
// one, but closing it here makes ~15 call sites go red in proportion to CI load
// (measured: 0-11 of them come back empty depending on GOMAXPROCS and machine
// load), and a suite that fails at random teaches a refactorer to ignore red.
// The hole is closed ONCE, deterministically, in char_run_drain_test.go
// (TestCharacterizeRunRendererNotSilenced) — read the header of that file before
// touching this function.
func runAssertRendererLines(t *testing.T, stdout string, want ...string) {
	t.Helper()

	got := strings.Split(stdout, "\n")
	// Split leaves a trailing "" for the final newline; renderer lines
	// themselves may end in a significant space, so nothing else is trimmed.
	if len(got) > 0 && got[len(got)-1] == "" {
		got = got[:len(got)-1]
	}
	if len(got) == 0 {
		t.Logf("no PlainRenderer lines reached stdout (the unawaited drain lost them); wanted up to %d line(s): %q", len(want), want)
		return
	}
	if len(got) > len(want) {
		t.Errorf("PlainRenderer printed %d line(s), want at most %d\ngot:  %q\nwant: %q", len(got), len(want), got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("PlainRenderer line %d = %q, want %q\ngot:  %q\nwant: %q", i+1, got[i], want[i], got, want)
			return
		}
	}
}

// runBakSuffix matches the timestamp the replan backup appends
// (tasks.md.bak-20060102-150405); scrub() does not know this format.
var runBakSuffix = regexp.MustCompile(`\.bak-\d{8}-\d{6}`)

// runListProjectDir renders the sorted contents of .corvex/tasks/<project>/ as a
// golden body, so a command's file-level side effects are locked too.
func runListProjectDir(t *testing.T, f *fixture, project string) string {
	t.Helper()
	dir := f.Path(".corvex", "tasks", project)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	body := fmt.Sprintf(".corvex/tasks/%s/\n  %s\n", project, strings.Join(names, "\n  "))
	return runBakSuffix.ReplaceAllString(body, ".bak-<TS>")
}

// runStubRefuse is the standard non-AI provider: any provider invocation
// (planner, worker, reviewer, griller) dies with exit 3 and a fixed stderr
// line, so the failure text in the goldens is stable.
const runStubRefuse = `echo "stub claude: refusing" >&2; exit 3`

// ── main path: --dry-run ─────────────────────────────────────────────────────

// The cheapest complete run path: parses tasks.md, validates and resolves the
// DAG, prints the execution order. Never constructs a provider.
func TestCharacterizeRunDryRun(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"run", "alpha", "--dry-run"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_dryrun", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// --dry-run + --task: the golden records that --task is IGNORED by the dry-run
// branch (runRun returns dryRun() before the orchestrator ever sees TargetTask),
// so the output is the full DAG, not the single task. See bugsObserved.
func TestCharacterizeRunDryRunIgnoresTask(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"run", "alpha", "--dry-run", "--task", "S99"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_dryrun_ignores_task", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// Dry run on a project that has spec.md but no tasks.md: the "not found" guard
// passes (spec.md exists) and the failure comes from the parser instead.
func TestCharacterizeRunDryRunNoTasksFile(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, "")

	args := []string{"run", "alpha", "--dry-run"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_dryrun_no_tasks_file", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// ── main path: full run, no AI ───────────────────────────────────────────────

// The end-to-end shape of `corvex run <project>` (invariant 3 of the roadmap):
// doctor preflight → cost preview → recovery guard → planning skipped via the
// anchor → DAG resolved → nothing left to schedule → success, exit 0.
func TestCharacterizeRunAllTasksDone(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksAllPassedMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ plan ready (1 tasks)", "+ done")
	// "cost" is left unscrubbed: the ceilings come from config.yaml, and the
	// preview line is the contract users read before spending money.
	goldenAssert(t, "run_all_tasks_done", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir, "cost"))
}

// Same as above but with an already-spent ledger, which appends the
// "· already spent" clause to the preview line.
func TestCharacterizeRunPreviewWithSpend(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksAllPassedMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	f.AddLedger("alpha", runLedgerSpend()...)

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ plan ready (1 tasks)", "+ done")
	goldenAssert(t, "run_preview_with_spend", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir, "cost"))
}

// ── main path: a task that actually PASSES ───────────────────────────────────

// runStubPass is a fake `claude` that speaks the stream-json protocol the
// provider parses, with a payload that satisfies BOTH consumers in one string:
// the Worker's TASK-REPORT parser and the Reviewer's "VERDICT: PASS" parser. The
// fixed duration_ms/total_cost_usd make the rendered duration and cost
// deterministic, so nothing here needs scrubbing.
// The text has to come on an "assistant" line: the provider assembles
// ExecuteResult.Output from streamed text events and ignores the result line's
// "result" field (verified — with only a result line, Output is empty and the
// reviewer reports "never produced a verdict").
const runStubPass = `cat <<'JSON'
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Implemented it.\n\nTASK-REPORT\nSUMMARY: implemented the first task\nDECISIONS: chose the boring option\nHANDOFF: nothing pending\n\nVERDICT: PASS\n"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"done","total_cost_usd":0.02,"total_input_tokens":100,"total_output_tokens":50,"duration_ms":1000}
JSON`

// runTasksOnePendingMD is a single PENDING task with no dependencies: the
// smallest project where a task can actually run to completion.
const runTasksOnePendingMD = "---\ngenerated_by: characterize\ndag:\n  S01: []\n---\n\n" +
	"## S01 — First Task ⬜ PENDING\n\n" +
	"```yaml\ntype: general\n```\n\n" +
	"### O que fazer\nFirst task\n\n" +
	"### Critérios de sucesso\n- [ ] Done\n"

// The one golden that proves a task can be executed end to end: worker →
// reviewer → checkpoint → anchor → done, exit 0. Cost and duration are locked
// unscrubbed because the fake provider reports fixed values, which also makes
// this the golden that would catch a change in cost accounting — this line is
// the WORKER's own $0.02; the reviewer's other $0.02 now rides on the
// review_result line instead of being folded in here (see internal/step's
// task_complete emission).
func TestCharacterizeRunTaskPasses(t *testing.T) {
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout,
		"+ plan ready (1 tasks)",
		"> S01  First Task",
		"+ S01  passed . 2s . $0.02",
		"+ done",
	)
	goldenAssert(t, "run_task_passes", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir, "cost", "dur"))
}

// The disk state left behind by the passing run above: tasks.md flipped to
// PASSED and anchor.yaml carrying the worker's HANDOFF/SUMMARY forward. This is
// the state the NEXT run reads, so locking it is what makes the refactor safe.
func TestCharacterizeRunTaskPassesDiskState(t *testing.T) {
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("expected the fake provider to pass the task, got %v", err)
	}
	body := "--- tasks.md ---\n" + f.Read(filepath.Join(".corvex", "tasks", "alpha", "tasks.md")) +
		"\n--- anchor.yaml ---\n" + f.Read(filepath.Join(".corvex", "tasks", "alpha", "anchor.yaml"))
	goldenAssert(t, "run_task_passes_disk_state", runScrubDir(body, f.Dir))
}

// ── the planner gate (legacy spec.md flow) ───────────────────────────────────

// The legacy flow: user writes spec.md by hand and runs. With no anchor.yaml,
// the spec hash never matches, so `run` replans — even though tasks.md exists,
// which also means tasks.md is renamed to a .bak-<timestamp> before the planner
// is invoked. The stub makes the planner fail, so the golden locks the error
// wrapping chain (run → planning → planner execution → claude cli).
func TestCharacterizeRunPlanningFails(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "* planning...")
	goldenAssert(t, "run_planning_fails", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir))
}

// The disk side effect of the failed replan above, which the error message does
// not mention: tasks.md was renamed to tasks.md.bak-<timestamp> BEFORE the
// planner ran, so a project whose planning failed is left with no tasks.md at
// all. Recorded as-is (see bugsObserved).
func TestCharacterizeRunReplanBackupSideEffect(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err == nil {
		t.Fatal("expected the stubbed planner to fail")
	}
	goldenAssert(t, "run_replan_backup_side_effect", runListProjectDir(t, f, "alpha"))
}

// --no-replan turns the same spec-drift situation into a refusal that costs
// nothing: no provider call, tasks.md untouched.
func TestCharacterizeRunNoReplanRefuses(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()

	args := []string{"run", "alpha", "--plain", "--yes", "--no-replan"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_no_replan", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// ── --task scoping ───────────────────────────────────────────────────────────

// --task with an id that is not in tasks.md at all.
func TestCharacterizeRunTaskNotFound(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksBothPendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes", "--task", "S99"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ plan ready (2 tasks)")
	goldenAssert(t, "run_task_not_found", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir))
}

// --task on a task whose dependency has not PASSED: refused before any worker
// runs, so this is a real (free) characterization of the scheduler's filter.
func TestCharacterizeRunTaskDepsNotMet(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksBothPendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes", "--task", "S02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ plan ready (2 tasks)")
	goldenAssert(t, "run_task_deps_not_met", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir))
}

// --task on a schedulable task: the worker is reached and the stub provider
// fails it. maxRetries is 2 in the fixture config, so the task is attempted 3
// times and the error reports "failed after 3 attempts".
func TestCharacterizeRunTaskWorkerFails(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes", "--task", "S02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	// The last line really does end in "failed . " with a trailing space: the
	// failure event carries no Message, and the renderer prints the separator
	// unconditionally. Recorded, not fixed (see bugsObserved).
	runAssertRendererLines(t, stdout,
		"+ plan ready (2 tasks)",
		"> S02  Second Task",
		"~ S02  retry 1",
		"> S02  Second Task",
		"~ S02  retry 2",
		"> S02  Second Task",
		"! S02  failed . ",
	)
	goldenAssert(t, "run_task_worker_fails", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir))
}

// tasks.md is rewritten as the attempt progresses: S02 ends FAILED. Locking the
// file content proves the on-disk side effect, not just the message.
func TestCharacterizeRunTasksFileAfterFailure(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes", "--task", "S02"); err == nil {
		t.Fatal("expected the stubbed provider to fail the task")
	}
	goldenAssert(t, "run_tasks_md_after_failure",
		scrub(f.Read(filepath.Join(".corvex", "tasks", "alpha", "tasks.md"))))
}

// --single scopes the run to the first ready task (S01 here) instead of walking
// the DAG. The stub fails it, which also exercises the retry loop.
func TestCharacterizeRunSingle(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksBothPendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes", "--single"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout,
		"+ plan ready (2 tasks)",
		"> S01  First Task",
		"~ S01  retry 1",
		"> S01  First Task",
		"~ S01  retry 2",
		"> S01  First Task",
		"! S01  failed . ",
	)
	goldenAssert(t, "run_single", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir))
}

// Without --plain: stdout is a pipe, so isInteractive() is false and runRun must
// still take the PlainRenderer branch instead of starting the bubbletea TUI.
// This golden is the guard against a refactor that launches the TUI in CI.
func TestCharacterizeRunWithoutPlainFlag(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksAllPassedMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ plan ready (1 tasks)", "+ done")
	goldenAssert(t, "run_no_plain_flag", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir, "cost"))
}

// ── malformed task graphs ────────────────────────────────────────────────────

// A dependency cycle: caught by dag.Validate before anything executes. --dry-run
// is enough to reach it.
func TestCharacterizeRunDryRunCycle(t *testing.T) {
	cyclic := "---\ngenerated_by: characterize\ndag:\n  S01: [S02]\n  S02: [S01]\n---\n\n" +
		"## S01 — First Task ⬜ PENDING\n\n```yaml\ntype: general\ndepends_on: [S02]\n```\n\n" +
		"### O que fazer\nFirst task\n\n### Critérios de sucesso\n- [ ] Done\n\n---\n\n" +
		"## S02 — Second Task ⬜ PENDING\n\n```yaml\ntype: general\ndepends_on: [S01]\n```\n\n" +
		"### O que fazer\nSecond task\n\n### Critérios de sucesso\n- [ ] Done\n"
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, cyclic)

	args := []string{"run", "alpha", "--dry-run"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_dryrun_cycle", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// A tasks.md the planner filled with prose instead of task headings. The
// orchestrator must refuse rather than report a successful empty run.
func TestCharacterizeRunZeroTasks(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD,
		"---\ngenerated_by: characterize\ndag: {}\n---\n\nI thought about it and here is my plan in prose.\n").GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_zero_tasks", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// A PASSED task whose dependency is still PENDING — the shape a replan can
// leave behind. The run aborts with the two-option repair message.
func TestCharacterizeRunDAGIntegrityViolation(t *testing.T) {
	stubClaude(t, runStubRefuse)
	broken := "---\ngenerated_by: characterize\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — First Task ⬜ PENDING\n\n```yaml\ntype: general\n```\n\n" +
		"### O que fazer\nFirst task\n\n### Critérios de sucesso\n- [ ] Done\n\n---\n\n" +
		"## S02 — Second Task ✅ PASSED\n\n```yaml\ntype: general\ndepends_on: [S01]\n```\n\n" +
		"### O que fazer\nSecond task\n\n### Critérios de sucesso\n- [ ] Done\n"
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, broken).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ plan ready (2 tasks)")
	goldenAssert(t, "run_dag_integrity", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir))
}

// tasks.md without spec.md: needsPlanning() short-circuits on the missing spec,
// so no anchor is needed and the planner is never consulted. This is the oldest
// supported shape of a project and it must keep running.
func TestCharacterizeRunTasksOnlyNoSpec(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", "", runTasksAllPassedMD).GitInit()

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ plan ready (1 tasks)", "+ done")
	goldenAssert(t, "run_tasks_only_no_spec", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir, "cost"))
}

// ── dirty working tree ───────────────────────────────────────────────────────

// The guard that must refuse: an uncommitted user file aborts the run before
// planning. .corvex/ is deliberately excluded from the count by the recovery
// manager, so only dirty.txt shows up.
func TestCharacterizeRunDirtyTreeRefuses(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	f.Write("dirty.txt", "uncommitted work\n")

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_dirty_tree", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// --force takes the destructive branch: recovery.Check() resets the tree and
// the run proceeds (here into the empty scheduler, since the anchor skips
// planning). The golden also documents that the untracked file is gone.
func TestCharacterizeRunDirtyTreeForce(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksAllPassedMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	f.Write("dirty.txt", "uncommitted work\n")

	args := []string{"run", "alpha", "--plain", "--yes", "--force"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	// Note what is NOT here: the orchestrator emits EventRecoveryResult with
	// "--force: discarded 1 uncommitted change(s)", but PlainRenderer has no case
	// for that event, so the destructive step is silent (see bugsObserved).
	runAssertRendererLines(t, stdout, "+ plan ready (1 tasks)", "+ done")
	got := runRendererTranscript(args, stderr, err)
	got += fmt.Sprintf("\n--- dirty.txt still present ---\n%v\n", runFileExists(f.Path("dirty.txt")))
	goldenAssert(t, "run_dirty_tree_force", runScrubDir(got, f.Dir, "cost"))
}

// Outside a git repo the guard cannot run at all: it logs a WARN and the run
// continues. Locking this keeps the "corvex works in a plain directory"
// behaviour honest.
func TestCharacterizeRunNoGitRepo(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksAllPassedMD)
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ plan ready (1 tasks)", "+ done")
	goldenAssert(t, "run_no_git_repo", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir, "cost"))
}

// ── argument / environment errors ────────────────────────────────────────────

// No .corvex/ anywhere: the first hard stop, with the worktree hint.
func TestCharacterizeRunNoCorvexDir(t *testing.T) {
	args := []string{"run", "alpha"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "run_no_corvex_dir", scrub(transcript(args, stdout, stderr, err)))
}

// Unknown project, with other projects around: lists them and (because "ghost"
// is far from every name) offers no suggestion.
func TestCharacterizeRunProjectNotFound(t *testing.T) {
	f := newFixture(t).
		AddProject("alpha", fixtureSpecMD, fixtureTasksMD).
		AddProject("beta", fixtureSpecMD, "")

	args := []string{"run", "ghost"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_project_not_found", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// A typo close enough for suggestProject to fire.
func TestCharacterizeRunProjectTypo(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"run", "alpah"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_project_typo", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// .corvex/ exists but holds no project at all: no "available projects" clause.
func TestCharacterizeRunNoProjectsAtAll(t *testing.T) {
	f := newFixture(t)

	args := []string{"run", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_no_projects", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// cobra's own arity error (run takes exactly one project).
func TestCharacterizeRunMissingArg(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"run"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_missing_arg", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// A worktree created by `corvex start` exists as a sibling directory, but the
// user is running from the main repo: refuse with the cd hint.
func TestCharacterizeRunWorktreeMismatch(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	// The sibling path convention is <parent>/<repo>-<project>; it only has to
	// exist as a directory for the guard to trip.
	runMkdirAll(t, ops.WorktreePath(f.Dir, "alpha"))

	args := []string{"run", "alpha", "--dry-run"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_worktree_mismatch", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// --here is the documented escape hatch for the case above.
func TestCharacterizeRunWorktreeMismatchHere(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	runMkdirAll(t, ops.WorktreePath(f.Dir, "alpha"))

	args := []string{"run", "alpha", "--dry-run", "--here"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_worktree_mismatch_here", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// ── --ab validation ──────────────────────────────────────────────────────────

// --ab without a scope: rejected after the provider is built but before any
// call, so it is free to characterize.
func TestCharacterizeRunABWithoutTask(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()

	args := []string{"run", "alpha", "--plain", "--yes", "--ab", "sonnet,opus"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_ab_without_task", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// --ab with a malformed model list.
func TestCharacterizeRunABBadModels(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()

	args := []string{"run", "alpha", "--plain", "--yes", "--task", "S02", "--ab", "sonnet"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_ab_bad_models", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// ── run_gate.go: doctor preflight ────────────────────────────────────────────

// A config that fails a doctor check aborts the run with the check list. This
// is the only golden that exercises doctorGate's error formatting through the
// CLI.
func TestCharacterizeRunDoctorGateFails(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(filepath.Join(".corvex", "config.yaml"),
		"project:\n  name: fixture\nprovider:\n  default: bogus-provider\nsandbox:\n  type: local\n")

	args := []string{"run", "alpha", "--plain", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_doctor_gate_fails", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// --skip-doctor bypasses the gate above; the run then dies further along (in
// the planner, because the bogus provider cannot be constructed).
func TestCharacterizeRunSkipDoctor(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(filepath.Join(".corvex", "config.yaml"),
		"project:\n  name: fixture\nprovider:\n  default: bogus-provider\nsandbox:\n  type: local\n")

	args := []string{"run", "alpha", "--plain", "--yes", "--skip-doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	// "cost" is kept unscrubbed on purpose: the ceilings in the preview line come
	// from config defaults (this config sets none), so the numbers are part of
	// the behaviour being locked.
	goldenAssert(t, "run_skip_doctor", runScrubDir(transcript(args, stdout, stderr, err), f.Dir, "cost"))
}

// --validate with no validate: block configured is rejected — but only AFTER a
// successful run, so this golden runs on the all-PASSED fixture.
func TestCharacterizeRunValidateNotConfigured(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksAllPassedMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes", "--validate"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ plan ready (1 tasks)", "+ done")
	goldenAssert(t, "run_validate_not_configured", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir, "cost"))
}

// --quiet reaches the PlainRenderer's quiet mode: "plan ready" is suppressed
// while the final "done" survives. Compare with run_all_tasks_done, which is the
// same invocation without --quiet.
func TestCharacterizeRunQuiet(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksAllPassedMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	args := []string{"run", "alpha", "--plain", "--yes", "--quiet"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	runAssertRendererLines(t, stdout, "+ done")
	goldenAssert(t, "run_quiet", runScrubDir(runRendererTranscript(args, stderr, err), f.Dir, "cost"))
}

// `run --help`: the flag surface is part of the contract the rebrand must not
// break silently.
func TestCharacterizeRunHelp(t *testing.T) {
	args := []string{"run", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "run_help", scrub(transcript(args, stdout, stderr, err)))
}

// ── review.go: escalations awaiting a human ──────────────────────────────────

// No .corvex/escalations/ directory at all.
func TestCharacterizeRunReviewNoDir(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"review"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_review_no_dir", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// Two escalations plus a non-markdown file that must be ignored; the preview
// shows the first four non-blank-ish lines of each file.
func TestCharacterizeRunReviewList(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(filepath.Join(".corvex", "escalations", "beta-S07.md"),
		"# Escalation beta/S07\n\nCategory: build\n\nAttempts: 3\n\nDetail line\n")
	f.Write(filepath.Join(".corvex", "escalations", "alpha-S02.md"),
		"# Escalation alpha/S02\nCategory: test\nAttempts: 3\nLast error: boom\nignored fifth line\n")
	f.Write(filepath.Join(".corvex", "escalations", "notes.txt"), "not an escalation\n")

	args := []string{"review"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_review_list", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// Filtered by project prefix.
func TestCharacterizeRunReviewFiltered(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(filepath.Join(".corvex", "escalations", "alpha-S02.md"),
		"# Escalation alpha/S02\nCategory: test\n")
	f.Write(filepath.Join(".corvex", "escalations", "beta-S07.md"),
		"# Escalation beta/S07\nCategory: build\n")

	args := []string{"review", "beta"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_review_filtered", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// Filter that matches nothing takes a different message than the empty case.
func TestCharacterizeRunReviewFilteredEmpty(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(filepath.Join(".corvex", "escalations", "alpha-S02.md"), "# Escalation alpha/S02\n")

	args := []string{"review", "gamma"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_review_filtered_empty", runScrubDir(transcript(args, stdout, stderr, err), f.Dir))
}

// review outside a project.
func TestCharacterizeRunReviewNoCorvexDir(t *testing.T) {
	args := []string{"review"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "run_review_no_corvex_dir", scrub(transcript(args, stdout, stderr, err)))
}

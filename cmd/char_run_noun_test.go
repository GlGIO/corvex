package cmd

// The `run` noun (F4, wave 1): list, show, watch, retry, kill — and the F7
// pause/resume pair that closes D13.
//
// Two kinds of test live here, on purpose:
//
//   - goldens, for the screens. They are the only oracle a renamed surface has,
//     and F3 promised the LEGACY screens keep their bytes — so these lock the
//     NEW ones instead of rewriting the old.
//   - behaviour asserts, for the things a golden cannot see: that the
//     deprecation notice never reaches a pipe (which is what keeps the F-1
//     network valid), that a verb shadows a project of the same name, and that
//     retry actually resets a step before running it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/run"
)

// privateIndex gives one test its own global index.
//
// The package pins a single scratch CORVEX_HOME (main_test.go), and that is
// correct for keeping the developer's home clean — but `run list` is
// cross-repository BY DESIGN, so with one shared index a listing golden would
// contain whatever other tests happened to run first. Not a flake to paper over:
// it is the feature working. Tests that assert on a listing therefore isolate
// the index; the one test that asserts the cross-repository property does so on
// purpose, with two fixtures it created itself.
func privateIndex(t *testing.T) {
	t.Helper()
	t.Setenv(run.HomeEnv, t.TempDir())
}

func TestCharacterizeRunListEmpty(t *testing.T) {
	privateIndex(t)
	f := newFixture(t)
	args := []string{"run", "list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_list_empty", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRunListEmptyJSON(t *testing.T) {
	privateIndex(t)
	f := newFixture(t)
	args := []string{"run", "list", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_list_empty_json", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRunListProjects(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).
		AddProject("alpha", fixtureSpecMD, fixtureTasksMD).
		AddProject("beta", fixtureSpecMD, "")
	f.AddLedger("alpha", activity.Entry{Type: "task_complete", TaskID: "S01", CostUSD: 0.25, DurationMs: 1500})

	args := []string{"run", "list", "--projects"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_list_projects", scrub(transcript(args, stdout, stderr, err)))
}

// A run that really happened: the id, the repo and the age are all
// machine-dependent, and the scrubbers absorb exactly those three. What the
// golden then locks is the shape — which is the part a rename can break.
func TestCharacterizeRunListAfterARealRun(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}

	args := []string{"run", "list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_list_after_run", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRunShowProject(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.AddLedger("alpha",
		activity.Entry{Type: "task_complete", TaskID: "S01", CostUSD: 0.25, DurationMs: 1500, TokensIn: 100, TokensOut: 50},
		activity.Entry{Type: "retry", TaskID: "S02"},
	)

	args := []string{"run", "show", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_show_project", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRunShowStep(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.AddLedger("alpha", activity.Entry{Type: "task_complete", TaskID: "S01", CostUSD: 0.25, DurationMs: 1500, Message: "done"})

	args := []string{"run", "show", "alpha", "--step", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_show_step", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRunShowJSON(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.AddLedger("alpha", activity.Entry{Type: "task_complete", TaskID: "S01", CostUSD: 0.25, DurationMs: 1500})

	args := []string{"run", "show", "alpha", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_show_project_json", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRunShowUnknownID(t *testing.T) {
	privateIndex(t)
	f := newFixture(t)
	args := []string{"run", "show", "run_beef"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_show_unknown_id", scrub(transcript(args, stdout, stderr, err)))
}

// `run show <id>` is the scope that filters the ledger to one execution, and it
// resolves through the global index rather than the current directory.
func TestCharacterizeRunShowByIDAfterARealRun(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}
	id := onlyRunID(t, f.Dir)

	args := []string{"run", "show", id}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_show_by_id", scrub(transcript(args, stdout, stderr, err)))
}

// The confusing case, locked on purpose: a project that HAS run gets an
// identity header, and the screen says out loud that the scope is still the
// project (every run of it), not the run named in the header.
func TestCharacterizeRunShowProjectAfterARealRun(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}

	args := []string{"run", "show", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_show_project_after_run", scrub(transcript(args, stdout, stderr, err)))
}

// The cross-repository default, asserted where it belongs: two repositories,
// one listing. This is the property that makes `run list` the history screen of
// the canvas (2e) rather than a per-directory listing.
func TestCharacterizeRunListSeesEveryRepository(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)

	ids := make([]string, 0, 2)
	for _, name := range []string{"alpha", "beta"} {
		f := newFixture(t).AddProject(name, fixtureSpecMD, runTasksOnePendingMD).GitInit()
		runSeedAnchor(f, name, fixtureSpecMD)
		if _, _, err := runCLIIn(t, f.Dir, "run", name, "--plain", "--yes"); err != nil {
			t.Fatalf("seeding run in %s: %v", name, err)
		}
		ids = append(ids, onlyRunID(t, f.Dir))
	}

	// Listed from a third directory that knows neither repository.
	stdout, _, err := runCLIIn(t, t.TempDir(), "run", "list")
	if err != nil {
		t.Fatalf("run list: %v", err)
	}
	for _, id := range ids {
		if !strings.Contains(stdout, id) {
			t.Errorf("run list did not show %s from another repository:\n%s", id, stdout)
		}
	}
}

// watch on a run that already ended draws once and returns. Without Settled it
// would spin forever, which under `go test` means a hung suite.
func TestCharacterizeRunWatchStopsOnAFinishedRun(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}

	stdout, _, err := runCLIIn(t, f.Dir, "run", "watch", onlyRunID(t, f.Dir))
	if err != nil {
		t.Fatalf("run watch: %v", err)
	}
	if !strings.Contains(stdout, "steps") {
		t.Errorf("watch drew nothing:\n%s", stdout)
	}
}

func TestCharacterizeRunRetryRequiresAStep(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	args := []string{"run", "retry", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_retry_needs_step", scrub(transcript(args, stdout, stderr, err)))
}

// --no-run is the half of retry that a golden can hold still: it marks the step
// PENDING and stops, so nothing is spent and nothing is stubbed.
func TestCharacterizeRunRetryNoRunResetsTheStep(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()

	args := []string{"run", "retry", "alpha", "--step", "S01", "--no-run"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_retry_no_run", scrub(transcript(args, stdout, stderr, err)))

	tasks := f.Read(filepath.Join(".corvex", "tasks", "alpha", "tasks.md"))
	if !strings.Contains(tasks, "## S01 — First Task ⬜ PENDING") {
		t.Errorf("S01 was not reset in tasks.md:\n%s", tasks)
	}
}

func TestCharacterizeRunKillUnknownID(t *testing.T) {
	privateIndex(t)
	f := newFixture(t)
	args := []string{"run", "kill", "run_beef", "--yes"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_kill_unknown", scrub(transcript(args, stdout, stderr, err)))
}

// Killing something that already ended is refused, not attempted. The pid on a
// finished record belongs to whoever inherited the number.
func TestCharacterizeRunKillRefusesAFinishedRun(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}

	_, _, err := runCLIIn(t, f.Dir, "run", "kill", onlyRunID(t, f.Dir), "--yes")
	if err == nil {
		t.Fatal("kill of a finished run returned nil; it must refuse")
	}
	if !strings.Contains(err.Error(), "finished") {
		t.Errorf("error does not say why: %v", err)
	}
}

// D1: the verb wins over a project of the same name, and `run start <name>` is
// the escape hatch. Both halves are asserted, because only having the first is
// a trap.
func TestCharacterizeRunVerbShadowsAProjectOfTheSameName(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).AddProject("list", fixtureSpecMD, fixtureTasksMD).GitInit()

	stdout, _, err := runCLIIn(t, f.Dir, "run", "list")
	if err != nil {
		t.Fatalf("`run list` must list runs, not run the project named list: %v", err)
	}
	if !strings.Contains(stdout, "No runs") {
		t.Errorf("`run list` did not list runs:\n%s", stdout)
	}

	// The escape hatch reaches the project — it gets as far as the cost preview
	// and stops there without --yes on a pipe... which auto-proceeds, so assert
	// on the fact that it did NOT print the listing.
	stdout, _, _ = runCLIIn(t, f.Dir, "run", "start", "list", "--dry-run")
	if strings.Contains(stdout, "No runs") {
		t.Errorf("`run start list` listed runs instead of reaching the project:\n%s", stdout)
	}
}

// The load-bearing half of the deprecation decision (D7): under a pipe the
// legacy commands must print exactly what they printed before, or the F-1
// network stops being an oracle. This is the control that would catch a notice
// leaking into stdout/stderr.
func TestCharacterizeDeprecatedCommandsSayNothingUnderAPipe(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	for _, args := range [][]string{
		{"status", "alpha"},
		{"list"},
		{"logs", "alpha"},
		{"inspect", "alpha"},
		{"review"},
	} {
		stdout, stderr, _ := runCLIIn(t, f.Dir, args...)
		if strings.Contains(stdout+stderr, "deprecated") {
			t.Errorf("`corvex %s` printed a deprecation notice under a pipe:\n%s%s",
				strings.Join(args, " "), stdout, stderr)
		}
	}
}

// onlyRunID reads the id of the single run recorded in a repository.
func onlyRunID(t *testing.T, repo string) string {
	t.Helper()
	views, err := run.Resolver{}.ListRepo(repo)
	if err != nil {
		t.Fatalf("ListRepo: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("repo holds %d run record(s), want 1", len(views))
	}
	return views[0].Record.RunID
}

// retry without --no-run is the half that spends money: it resets the step AND
// executes it. Stubbed, so what is asserted is the handoff — reset, then the
// run path with the step pinned — not the model.
func TestCharacterizeRunRetryResetsAndRunsTheStep(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}
	tasksRel := filepath.Join(".corvex", "tasks", "alpha", "tasks.md")
	if !strings.Contains(f.Read(tasksRel), "PASSED") {
		t.Fatalf("fixture did not finish the step:\n%s", f.Read(tasksRel))
	}

	stdout, _, err := runCLIIn(t, f.Dir, "run", "retry", "alpha", "--step", "S01", "--plain", "--yes")
	if err != nil {
		t.Fatalf("run retry: %v", err)
	}
	if !strings.Contains(stdout, "S01 reset to PENDING") {
		t.Errorf("retry did not report the reset:\n%s", stdout)
	}
	if !strings.Contains(f.Read(tasksRel), "PASSED") {
		t.Errorf("retry reset the step but never ran it:\n%s", f.Read(tasksRel))
	}
	// Two runs now exist for one project — which is the thing run identity was
	// built for, and the reason `run show <id>` had to exist.
	views, lerr := run.Resolver{}.ListRepo(f.Dir)
	if lerr != nil {
		t.Fatalf("ListRepo: %v", lerr)
	}
	if len(views) != 2 {
		t.Errorf("repo holds %d run record(s) after a retry, want 2", len(views))
	}
}

// A run id in another repository is refused with the directory to cd into,
// instead of quietly running the wrong tree.
func TestCharacterizeRunRetryRefusesAForeignRun(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	other := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(other, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, other.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}
	foreign := onlyRunID(t, other.Dir)

	here := newFixture(t).AddProject("beta", fixtureSpecMD, fixtureTasksMD).GitInit()
	_, _, err := runCLIIn(t, here.Dir, "run", "retry", foreign, "--step", "S01", "--no-run")
	if err == nil {
		t.Fatal("retrying a run of another repository must be refused")
	}
	if !strings.Contains(err.Error(), "cd ") {
		t.Errorf("the refusal does not say where to go: %v", err)
	}
}

// --since 0 keeps everything, and --repo . narrows to this repository. Both are
// filters a listing gets wrong silently, so both are asserted against a run in
// a second repository.
func TestCharacterizeRunListFilters(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	mine := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(mine, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, mine.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}
	theirs := newFixture(t).AddProject("beta", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(theirs, "beta", fixtureSpecMD)
	if _, _, err := runCLIIn(t, theirs.Dir, "run", "beta", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}

	all, _, err := runCLIIn(t, mine.Dir, "run", "list", "--since", "0")
	if err != nil {
		t.Fatalf("run list --since 0: %v", err)
	}
	if !strings.Contains(all, "alpha") || !strings.Contains(all, "beta") {
		t.Errorf("--since 0 lost a repository:\n%s", all)
	}

	local, _, err := runCLIIn(t, mine.Dir, "run", "list", "--repo", ".")
	if err != nil {
		t.Fatalf("run list --repo .: %v", err)
	}
	if strings.Contains(local, "beta") {
		t.Errorf("--repo . listed another repository:\n%s", local)
	}
	if !strings.Contains(local, "alpha") {
		t.Errorf("--repo . dropped this repository:\n%s", local)
	}
}

// F5's headline number on this screen: the human clock, kept apart from the
// run's clock. Nothing observed the line that prints it, so it could be deleted
// with the suite green — the gap an audit found. The fixture is a gate that was
// decided after two hours of somebody not looking at it.
func TestCharacterizeRunShowSeparatesTheHumanClock(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.AddLedger("alpha",
		activity.Entry{Type: "task_complete", TaskID: "S01", Phase: "worker", CostUSD: 0.25, DurationMs: 1500},
		activity.Entry{Type: "gate_decided", TaskID: "S02", Phase: "gate", DurationMs: 2 * 60 * 60 * 1000, Message: "human: ship it"},
	)

	args := []string{"run", "show", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_show_human_clock", scrub(transcript(args, stdout, stderr, err)))
	if !strings.Contains(stdout, "waiting on a person") {
		t.Errorf("the human clock is not on the screen:\n%s", stdout)
	}
}

// `run pause` on an id nobody knows: the same refusal `run kill` gives, because
// both verbs address a run through the same index. A golden holds the wording.
func TestCharacterizeRunPauseUnknownID(t *testing.T) {
	privateIndex(t)
	f := newFixture(t)
	args := []string{"run", "pause", "run_beef"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_pause_unknown", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRunResumeUnknownID(t *testing.T) {
	privateIndex(t)
	f := newFixture(t)
	args := []string{"run", "resume", "run_beef"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "runnoun_resume_unknown", scrub(transcript(args, stdout, stderr, err)))
}

// Pausing a run that already ended is refused, and — the part that matters more
// than the message — it leaves no control file behind. Run ids are recycled, so
// an orphan here is a stop order waiting for whoever draws that id next.
func TestCharacterizeRunPauseRefusesAFinishedRunAndLeavesNoOrphan(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}
	id := onlyRunID(t, f.Dir)

	_, _, err := runCLIIn(t, f.Dir, "run", "pause", id)
	if err == nil {
		t.Fatal("pause of a finished run returned nil; it must refuse")
	}
	if !strings.Contains(err.Error(), "finished") {
		t.Errorf("error does not say why: %v", err)
	}
	if _, paused, _ := run.PauseRequested(f.Dir, id); paused {
		t.Error("a refused pause left a control file for the next run wearing this id")
	}
}

// The round trip a human actually types, over a run that is not this process:
// pause writes the file, `run list --status paused` is not fooled by it (the run
// itself reports its status — a control file is a request, not a state), and
// resume takes it away.
func TestCharacterizeRunPauseAndResumeRoundTrip(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).GitInit()
	id := seedLiveRun(t, f.Dir)

	if _, _, err := runCLIIn(t, f.Dir, "run", "pause", id); err != nil {
		t.Fatalf("run pause: %v", err)
	}
	if _, paused, _ := run.PauseRequested(f.Dir, id); !paused {
		t.Fatal("`run pause` printed success without writing the control file")
	}

	// Negative control: pausing twice is not an error, and does not double
	// anything — the file is the state, and writing it again is writing it again.
	if _, _, err := runCLIIn(t, f.Dir, "run", "pause", id); err != nil {
		t.Fatalf("second run pause: %v", err)
	}

	stdout, _, err := runCLIIn(t, f.Dir, "run", "resume", id)
	if err != nil {
		t.Fatalf("run resume: %v", err)
	}
	if !strings.Contains(stdout, id) {
		t.Errorf("resume did not name the run:\n%s", stdout)
	}
	if _, paused, _ := run.PauseRequested(f.Dir, id); paused {
		t.Error("the control file survived `run resume`")
	}

	// And resuming again says so rather than reporting a success that changed
	// nothing.
	if _, _, err := runCLIIn(t, f.Dir, "run", "resume", id); err == nil {
		t.Error("`run resume` on a run nobody paused returned nil")
	}
}

// seedLiveRun writes the record and index line of a run that looks alive to a
// reader: this process's own pid, beating now. Registry.Start would do the same
// thing, but a run started here would also have to be finished here, and a
// finished run is exactly what `pause` refuses.
func seedLiveRun(t *testing.T, repo string) string {
	t.Helper()
	const id = "run_1a2b"
	now := time.Now().UTC()
	rec := run.Record{
		RunID: id, Repo: repo, Project: "alpha", PID: os.Getpid(),
		Host: mustHostname(t), Status: run.StatusRunning, StartedAt: now, UpdatedAt: now,
	}
	if err := run.WriteRecord(rec); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}
	home, err := run.Home()
	if err != nil {
		t.Fatalf("run.Home: %v", err)
	}
	if err := run.AppendIndex(home, rec, now); err != nil {
		t.Fatalf("AppendIndex: %v", err)
	}
	return id
}

func mustHostname(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	return h
}

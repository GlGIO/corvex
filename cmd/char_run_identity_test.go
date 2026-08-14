package cmd

// Run identity through the real CLI path.
//
// These are not goldens. A golden locks what a command PRINTS, and identity
// changes nothing on screen — what it changes is what the run leaves on disk for
// somebody else to read, so that is what gets asserted: the run_id on every
// ledger line, the record in `<repo>/.corvex/runs/`, and the consolidated global
// index. Named TestCharacterizeRunIdentity* so `-run TestCharacterize` still
// covers them.
//
// The pid, the host and the id itself are machine- and draw-dependent; putting
// the raw record into a golden would mean inventing scrubbers for all three and
// then asserting on the scrubbed placeholders, which proves less than reading
// the fields.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/run"
)

// corvexHome is the scratch home this suite is pinned to (see main_test.go).
func corvexHome(t *testing.T) string {
	t.Helper()
	home, err := run.Home()
	if err != nil {
		t.Fatalf("run.Home: %v", err)
	}
	return home
}

// TestCharacterizeRunIdentityReachesLedgerRecordAndIndex is the wiring proof:
// one `corvex run`, and the same id appears in all three places identity is
// observable.
func TestCharacterizeRunIdentityReachesLedgerRecordAndIndex(t *testing.T) {
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("run: %v", err)
	}

	// 1. The record: one run, this project, no recipe, closed as done.
	views, err := run.Resolver{}.ListRepo(f.Dir)
	if err != nil {
		t.Fatalf("ListRepo: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("repo holds %d run record(s), want 1", len(views))
	}
	rec := views[0].Record
	if !run.ValidID(rec.RunID) {
		t.Fatalf("record run id %q is not well formed", rec.RunID)
	}
	if rec.Project != "alpha" {
		t.Errorf("record project = %q, want alpha", rec.Project)
	}
	if rec.Recipe != "" {
		t.Errorf("record recipe = %q, want empty: `run <project>` has no recipe", rec.Recipe)
	}
	if rec.Status != run.StatusDone {
		t.Errorf("record status = %q, want done — a finished run must not stay running", rec.Status)
	}
	if views[0].Liveness != run.LivenessFinished {
		t.Errorf("liveness = %q, want finished", views[0].Liveness)
	}
	if rec.PID <= 0 || rec.Host == "" || rec.StartedAt.IsZero() {
		t.Errorf("record is missing the facts a second process probes with: %+v", rec)
	}

	// 2. The ledger: every line carries this run, and nothing else.
	entries, err := activity.Read(f.Dir, "alpha")
	if err != nil {
		t.Fatalf("activity.Read: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the run wrote no ledger entries")
	}
	for _, e := range entries {
		if e.RunID != rec.RunID {
			t.Fatalf("ledger entry %s carries run %q, want %q", e.Type, e.RunID, rec.RunID)
		}
		if e.Repo != rec.Repo {
			t.Errorf("ledger entry %s carries repo %q, want %q", e.Type, e.Repo, rec.Repo)
		}
		if e.Recipe != "" {
			t.Errorf("ledger entry %s carries recipe %q, want empty", e.Type, e.Recipe)
		}
	}
	if got := len(activity.FilterByRun(entries, rec.RunID)); got != len(entries) {
		t.Errorf("FilterByRun found %d of %d lines", got, len(entries))
	}

	// 3. The global index: a second reader finds the run without the repo's help.
	idx, found, err := run.Resolver{}.Get(rec.RunID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatalf("run %s is absent from the global index at %s", rec.RunID, corvexHome(t))
	}
	if idx.Record.Status != run.StatusDone || idx.Record.Project != "alpha" {
		t.Errorf("index snapshot = %+v, want project alpha status done", idx.Record)
	}

	// The record directory is gitignored: it carries a pid and an absolute path
	// of this machine and has no business in the user's history.
	if got := strings.TrimSpace(f.Read(filepath.Join(".corvex", "runs", ".gitignore"))); got != "*" {
		t.Errorf(".corvex/runs/.gitignore = %q, want \"*\"", got)
	}
}

// TestCharacterizeRunIdentityTwoRunsAreDistinguishable is the F1 acceptance
// criterion at the CLI: the same project run twice, and the two runs separable
// in the one ledger they share and in the two records they wrote.
func TestCharacterizeRunIdentityTwoRunsAreDistinguishable(t *testing.T) {
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	// First run passes S01. The second finds nothing to do and exits through the
	// success path — a different shape of run, same identity requirement.
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("second run: %v", err)
	}

	views, err := run.Resolver{}.ListRepo(f.Dir)
	if err != nil {
		t.Fatalf("ListRepo: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("repo holds %d run record(s), want 2", len(views))
	}
	// ListRepo sorts newest-started first, so views[0] is the second invocation.
	later, earlier := views[0].Record, views[1].Record
	if later.RunID == earlier.RunID {
		t.Fatalf("both runs recorded id %q", later.RunID)
	}
	for _, rec := range []run.Record{later, earlier} {
		if rec.Status != run.StatusDone {
			t.Errorf("run %s status = %q, want done", rec.RunID, rec.Status)
		}
	}

	// One ledger, two runs, and every line attributable to exactly one of them.
	entries, err := activity.Read(f.Dir, "alpha")
	if err != nil {
		t.Fatalf("activity.Read: %v", err)
	}
	byRun := map[string]int{}
	for _, e := range entries {
		byRun[e.RunID]++
	}
	if len(byRun) != 2 {
		t.Fatalf("ledger holds %d distinct run id(s), want 2: %v", len(byRun), byRun)
	}
	if byRun[""] != 0 {
		t.Errorf("%d ledger line(s) carry no run id", byRun[""])
	}
	a := activity.FilterByRun(entries, earlier.RunID)
	b := activity.FilterByRun(entries, later.RunID)
	if len(a) == 0 || len(b) == 0 || len(a)+len(b) != len(entries) {
		t.Errorf("split = %d + %d of %d lines", len(a), len(b), len(entries))
	}

	// The cumulative project view still spans both runs (the "$0.00 after
	// resume" bug stays fixed), while the per-run view separates them.
	all, err := activity.Summarize(f.Dir, "alpha")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if all.TotalCostUSD <= 0 {
		t.Errorf("project summary total = %v, want the spend of the passing run", all.TotalCostUSD)
	}
	// The earlier run is the one that passed S01; the later one found nothing to
	// do. Per-run summaries have to reflect that difference — that is what makes
	// "two runs in one number" no longer the only view available.
	earlierSummary, err := activity.SummarizeRun(f.Dir, "alpha", earlier.RunID)
	if err != nil {
		t.Fatalf("SummarizeRun(earlier): %v", err)
	}
	if len(earlierSummary.PerTask) != 1 {
		t.Errorf("the first run passed S01, but its summary holds %d task(s): %+v", len(earlierSummary.PerTask), earlierSummary.PerTask)
	}
	laterSummary, err := activity.SummarizeRun(f.Dir, "alpha", later.RunID)
	if err != nil {
		t.Fatalf("SummarizeRun(later): %v", err)
	}
	if len(laterSummary.PerTask) != 0 {
		t.Errorf("the second run completed no task, but its summary holds %d: %+v", len(laterSummary.PerTask), laterSummary.PerTask)
	}

	// And both are in the global index, still separate.
	views, err = run.Resolver{}.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	seen := map[string]bool{}
	for _, v := range views {
		if v.Record.RunID == earlier.RunID || v.Record.RunID == later.RunID {
			seen[v.Record.RunID] = true
		}
	}
	if len(seen) != 2 {
		t.Errorf("global index holds %d of the 2 runs this repo started (%v)", len(seen), seen)
	}
}

// TestCharacterizeRunIdentityFailedRunIsRecordedFailed closes the other end of
// the lifecycle: a run that fails must say so on disk, not stay `running`.
func TestCharacterizeRunIdentityFailedRunIsRecordedFailed(t *testing.T) {
	stubClaude(t, runStubRefuse)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err == nil {
		t.Fatal("expected the refusing stub to fail the run")
	}

	views, err := run.Resolver{}.ListRepo(f.Dir)
	if err != nil {
		t.Fatalf("ListRepo: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("repo holds %d run record(s), want 1", len(views))
	}
	if got := views[0].Record.Status; got != run.StatusFailed {
		t.Errorf("record status = %q, want failed", got)
	}
	if views[0].Liveness != run.LivenessFinished {
		t.Errorf("liveness = %q, want finished", views[0].Liveness)
	}
}

// legacyLedgerLine is a literal pre-F1 ledger line — written as bytes, not
// generated by today's code, so it keeps meaning "what is already on disk" even
// if the schema changes again.
const legacyLedgerLine = `{"ts":"2026-01-02T03:04:05Z","type":"task_complete","task_id":"S01","phase":"worker","attempt":1,"duration_ms":8000,"cost_usd":0.15,"tokens_in":600,"tokens_out":300,"status":"PASSED","message":"legacy"}`

// TestCharacterizeRunIdentityAppendsToALegacyLedger is LEI 5 in the wired path:
// real ledgers on disk have lines with no identity, and a run that now stamps
// one has to append to that file rather than choke on it or rewrite it.
//
// Asserted from the CLI's own reader, not from the file: `inspect` has to keep
// reporting the legacy spend, which is the user-visible half of the promise.
func TestCharacterizeRunIdentityAppendsToALegacyLedger(t *testing.T) {
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	f.Write(filepath.Join(".corvex", "tasks", "alpha", "activity.jsonl"), legacyLedgerLine+"\n")

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("run over a legacy ledger: %v", err)
	}

	entries, err := activity.Read(f.Dir, "alpha")
	if err != nil {
		t.Fatalf("activity.Read: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("ledger holds %d entries, want the legacy line plus this run's", len(entries))
	}
	// The legacy line survives untouched, identity and all (i.e. none).
	if got := entries[0]; got.RunID != "" || got.Message != "legacy" || got.CostUSD != 0.15 {
		t.Errorf("the legacy line was altered: %+v", got)
	}
	// The new lines all carry the new run.
	newID := entries[len(entries)-1].RunID
	if !run.ValidID(newID) {
		t.Fatalf("the appended lines carry no run id (%q)", newID)
	}
	if got := len(activity.FilterByRun(entries, "")); got != 1 {
		t.Errorf("%d line(s) have no identity, want exactly the 1 legacy line", got)
	}

	// And the pre-F1 spend is still counted, which is the thing a resume shows.
	sum, err := activity.Summarize(f.Dir, "alpha")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if sum.TotalCostUSD <= 0 {
		t.Errorf("project total = %v, want the legacy spend to still count", sum.TotalCostUSD)
	}
}

// TestCharacterizeRunIdentityInspectJSONCarriesTheRunID is the one place run
// identity becomes *observable CLI output*: `inspect --task --json` serialises
// ledger entries verbatim, so from F1 on it emits run_id and repo.
//
// This is the golden the LEI 4b normaliser exists for. The id is drawn from
// crypto/rand at run start, so it can never be a literal in a golden — it comes
// out as <RUN_ID>, which locks the SHAPE of the schema change (the keys are
// there, in this position) without locking a value that changes every run. The
// pre-F1 golden for the same command (doctorinspect_inspect_task_json) is
// untouched and still passing, because its fixture ledger is written with the
// zero Identity: the two goldens together say "an identified run gains the
// fields, an unidentified ledger keeps its exact old bytes".
func TestCharacterizeRunIdentityInspectJSONCarriesTheRunID(t *testing.T) {
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("run: %v", err)
	}

	args := []string{"inspect", "alpha", "--task", "S01", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "run_identity_inspect_task_json",
		runScrubDir(transcript(args, stdout, stderr, err), f.Dir))

	// Belt to the golden's braces: prove the placeholder replaced a real id
	// rather than an empty string, which a golden alone could not tell apart.
	if !strings.Contains(stdout, "\"run_id\": \"run_") {
		t.Errorf("inspect --json emitted no concrete run id:\n%s", stdout)
	}
}

// TestCharacterizeRunIdentityDryRunRegistersNothing: `--dry-run` executes
// nothing, so it must mint nothing. A dry run in the index would be a row for
// work that never happened.
func TestCharacterizeRunIdentityDryRunRegistersNothing(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--dry-run"); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	views, err := run.Resolver{}.ListRepo(f.Dir)
	if err != nil {
		t.Fatalf("ListRepo: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("--dry-run registered %d run(s): %+v", len(views), views)
	}
}

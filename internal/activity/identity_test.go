package activity_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
)

func ledgerPath(workDir, project string) string {
	return filepath.Join(workDir, ".corvex", "tasks", project, "activity.jsonl")
}

// readRawLines returns the ledger file as raw JSON lines. The identity design is
// a claim about the *bytes on disk* ("grep one line and understand it"), so the
// tests that defend it must look at the line, not at the decoded struct.
func readRawLines(t *testing.T, workDir, project string) []string {
	t.Helper()
	data, err := os.ReadFile(ledgerPath(workDir, project))
	if err != nil {
		t.Fatalf("reading raw ledger: %v", err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// Every line carries the run identity — not a header line at the top of the
// file. A reader must be able to attribute a single line without state.
func TestAppend_StampsIdentityOnEveryLine(t *testing.T) {
	workDir, project := setupProjectDir(t)
	id := activity.Identity{RunID: "run_8f21", Repo: "/repos/corvex", Recipe: "ship-feature"}
	l, err := activity.New(workDir, project, id)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The caller never mentions the run: that is the point of stamping at
	// construction. If identity had to be filled per event, some code path
	// would forget.
	for _, e := range []activity.Entry{
		{Type: "run_start", Timestamp: time.Unix(1, 0).UTC()},
		{Type: "task_start", TaskID: "S01", Timestamp: time.Unix(2, 0).UTC()},
		{Type: "task_complete", TaskID: "S01", Status: "PASSED", Timestamp: time.Unix(3, 0).UTC()},
	} {
		if err := l.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	lines := readRawLines(t, workDir, project)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), lines)
	}
	for i, line := range lines {
		for _, want := range []string{`"run_id":"run_8f21"`, `"repo":"/repos/corvex"`, `"recipe":"ship-feature"`} {
			if !strings.Contains(line, want) {
				t.Errorf("line %d missing %s: %s", i, want, line)
			}
		}
	}

	got, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for i, e := range got {
		if e.RunID != id.RunID || e.Repo != id.Repo || e.Recipe != id.Recipe {
			t.Errorf("entry %d identity = %q/%q/%q, want %q/%q/%q",
				i, e.RunID, e.Repo, e.Recipe, id.RunID, id.Repo, id.Recipe)
		}
	}
}

// A Ledger with the zero Identity must write exactly the bytes it wrote before
// F1: omitempty keeps the three keys out of the line. This is what lets the
// golden network in cmd/ stay untouched while identity is being wired.
func TestAppend_ZeroIdentityOmitsFields(t *testing.T) {
	workDir, project := setupProjectDir(t)
	l, err := activity.New(workDir, project, activity.Identity{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Append(activity.Entry{Type: "task_start", TaskID: "S01", Timestamp: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	line := readRawLines(t, workDir, project)[0]
	if want := `{"ts":"1970-01-01T00:00:01Z","type":"task_start","task_id":"S01"}`; line != want {
		t.Errorf("line = %s\nwant   %s", line, want)
	}
}

// The ledger's identity is authoritative: a Ledger built for run A cannot be
// talked into writing a line labelled run B. Fields the ledger does not know are
// left as the entry had them, so a replay/import tool can preserve provenance of
// lines it did not produce.
func TestAppend_LedgerIdentityWinsOverEntry(t *testing.T) {
	workDir, project := setupProjectDir(t)
	l, err := activity.New(workDir, project, activity.Identity{RunID: "run_A"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Append(activity.Entry{
		Type:   "task_start",
		RunID:  "run_B_forged",
		Recipe: "imported-recipe", // ledger has no recipe: entry value survives
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got[0].RunID != "run_A" {
		t.Errorf("run_id = %q, want run_A (ledger identity must win)", got[0].RunID)
	}
	if got[0].Recipe != "imported-recipe" {
		t.Errorf("recipe = %q, want imported-recipe (ledger knows none, entry keeps its own)", got[0].Recipe)
	}
}

// legacyLine is a real pre-F1 ledger line, written out literally rather than
// produced by the current code — the only way to prove that ledgers already on
// disk keep reading. Byte-for-byte the shape emitted before run identity existed.
const legacyLine = `{"ts":"2026-01-02T03:04:05Z","type":"task_complete","task_id":"S01",` +
	`"phase":"worker","attempt":1,"duration_ms":8000,"cost_usd":0.15,` +
	`"tokens_in":600,"tokens_out":300,"status":"PASSED","message":"done"}`

func TestRead_LegacyLineWithoutIdentityStillParses(t *testing.T) {
	workDir, project := setupProjectDir(t)
	if err := os.WriteFile(ledgerPath(workDir, project), []byte(legacyLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("Read of legacy ledger: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("legacy line dropped: got %d entries, want 1", len(got))
	}
	e := got[0]
	if e.Type != "task_complete" || e.TaskID != "S01" || e.Status != "PASSED" ||
		e.DurationMs != 8000 || e.CostUSD != 0.15 || e.TokensIn != 600 ||
		e.TokensOut != 300 || e.Attempt != 1 || e.Phase != "worker" || e.Message != "done" {
		t.Errorf("legacy fields lost: %+v", e)
	}
	if e.RunID != "" || e.Repo != "" || e.Recipe != "" {
		t.Errorf("identity should be empty for a legacy line, got %q/%q/%q", e.RunID, e.Repo, e.Recipe)
	}
	if e.Timestamp.UTC().Format(time.RFC3339) != "2026-01-02T03:04:05Z" {
		t.Errorf("timestamp = %v", e.Timestamp)
	}

	// And the aggregate view still sees it: a legacy ledger must not silently
	// stop counting because identity was introduced.
	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.TotalCostUSD != 0.15 || len(s.PerTask) != 1 {
		t.Errorf("legacy summary = %+v, want one task and 0.15", s)
	}
	// Legacy lines are addressable as the run-less population.
	if got := activity.FilterByRun(got, ""); len(got) != 1 {
		t.Errorf("FilterByRun(\"\") = %d entries, want 1 (pre-identity lines)", len(got))
	}
}

// A legacy line and a new line in the same file: appending after the upgrade
// must not corrupt or drop what was already there.
func TestRead_MixedLegacyAndIdentifiedLines(t *testing.T) {
	workDir, project := setupProjectDir(t)
	if err := os.WriteFile(ledgerPath(workDir, project), []byte(legacyLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := activity.New(workDir, project, activity.Identity{RunID: "run_new", Repo: "/repos/corvex"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Append(activity.Entry{Type: "task_complete", TaskID: "S02", Status: "PASSED", CostUSD: 0.25}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 || got[0].RunID != "" || got[1].RunID != "run_new" {
		t.Fatalf("mixed ledger = %+v", got)
	}
}

// The F1 acceptance criterion at the ledger level: two runs writing the same
// project file stay separable. No CLI needed to show it.
func TestFilterByRun_TwoRunsInOneFileAreSeparable(t *testing.T) {
	workDir, project := setupProjectDir(t)
	runA, err := activity.New(workDir, project, activity.Identity{RunID: "run_aaaa", Repo: "/repos/corvex", Recipe: "r1"})
	if err != nil {
		t.Fatalf("New A: %v", err)
	}
	runB, err := activity.New(workDir, project, activity.Identity{RunID: "run_bbbb", Repo: "/repos/corvex", Recipe: "r2"})
	if err != nil {
		t.Fatalf("New B: %v", err)
	}

	// Interleaved on purpose: two concurrent runs append to the same file, so
	// line order cannot be what attributes a line to a run. This is the case a
	// per-run header line could not represent at all.
	appends := []struct {
		l *activity.Ledger
		e activity.Entry
	}{
		{runA, activity.Entry{Type: "task_start", TaskID: "S01"}},
		{runB, activity.Entry{Type: "task_start", TaskID: "S01"}},
		{runA, activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED"}},
		{runB, activity.Entry{Type: "retry", TaskID: "S01"}},
		{runB, activity.Entry{Type: "task_complete", TaskID: "S01", Status: "FAILED"}},
	}
	for _, a := range appends {
		if err := a.l.Append(a.e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	all, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(all))
	}

	a := activity.FilterByRun(all, "run_aaaa")
	b := activity.FilterByRun(all, "run_bbbb")
	if len(a) != 2 || len(b) != 3 {
		t.Fatalf("split = %d/%d, want 2/3", len(a), len(b))
	}
	if a[1].Status != "PASSED" || b[2].Status != "FAILED" {
		t.Errorf("wrong lines attributed: A=%+v B=%+v", a, b)
	}
	if a[0].Recipe != "r1" || b[0].Recipe != "r2" {
		t.Errorf("recipe not per-run: A=%q B=%q", a[0].Recipe, b[0].Recipe)
	}
	if got := activity.FilterByRun(all, "run_missing"); len(got) != 0 || got == nil {
		t.Errorf("unknown run should give an empty non-nil slice, got %v", got)
	}
}

// Summarize is deliberately cumulative across runs (it is what "resume shows the
// $16.54 already spent" needs), and cannot double count: metrics are keyed by
// task, latest PASSED wins. SummarizeRun is the per-run view.
func TestSummarize_CumulativeAcrossRuns_SummarizeRun_PerRun(t *testing.T) {
	workDir, project := setupProjectDir(t)
	runA, err := activity.New(workDir, project, activity.Identity{RunID: "run_aaaa"})
	if err != nil {
		t.Fatalf("New A: %v", err)
	}
	runB, err := activity.New(workDir, project, activity.Identity{RunID: "run_bbbb"})
	if err != nil {
		t.Fatalf("New B: %v", err)
	}

	// Run A passes S01 for $0.20. Run B re-runs S01 (now $0.10) and passes S02.
	if err := runA.Append(activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED", DurationMs: 2000, CostUSD: 0.20, TokensIn: 100, TokensOut: 40, Timestamp: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := runB.Append(activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED", DurationMs: 500, CostUSD: 0.10, TokensIn: 30, TokensOut: 10, Timestamp: time.Unix(2, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := runB.Append(activity.Entry{Type: "task_complete", TaskID: "S02", Status: "PASSED", DurationMs: 3000, CostUSD: 0.50, TokensIn: 200, TokensOut: 60, Timestamp: time.Unix(3, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	// Project view: S01 counted once, with run B's numbers (latest PASSED wins),
	// plus S02. NOT 0.20+0.10+0.50 — a task re-run replaces its own metrics.
	all, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(all.PerTask) != 2 {
		t.Fatalf("PerTask = %+v, want S01 and S02", all.PerTask)
	}
	if want := 0.10 + 0.50; all.TotalCostUSD != want {
		t.Errorf("project TotalCostUSD = %v, want %v (cumulative across runs, one entry per task)", all.TotalCostUSD, want)
	}
	if all.PerTask["S01"].DurationMs != 500 {
		t.Errorf("S01 duration = %d, want 500 (run B's re-run)", all.PerTask["S01"].DurationMs)
	}

	// Per-run view: each run answers only for what it did.
	sa, err := activity.SummarizeRun(workDir, project, "run_aaaa")
	if err != nil {
		t.Fatalf("SummarizeRun A: %v", err)
	}
	if sa.TotalCostUSD != 0.20 || len(sa.PerTask) != 1 || sa.PerTask["S01"].DurationMs != 2000 {
		t.Errorf("run A summary = %+v, want only S01 at $0.20/2000ms", sa)
	}
	sb, err := activity.SummarizeRun(workDir, project, "run_bbbb")
	if err != nil {
		t.Fatalf("SummarizeRun B: %v", err)
	}
	if want := 0.60; sb.TotalCostUSD != want || len(sb.PerTask) != 2 {
		t.Errorf("run B summary = %+v, want S01+S02 at $%v", sb, want)
	}
	if _, err := activity.SummarizeRun(workDir, "missing-project", "run_aaaa"); err != nil {
		t.Errorf("SummarizeRun on missing ledger should be empty, not an error: %v", err)
	}
}

// Identity must never carry anything secret: the ledger is committed-adjacent and
// the global index lives in $HOME. This test is a tripwire on the struct's shape
// — if a field like Env or Token is added, it fails and the reviewer has to
// justify it.
func TestIdentity_CarriesNoSecrets(t *testing.T) {
	buf, err := json.Marshal(activity.Entry{
		Type:   "task_start",
		RunID:  "run_8f21",
		Repo:   "/repos/corvex",
		Recipe: "ship-feature",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"env", "token", "secret", "credential", "allowlist", "api_key"} {
		if strings.Contains(strings.ToLower(string(buf)), forbidden) {
			t.Errorf("ledger line mentions %q: %s", forbidden, buf)
		}
	}
}

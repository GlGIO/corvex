package activity_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
)

// sameCost compares an AGGREGATED dollar amount. Never `!=`.
//
// Summarize totals cost by iterating PerTask, which is a map, so the addition
// happens in a different order on every execution — Go randomises map iteration
// deliberately. Float addition is not associative, so three or more terms land
// on 0.45 or 0.44999999999999996 depending on the draw, and an exact comparison
// is a coin flip: TestRead_AllFourOnDiskLineShapesStayReadable failed 3 times
// in 10 before this helper existed. Two-term sums happen to be safe (IEEE
// addition is commutative), which is exactly why the flake hid — it only bites
// once a fixture grows a third task.
//
// The tolerance is a hundred-millionth of a cent, far below the two decimals the
// product ever renders, so nothing a user could notice slips through it. The
// production non-determinism is NOT fixed here: it predates this phase, no
// golden observes it (cost is formatted), and reducing in stable task_id order is
// a behaviour change. It is written down as BACKLOG in
// .corvex/tasks/rebrand/f-1-anomalias.md.
func sameCost(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

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
	id := activity.Identity{RunID: "run_8f21", Recipe: "ship-feature"}
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
		for _, want := range []string{`"run_id":"run_8f21"`, `"recipe":"ship-feature"`} {
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
		if e.RunID != id.RunID || e.Recipe != id.Recipe {
			t.Errorf("entry %d identity = %q/%q, want %q/%q",
				i, e.RunID, e.Recipe, id.RunID, id.Recipe)
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

// preF1Line (see the const block below) is a real pre-F1 ledger line, written out
// literally rather than produced by the current code — the only way to prove that
// ledgers already on disk keep reading.
func TestRead_LegacyLineWithoutIdentityStillParses(t *testing.T) {
	workDir, project := setupProjectDir(t)
	if err := os.WriteFile(ledgerPath(workDir, project), []byte(preF1Line+"\n"), 0o644); err != nil {
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
	if e.RunID != "" || e.Recipe != "" {
		t.Errorf("identity should be empty for a legacy line, got %q/%q", e.RunID, e.Recipe)
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
	if !sameCost(s.TotalCostUSD, 0.15) || len(s.PerTask) != 1 {
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
	if err := os.WriteFile(ledgerPath(workDir, project), []byte(preF1Line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := activity.New(workDir, project, activity.Identity{RunID: "run_new"})
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
	runA, err := activity.New(workDir, project, activity.Identity{RunID: "run_aaaa", Recipe: "r1"})
	if err != nil {
		t.Fatalf("New A: %v", err)
	}
	runB, err := activity.New(workDir, project, activity.Identity{RunID: "run_bbbb", Recipe: "r2"})
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
	if want := 0.10 + 0.50; !sameCost(all.TotalCostUSD, want) {
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
	if !sameCost(sa.TotalCostUSD, 0.20) || len(sa.PerTask) != 1 || sa.PerTask["S01"].DurationMs != 2000 {
		t.Errorf("run A summary = %+v, want only S01 at $0.20/2000ms", sa)
	}
	sb, err := activity.SummarizeRun(workDir, project, "run_bbbb")
	if err != nil {
		t.Fatalf("SummarizeRun B: %v", err)
	}
	if want := 0.60; !sameCost(sb.TotalCostUSD, want) || len(sb.PerTask) != 2 {
		t.Errorf("run B summary = %+v, want S01+S02 at $%v", sb, want)
	}
	if _, err := activity.SummarizeRun(workDir, "missing-project", "run_aaaa"); err != nil {
		t.Errorf("SummarizeRun on missing ledger should be empty, not an error: %v", err)
	}
}

// Identity must never carry anything secret: the ledger is committed and the
// global index lives in $HOME. This test is a tripwire on the struct's shape
// — if a field like Env or Token is added, it fails and the reviewer has to
// justify it.
func TestIdentity_CarriesNoSecrets(t *testing.T) {
	buf, err := json.Marshal(activity.Entry{
		Type:   "task_start",
		RunID:  "run_8f21",
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

// The ledger is the one corvex artifact that lands in the user's git history
// (corvex's own auto_commit commits it), so its schema is a privacy surface. This
// enumerates the JSON keys Entry can emit and pins them to an allowlist: adding a
// field makes this test fail, and whoever adds it has to write down that the value
// is safe to publish. F1 added `repo` = absolute path with no such moment, which is
// how `/Users/<username>/…` ended up on every line.
//
// The allowlist is deliberately the whole schema and not just the identity fields:
// the leak did not arrive through a field called "path".
func TestEntry_JSONKeysAreAnAllowlist(t *testing.T) {
	allowed := map[string]string{
		"ts":          "a UTC timestamp",
		"type":        "an event name from a closed set in internal/orchestrator",
		"run_id":      "`run_` + 4 hex from crypto/rand — identifies the run, describes nothing about the machine",
		"recipe":      "the user's own recipe name",
		"task_id":     "a task id from the user's tasks.md",
		"phase":       "worker | review | plan | recovery | gate | validate",
		"attempt":     "a retry counter",
		"duration_ms": "a duration",
		"cost_usd":    "money",
		"tokens_in":   "a token count",
		"tokens_out":  "a token count",
		"status":      "PASSED | FAILED | …",
		"message":     "a human-readable note; the emitters must keep paths out of it",
		"tool":        "the NAME of a tool the worker used (Read, Bash, …) — never its input, which carries paths, command lines and whatever the user exported",
	}

	typ := reflect.TypeOf(activity.Entry{})
	seen := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if key == "" {
			key = field.Name
		}
		seen[key] = true
		if _, ok := allowed[key]; !ok {
			t.Errorf("Entry.%s emits the ledger key %q, which is not on the allowlist.\n"+
				"activity.jsonl is COMMITTED: add the key here with a note saying why its value is\n"+
				"safe to publish, or keep the value in the run record / global index instead.",
				field.Name, key)
		}
	}
	for key := range allowed {
		if !seen[key] {
			t.Errorf("the allowlist still lists %q, which Entry no longer emits: prune it", key)
		}
	}
	if seen["repo"] {
		t.Error("the `repo` key is back on the ledger line — see activity.Identity for why it left")
	}
}

// The three literal shapes of a line that exist on disk in the wild. Written out
// as bytes rather than produced by the current code: that is the only way to prove
// a ledger someone already has keeps reading.
const (
	// preF1Line: before run identity existed.
	preF1Line = `{"ts":"2026-01-02T03:04:05Z","type":"task_complete","task_id":"S01",` +
		`"phase":"worker","attempt":1,"duration_ms":8000,"cost_usd":0.15,` +
		`"tokens_in":600,"tokens_out":300,"status":"PASSED","message":"done"}`

	// f1WithRepoLine: written by F1, carrying the absolute path that this change
	// removes. Real files hold these — the repos this project tested in, and the
	// user's own. The key is now unknown to Entry, and must be ignored, not fatal:
	// a run that upgrades corvex mid-project appends to a file full of these.
	f1WithRepoLine = `{"ts":"2026-01-02T03:04:06Z","type":"task_complete","run_id":"run_eed5",` +
		`"repo":"/Users/someone/projects/corvex","task_id":"S02","duration_ms":4000,` +
		`"cost_usd":0.25,"status":"PASSED"}`

	// postFixLine: the run id, no machine. Written from F1's fix until F5.
	postFixLine = `{"ts":"2026-01-02T03:04:07Z","type":"task_complete","run_id":"run_9c3f",` +
		`"task_id":"S03","duration_ms":1000,"cost_usd":0.05,"status":"PASSED"}`

	// f5PhaseToolLine: the fourth shape, written from F5 on. Two columns finally
	// carry a value — `phase`, which existed since before F1 and nothing ever
	// filled, and `tool`, which is new. Both are the worker's own vocabulary and
	// neither describes the machine: `tool` is the NAME only, never the input.
	// A ledger that a run upgraded mid-project holds all four shapes at once.
	f5PhaseToolLine = `{"ts":"2026-01-02T03:04:08Z","type":"tool_result","run_id":"run_9c3f",` +
		`"task_id":"S03","phase":"worker","duration_ms":250,"tool":"Bash"}`
)

func TestRead_AllFourOnDiskLineShapesStayReadable(t *testing.T) {
	workDir, project := setupProjectDir(t)
	body := preF1Line + "\n" + f1WithRepoLine + "\n" + postFixLine + "\n" + f5PhaseToolLine + "\n"
	if err := os.WriteFile(ledgerPath(workDir, project), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("Read dropped a line: got %d entries, want 4 (%+v)", len(got), got)
	}

	// The F1 line keeps everything except the field that was removed: the run id
	// still identifies it, the metrics still count. Dropping `repo` on read is the
	// point — `inspect --json` re-serialises entries verbatim, so a line that used
	// to leak a path stops leaking it the moment it is read by the new code.
	f1 := got[1]
	if f1.RunID != "run_eed5" || f1.TaskID != "S02" || f1.CostUSD != 0.25 || f1.DurationMs != 4000 {
		t.Errorf("F1-with-repo line lost its fields: %+v", f1)
	}
	if buf, err := json.Marshal(f1); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(buf), "/Users/someone") {
		t.Errorf("re-serialising an old line republishes the path it carried: %s", buf)
	}

	// All three populations are addressable, and the totals span them: an upgrade
	// must not make a project's accumulated spend disappear.
	if n := len(activity.FilterByRun(got, "")); n != 1 {
		t.Errorf("pre-F1 lines addressable as the run-less population: got %d, want 1", n)
	}
	if n := len(activity.FilterByRun(got, "run_eed5")); n != 1 {
		t.Errorf("F1 line not addressable by its run id: got %d, want 1", n)
	}
	// The F5 line's two new columns survive the round trip. Nothing older is
	// disturbed by them: the three neighbours simply have no value there.
	f5 := got[3]
	if f5.Phase != "worker" || f5.Tool != "Bash" || f5.DurationMs != 250 {
		t.Errorf("F5 line lost phase/tool: %+v", f5)
	}

	sum, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if want := 0.15 + 0.25 + 0.05; !sameCost(sum.TotalCostUSD, want) || len(sum.PerTask) != 3 {
		t.Errorf("summary over the four shapes = %+v, want 3 tasks and $%v (the fourth shape is a tool line and completes no task)", sum, want)
	}

	// The four coexist in the breakdown too. Two of these lines carry no phase
	// (the F1 and post-fix shapes), and they stay countable in a bucket that says
	// so instead of being folded into the labelled one. The other two do carry
	// one — the pre-F1 fixture happens to, and the F5 line does by design — and
	// they share the worker bucket across a five-month gap in the format.
	un := sum.PerPhase[activity.PhaseUnattributed]
	if un.Entries != 2 || !sameCost(un.CostUSD, 0.25+0.05) {
		t.Errorf("unattributed bucket = %+v, want the two phase-less lines and their money", un)
	}
	if w := sum.PerPhase["worker"]; w.Entries != 2 || w.DurationMs != 8000 || !sameCost(w.CostUSD, 0.15) {
		t.Errorf("worker bucket = %+v, want both labelled lines, with the F5 tool line's 250ms "+
			"belonging to the tool row and not to the phase clock", w)
	}
	if sum.PerTool["Bash"].DurationMs != 250 {
		t.Errorf("PerTool[Bash] = %+v, want the 250ms the on-disk line carries", sum.PerTool["Bash"])
	}
}

// Appending to a ledger that already holds a leaked F1 line must not re-leak: the
// new line is clean even though its neighbour is not. (Rewriting the neighbour is
// deliberately NOT done — an append-only file the user has committed is not ours
// to rewrite, and a `git log -p` would show the path anyway.)
func TestAppend_AfterAnF1LeakedLineWritesNoPath(t *testing.T) {
	workDir, project := setupProjectDir(t)
	if err := os.WriteFile(ledgerPath(workDir, project), []byte(f1WithRepoLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := activity.New(workDir, project, activity.Identity{RunID: "run_new"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Append(activity.Entry{Type: "task_start", TaskID: "S09", Timestamp: time.Unix(9, 0).UTC()}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	lines := readRawLines(t, workDir, project)
	if len(lines) != 2 {
		t.Fatalf("expected 2 raw lines, got %d: %q", len(lines), lines)
	}
	if lines[0] != f1WithRepoLine {
		t.Errorf("the pre-existing line was rewritten:\n%s", lines[0])
	}
	if want := `{"ts":"1970-01-01T00:00:09Z","type":"task_start","run_id":"run_new","task_id":"S09"}`; lines[1] != want {
		t.Errorf("new line = %s\nwant       %s", lines[1], want)
	}
}

// The regression guard for the flake that made the suite red in roughly a third
// of runs: aggregated cost is stable only to a tolerance, so an assertion on it
// must be written as one.
//
// The mechanism is demonstrated rather than argued, and demonstrated
// DETERMINISTICALLY — a guard that only bites on an unlucky map order would be
// the same coin flip it is meant to replace. Four costs are chosen so that adding
// them low-task-id-first and high-task-id-first produce different float64 values.
// Both orders are legitimate: `aggregate` walks PerTask, which is a map, and Go
// re-randomises map order on every `range`. So the total the product reports is
// one of several bit patterns and no exact comparison against it can hold.
//
// Replacing sameCost with `==` here fails every run, not one in three.
func TestSummarize_AggregatedCostIsStableOnlyToATolerance(t *testing.T) {
	workDir, project := setupProjectDir(t)
	l, err := activity.New(workDir, project, activity.Identity{RunID: "run_agg"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 0.07 + 0.11 + 0.13 + 0.17: ascending gives 0.47999999999999998,
	// descending 0.48000000000000004. Three of the four cyclic orders a small
	// map can hand out disagree with the ascending one.
	costs := []float64{0.07, 0.11, 0.13, 0.17}
	ids := make([]string, len(costs))
	for i, c := range costs {
		ids[i] = fmt.Sprintf("S%02d", i)
		if err := l.Append(activity.Entry{
			Type: "task_complete", TaskID: ids[i], Status: "PASSED",
			CostUSD: c, Timestamp: time.Unix(int64(i), 0).UTC(),
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	s, err := activity.Summarize(workDir, project)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.PerTask) != len(costs) {
		t.Fatalf("PerTask = %+v, want %d tasks", s.PerTask, len(costs))
	}

	// Two orders over the very same map the product sums over.
	var up, down float64
	for i := 0; i < len(ids); i++ {
		up += s.PerTask[ids[i]].CostUSD
		down += s.PerTask[ids[len(ids)-1-i]].CostUSD
	}
	if up == down {
		t.Fatalf("the fixture stopped being order-sensitive (%v both ways): pick costs that "+
			"round differently, or this test proves nothing", up)
	}
	if !sameCost(up, down) {
		t.Fatalf("ascending %v and descending %v differ by more than a rounding error: "+
			"that is an arithmetic bug, not float representation", up, down)
	}

	// And the total the product produced has to be one of them, to the tolerance.
	// 200 draws because each Summarize walks the map in a fresh order: any draw
	// outside the tolerance is a real defect, and an exact comparison would have
	// tripped on the first draw that rounded the other way.
	for i := 0; i < 200; i++ {
		got, err := activity.Summarize(workDir, project)
		if err != nil {
			t.Fatalf("Summarize draw %d: %v", i, err)
		}
		if !sameCost(got.TotalCostUSD, up) {
			t.Fatalf("draw %d totalled %v, outside the tolerance around %v", i, got.TotalCostUSD, up)
		}
	}
}

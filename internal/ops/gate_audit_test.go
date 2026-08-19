package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/types"
)

// seedDecidedGate writes one more gate on the fixture's repository, already
// answered, with both stamps chosen by the caller.
//
// The Decision goes in through gate.Open rather than through gate.Decide on
// purpose: the audit reads history it did not create — an expiry from last night,
// a rejection from last week — and Decide can only produce a decision dated by
// the caller's clock at a gate it is willing to accept (it refuses an approval
// whose required reading has no mark). Building the past through Decide would
// mean building a different past. The cases that MUST go through Decide, because
// what is being measured is Decide's own behaviour, do so below.
func (f gateFixture) seedDecidedGate(t *testing.T, runID, stepID string, evidence []types.Evidence,
	verdict gate.Verdict, openedAt, decidedAt time.Time, reads []gate.ReadMark) {
	t.Helper()
	if err := gate.Open(gate.Pending{
		RunID: runID, StepID: stepID, Repo: f.repo, Project: "demo", Recipe: "gate-demo",
		Nature: types.GateHuman, Label: "Aprovação de " + stepID,
		Evidence: evidence, Reads: reads,
		OpenedAt: openedAt,
		Decision: &gate.Decision{Verdict: verdict, DecidedAt: decidedAt},
	}); err != nil {
		t.Fatalf("seeding decided gate %s/%s: %v", runID, stepID, err)
	}
}

// seedOpenGate writes an undecided gate with a chosen opening stamp.
func (f gateFixture) seedOpenGate(t *testing.T, runID, stepID string, evidence []types.Evidence, openedAt time.Time) {
	t.Helper()
	if err := gate.Open(gate.Pending{
		RunID: runID, StepID: stepID, Repo: f.repo, Project: "demo", Recipe: "gate-demo",
		Nature: types.GateHuman, Label: "Aprovação de " + stepID,
		Evidence: evidence, OpenedAt: openedAt,
	}); err != nil {
		t.Fatalf("seeding open gate %s/%s: %v", runID, stepID, err)
	}
}

func groupOf(t *testing.T, audit GateAudit, key string) GateAuditGroup {
	t.Helper()
	for _, g := range audit.Groups {
		if g.Key == key {
			return g
		}
	}
	t.Fatalf("no group %q in %+v", key, audit.Groups)
	return GateAuditGroup{}
}

func rowOf(t *testing.T, audit GateAudit, stepID string) GateAuditRow {
	t.Helper()
	for _, r := range audit.Rows {
		if r.StepID == stepID {
			return r
		}
	}
	t.Fatalf("no row for step %q in %d rows", stepID, len(audit.Rows))
	return GateAuditRow{}
}

// TestLoadGateAudit_ExpiryIsOutsideTheRejectionRate is the finding this whole
// file exists for. Rejected means somebody judged and said no; expired means
// nobody appeared. Summing them would make an abandoned gate read as the most
// discerning one in the pipeline.
func TestLoadGateAudit_ExpiryIsOutsideTheRejectionRate(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	base := f.now.Add(-4 * time.Hour)
	read := []gate.ReadMark{{Label: "Migration", At: base.Add(time.Minute)}}

	f.seedDecidedGate(t, "run_aaa1", "S07", requiredEvidence(), gate.Approved, base, base.Add(2*time.Minute), read)
	f.seedDecidedGate(t, "run_aaa2", "S07", requiredEvidence(), gate.Rejected, base, base.Add(5*time.Minute), read)
	f.seedDecidedGate(t, "run_aaa3", "S07", requiredEvidence(), gate.Expired, base, base.Add(90*time.Minute), nil)

	audit, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	g := groupOf(t, audit, "gate-demo/S07")

	if g.Approved != 1 || g.Rejected != 1 || g.Expired != 1 {
		t.Fatalf("verdict buckets are %d/%d/%d, want 1 approved, 1 rejected, 1 expired", g.Approved, g.Rejected, g.Expired)
	}
	if g.Decided != g.Approved+g.Rejected+g.Expired {
		t.Errorf("decided is %d but the three buckets sum to %d — a verdict fell outside them",
			g.Decided, g.Approved+g.Rejected+g.Expired)
	}
	if g.Judged != 2 {
		t.Errorf("judged is %d, want 2: the expired gate was answered by a timer, not by a person", g.Judged)
	}
	if g.RejectionRate == nil || *g.RejectionRate != 0.5 {
		t.Errorf("rejection rate is %v, want 0.5 — expiry must stay out of the denominator", g.RejectionRate)
	}
	// The 90-minute expiry is a recipe timer. If it entered the latency
	// distribution the median would jump from 5m to 90m and the gate would look
	// deliberate.
	if g.LatencyMsMax == nil || *g.LatencyMsMax != (5*time.Minute).Milliseconds() {
		t.Errorf("max latency is %v, want 5m: the expiry timer is not a human response time", g.LatencyMsMax)
	}
}

// TestLoadGateAudit_OnlyGateFilesCount is the --approve-gates exclusion, proved
// for free.
//
// The auto-approval path returns before openGate (internal/step/human_gate.go:47-67),
// so it writes a gate_decided line to the ledger and NO gate file. Reading the
// disk substrate therefore excludes policy approvals without a single line of
// filtering — and this test pins both halves: the ledger really does claim a
// decision, and the audit really does not count it.
func TestLoadGateAudit_OnlyGateFilesCount(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	if err := os.MkdirAll(filepath.Join(f.repo, ".corvex", "tasks", "demo"), 0o755); err != nil {
		t.Fatalf("creating the project dir: %v", err)
	}
	ledger, err := activity.New(f.repo, "demo", activity.Identity{})
	if err != nil {
		t.Fatalf("opening the ledger: %v", err)
	}
	// Exactly the line the auto-approved gate emits: passed, no duration.
	if err := ledger.Append(activity.Entry{
		Timestamp: f.now, Type: "gate_decided", TaskID: "S99", Phase: "gate",
		Status: "PASSED", Message: "Aprovação de STG (auto-approved by policy, no human waited)",
	}); err != nil {
		t.Fatalf("appending to the ledger: %v", err)
	}

	entries, err := ReadActivityLedger(f.repo, "demo")
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if len(entries) != 1 || entries[0].Type != "gate_decided" || entries[0].DurationMs != 0 {
		t.Fatalf("positive control failed: the ledger should claim one durationless gate_decided, got %+v", entries)
	}

	audit, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	if len(audit.Rows) != 1 {
		t.Fatalf("the audit reports %d rows, want 1 (only the gate that has a file)", len(audit.Rows))
	}
	for _, r := range audit.Rows {
		if r.StepID == "S99" {
			t.Errorf("the policy-approved step %s reached the audit: a declared auto-approval is not a fast human", r.StepID)
		}
	}
}

// TestLoadGateAudit_ReadGapSeparatesStampFromJudgement pins the three states of
// the sharpest number, including the two that only the real gate.Decide path can
// produce.
func TestLoadGateAudit_ReadGapSeparatesStampFromJudgement(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())

	// No required reading: there is nothing to have read, so the gap is not
	// zero — it is inexpressible, and a 0 here would be a fabricated finding.
	f.seedDecidedGate(t, "run_bbb1", "S10",
		[]types.Evidence{{Kind: types.EvidenceTestOutput, Label: "Testes", Content: "312/312"}},
		gate.Approved, f.now.Add(-time.Hour), f.now.Add(-59*time.Minute), nil)

	// Single keystroke: `gate approve --ack "Migration"`. Decide dates the mark
	// at the decision itself (internal/gate/store.go:100-110), so the gap is
	// exactly 0 — the measurement risk #1 asks for.
	f.seedOpenGate(t, "run_bbb2", "S11", requiredEvidence(), f.now.Add(-2*time.Hour))
	if _, err := gate.Decide(f.repo, "run_bbb2", "S11", gate.Decision{
		Verdict: gate.Approved, DecidedAt: f.now.Add(-90 * time.Minute), Acked: []string{"Migration"},
	}); err != nil {
		t.Fatalf("deciding the inline-ack gate: %v", err)
	}

	// Read on Monday, decided on Tuesday: a separate `gate ack` left a mark the
	// approval did not move, so the gap is positive and provable.
	f.seedOpenGate(t, "run_bbb3", "S12", requiredEvidence(), f.now.Add(-3*time.Hour))
	if _, _, err := gate.MarkRead(f.repo, "run_bbb3", "S12", []string{"Migration"}, f.now.Add(-150*time.Minute)); err != nil {
		t.Fatalf("marking read: %v", err)
	}
	if _, err := gate.Decide(f.repo, "run_bbb3", "S12", gate.Decision{
		Verdict: gate.Approved, DecidedAt: f.now.Add(-120 * time.Minute),
	}); err != nil {
		t.Fatalf("deciding the pre-read gate: %v", err)
	}

	audit, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}

	if gap := rowOf(t, audit, "S10").ReadGapMs; gap != nil {
		t.Errorf("read gap is %dms on a gate with no required reading, want absent", *gap)
	}
	if gap := rowOf(t, audit, "S11").ReadGapMs; gap == nil || *gap != 0 {
		t.Errorf("read gap is %v on the inline --ack gate, want exactly 0", gap)
	}
	if gap := rowOf(t, audit, "S12").ReadGapMs; gap == nil || *gap != (30*time.Minute).Milliseconds() {
		t.Errorf("read gap is %v on the pre-read gate, want 30m", gap)
	}
	// Counting by Decision.Acked would report zero acknowledgement on S12, which
	// is the gate where the human actually read first.
	if row := rowOf(t, audit, "S12"); row.ReadMarks != 1 || row.RequiredReading != 1 {
		t.Errorf("S12 reports %d read marks over %d required items, want 1 over 1", row.ReadMarks, row.RequiredReading)
	}
	// Smallest gap first: the keystroke has to be the first thing on the screen.
	if audit.Rows[0].StepID != "S11" {
		t.Errorf("the first row is %s, want S11 — the zero gap must sort to the top", audit.Rows[0].StepID)
	}
}

// TestLoadGateAudit_P95WithheldBelowTwentySamples keeps a percentile from being
// printed over an anecdote.
func TestLoadGateAudit_P95WithheldBelowTwentySamples(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	base := f.now.Add(-10 * time.Hour)
	seed := func(n int) {
		for i := 0; i < n; i++ {
			f.seedDecidedGate(t, fmt.Sprintf("run_c%03x", i), "S20", nil, gate.Approved,
				base, base.Add(time.Duration(i+1)*time.Second), nil)
		}
	}

	seed(P95MinSample - 1)
	audit, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	g := groupOf(t, audit, "gate-demo/S20")
	if g.Judged != P95MinSample-1 {
		t.Fatalf("judged is %d, want %d", g.Judged, P95MinSample-1)
	}
	if g.LatencyMsP95 != nil {
		t.Errorf("p95 is %dms over %d samples, want absent below %d", *g.LatencyMsP95, g.Judged, P95MinSample)
	}
	// min/median/max are emitted anyway: withholding everything would leave the
	// reader with no measurement at all, which is worse than a small sample they
	// can see the size of.
	if g.LatencyMsMin == nil || g.LatencyMsMedian == nil || g.LatencyMsMax == nil {
		t.Errorf("min/median/max are %v/%v/%v, want all three present", g.LatencyMsMin, g.LatencyMsMedian, g.LatencyMsMax)
	}

	f.seedDecidedGate(t, "run_cfff", "S20", nil, gate.Approved, base, base.Add(999*time.Second), nil)
	audit, err = f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	g = groupOf(t, audit, "gate-demo/S20")
	if g.Judged != P95MinSample {
		t.Fatalf("judged is %d, want %d", g.Judged, P95MinSample)
	}
	if g.LatencyMsP95 == nil {
		t.Fatalf("p95 is absent at exactly %d samples, want a number", P95MinSample)
	}
	// Nearest rank, no interpolation: the printed number is a wait somebody
	// actually had. With 20 samples that is the 19th — the 999s outlier is the
	// max and stays out of the p95, which is why both are emitted.
	if *g.LatencyMsP95 != (19 * time.Second).Milliseconds() {
		t.Errorf("p95 is %dms, want 19000 (the 19th of 20 samples by nearest rank)", *g.LatencyMsP95)
	}
	if g.LatencyMsMax == nil || *g.LatencyMsMax != (999*time.Second).Milliseconds() {
		t.Errorf("max is %v, want the 999s outlier", g.LatencyMsMax)
	}
}

// TestLoadGateAudit_MissingRepoContributesZero: the index remembers repositories
// that moved or were deleted. One of them must not take the history of the others
// down with it.
func TestLoadGateAudit_MissingRepoContributesZero(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	dead := t.TempDir()
	reg := run.Registry{
		Repo: dead, Home: f.home,
		NewID:   func() (string, error) { return "run_dead", nil },
		Now:     func() time.Time { return f.now },
		Machine: "test-machine",
	}
	if _, err := reg.Start(run.StartOptions{Project: "gone", Recipe: "gone"}); err != nil {
		t.Fatalf("registering the doomed run: %v", err)
	}
	if err := os.RemoveAll(dead); err != nil {
		t.Fatalf("removing the repo: %v", err)
	}

	audit, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	if audit.Repos != 2 {
		t.Errorf("the audit reports %d repositories scanned, want 2 — a shrunken population must be visible, not silent", audit.Repos)
	}
	if len(audit.Rows) != 1 {
		t.Fatalf("the audit reports %d rows, want the 1 gate of the repository that still exists", len(audit.Rows))
	}
}

// TestLoadGateAudit_WindowFiltersByOpenedAt: --since narrows by when the gate was
// opened, the way `run list --since` narrows by when the run started.
func TestLoadGateAudit_WindowFiltersByOpenedAt(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	f.seedDecidedGate(t, "run_ddd1", "S30", nil, gate.Approved,
		f.now.Add(-30*24*time.Hour), f.now.Add(-30*24*time.Hour).Add(time.Minute), nil)

	windowed, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{Since: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	for _, r := range windowed.Rows {
		if r.StepID == "S30" {
			t.Errorf("a gate opened 30 days ago survived --since 7d")
		}
	}
	if windowed.Since != 7*24*time.Hour {
		t.Errorf("the audit reports the window as %s, want 168h — an empty screen must be able to name its filter", windowed.Since)
	}

	all, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	if len(all.Rows) != 2 {
		t.Errorf("without a window the audit reports %d rows, want 2", len(all.Rows))
	}
}

// TestLoadGateAudit_NatureFilterKeepsOneDistribution: today every gate file is
// human, because humanGate is the only writer of one. The filter exists so that
// the day a policy gate is persisted, a 12ms exit code cannot drag a human median
// down with it.
func TestLoadGateAudit_NatureFilterKeepsOneDistribution(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	if err := gate.Open(gate.Pending{
		RunID: "run_eee1", StepID: "S40", Repo: f.repo, Project: "demo", Recipe: "gate-demo",
		Nature: types.GatePolicy, Label: "Ceiling", OpenedAt: f.now.Add(-time.Hour),
		Decision: &gate.Decision{Verdict: gate.Approved, DecidedAt: f.now.Add(-time.Hour).Add(12 * time.Millisecond)},
	}); err != nil {
		t.Fatalf("seeding the policy gate: %v", err)
	}

	all, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	if len(all.Rows) != 2 {
		t.Fatalf("unfiltered audit has %d rows, want 2 (positive control)", len(all.Rows))
	}

	human, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{Nature: types.GateHuman})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	if len(human.Rows) != 1 || human.Rows[0].Nature != types.GateHuman {
		t.Fatalf("filtering by human keeps %d rows: %+v", len(human.Rows), human.Rows)
	}
}

// TestGateAuditRow_CarriesNoEvidence is the privacy guard. `gate show --json`
// embeds gate.Pending whole, evidence and all, because a person asked to read it.
// An audit is a different act: it is the shape a script or an HTTP client pulls
// routinely, and Evidence.Content carries diffs, test output and production schema
// names.
func TestGateAuditRow_CarriesNoEvidence(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, []types.Evidence{
		{Kind: types.EvidenceTestOutput, Label: "Testes", RequiredReading: true,
			Content: "ANTHROPIC_API_KEY=sk-ant-CANARY111 /Users/someone/secret"},
	})
	audit, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	buf, err := json.Marshal(audit)
	if err != nil {
		t.Fatalf("marshalling the audit: %v", err)
	}
	for _, canary := range []string{"CANARY111", "sk-ant", "/Users/someone"} {
		if strings.Contains(string(buf), canary) {
			t.Fatalf("the audit JSON leaked %q — the audit carries labels and counts, never content", canary)
		}
	}

	// An allowlist rather than a canary alone, in the shape of
	// TestEntry_JSONKeysAreAnAllowlist: a field added later has to be argued for
	// here, because a canary only catches the leak somebody thought of.
	allowed := map[string]string{
		"key":              "recipe/step — the user's own recipe and step names",
		"run_id":           "`run_` + 4 hex, identifies the run and describes nothing else",
		"step_id":          "a step id from the user's own recipe",
		"repo":             "a repository path, as RunRow already carries one",
		"project":          "the user's own project name",
		"recipe":           "the user's own recipe name",
		"nature":           "human | computational | inferential | policy",
		"label":            "the gate's label: the user's own words, already in the ledger",
		"verdict":          "approved | rejected | expired",
		"opened_at":        "a timestamp",
		"decided_at":       "a timestamp",
		"latency_ms":       "a duration",
		"read_gap_ms":      "a duration",
		"required_reading": "a COUNT of required items, never their labels or content",
		"read_marks":       "a COUNT of reading marks",
	}
	typ := reflect.TypeOf(GateAuditRow{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if key == "" {
			key = field.Name
		}
		if _, ok := allowed[key]; !ok {
			t.Errorf("GateAuditRow.%s publishes %q, which is not on the allowlist.\n"+
				"An audit row is pulled routinely by scripts and by the HTTP surface: add the key here\n"+
				"with a note saying why its value is safe to hand out, or keep it in the gate file.",
				field.Name, key)
		}
	}
}

// TestGateVerdict_ThreeBucketsAndNoMore is the tripwire, in the spirit of
// TestLedgerTypeLiterals_TrackTheEventVocabulary: the audit buckets verdicts by
// name, so a fourth verdict added upstream would silently fall out of
// approved/rejected/expired and quietly shrink every rejection rate on the
// screen.
//
// It reads the source rather than calling IsValid, because IsValid would happily
// accept a fourth value: what has to fail here is the ADDITION, not a lookup.
func TestGateVerdict_ThreeBucketsAndNoMore(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "gate", "gate.go"))
	if err != nil {
		t.Fatalf("reading internal/gate/gate.go: %v", err)
	}
	found := regexp.MustCompile(`Verdict\s*=\s*"([a-z_]+)"`).FindAllStringSubmatch(string(src), -1)
	got := make([]string, 0, len(found))
	for _, m := range found {
		got = append(got, m[1])
	}
	want := []string{string(gate.Approved), string(gate.Rejected), string(gate.Expired)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gate.Verdict declares %v, and the audit buckets exactly %v.\n"+
			"A new verdict needs a bucket in groupAuditRows and a decision about the rejection rate:\n"+
			"is it a judgement (denominator) or an absence (reported beside it, like expiry)?", got, want)
	}
	// The invariant the JSON exposes: Decided is the sum of the three. Asserted
	// on a real corpus so the claim in GateAuditGroup's doc is not just prose.
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	base := f.now.Add(-time.Hour)
	for i, v := range []gate.Verdict{gate.Approved, gate.Rejected, gate.Expired} {
		f.seedDecidedGate(t, fmt.Sprintf("run_f00%d", i), "S50", nil, v, base, base.Add(time.Minute), nil)
	}
	audit, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	g := groupOf(t, audit, "gate-demo/S50")
	if g.Decided != g.Approved+g.Rejected+g.Expired || g.Decided != 3 {
		t.Errorf("decided=%d but approved+rejected+expired=%d", g.Decided, g.Approved+g.Rejected+g.Expired)
	}
}

// TestLoadGateAudit_LatencyPopulationIsTheSampleNotTheJudgedCount is the
// arithmetic behind the `(measured on X of Y)` the CLI prints.
//
// auditRow leaves LatencyMs nil for a gate that was judged but whose file has no
// `decided_at` — on purpose, because the alternative is inventing a latency from
// now(), which measures when the audit ran and not how long a person took. That
// gate is Judged and contributes nothing to the distribution, so a screen that
// labelled the median `n = Judged` credited it with a sample it never saw.
func TestLoadGateAudit_LatencyPopulationIsTheSampleNotTheJudgedCount(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	base := f.now.Add(-4 * time.Hour)
	read := []gate.ReadMark{{Label: "Migration", At: base.Add(time.Minute)}}

	f.seedDecidedGate(t, "run_bbb1", "S31", requiredEvidence(), gate.Rejected, base, base.Add(5*time.Minute), read)
	// Judged, and unmeasurable: no decided_at on the file.
	f.seedDecidedGate(t, "run_bbb2", "S31", requiredEvidence(), gate.Rejected, base, time.Time{}, read)

	audit, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	g := groupOf(t, audit, "gate-demo/S31")

	if g.Judged != 2 {
		t.Fatalf("judged is %d, want 2: both gates were answered by a person", g.Judged)
	}
	if g.LatencyMeasured != 1 {
		t.Errorf("latency_measured is %d, want 1 — only one of the two gates carries a decided_at", g.LatencyMeasured)
	}
	if g.LatencyMsMedian == nil || *g.LatencyMsMedian != (5*time.Minute).Milliseconds() {
		t.Errorf("the median is %v, want the one measurable latency (5m)", g.LatencyMsMedian)
	}
	if g.LatencyMeasured == g.Judged {
		t.Error("the latency population equals the judged count, which is exactly the overstatement this guards")
	}
}

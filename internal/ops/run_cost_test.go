package ops

// Money on the list has to be the SAME money as inside the run.
//
// The list and the detail are two screens over one fact, and a runner whose
// history says $0.40 while the run it opens says $1.20 has taught its user to
// trust neither. So the row does not compute spend a second way — it walks the
// same rows the report walks — and this is the test that keeps it that way when
// somebody optimises one of the two.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// writeCostLedger lays down a project whose ledger holds one worker line and one
// review line per task, plus a RETRY of S01 — the shape that separates "sum
// every line" from the rule the report uses, where a retried task counts once.
func writeCostLedger(t *testing.T, repo, project, runID string) {
	t.Helper()
	dir := filepath.Join(repo, ".corvex", "tasks", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tasks := "---\ngenerated_by: t\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — Um ✅ PASSED\n\n```yaml\ntype: general\n```\n\n### O que fazer\nx\n\n### Critérios de sucesso\n- [ ] y\n\n" +
		"## S02 — Dois ✅ PASSED\n\n```yaml\ntype: general\ndepends_on: [S01]\n```\n\n### O que fazer\nx\n\n### Critérios de sucesso\n- [ ] y\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(tasks), 0o644); err != nil {
		t.Fatal(err)
	}

	lines := []string{
		// S01's first attempt, then a retry, then the attempt that stuck. Summing
		// every line would count the first attempt too.
		`{"timestamp":"2026-08-17T11:00:00Z","type":"task_complete","run_id":"` + runID + `","task_id":"S01","cost_usd":0.30}`,
		`{"timestamp":"2026-08-17T11:01:00Z","type":"retry","run_id":"` + runID + `","task_id":"S01"}`,
		`{"timestamp":"2026-08-17T11:02:00Z","type":"review_result","run_id":"` + runID + `","task_id":"S01","cost_usd":0.05}`,
		`{"timestamp":"2026-08-17T11:03:00Z","type":"task_complete","run_id":"` + runID + `","task_id":"S01","cost_usd":0.40}`,
		`{"timestamp":"2026-08-17T11:04:00Z","type":"review_result","run_id":"` + runID + `","task_id":"S02","cost_usd":0.10}`,
		`{"timestamp":"2026-08-17T11:05:00Z","type":"task_complete","run_id":"` + runID + `","task_id":"S02","cost_usd":1.00}`,
		// Another run in the same project: its money is not this run's money.
		`{"timestamp":"2026-08-17T11:06:00Z","type":"task_complete","run_id":"run_ffff","task_id":"S02","cost_usd":9.99}`,
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "activity.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCostsByRun_AgreesWithTheRunReport(t *testing.T) {
	f := newRunFixture(t)
	repo := f.add(t, "run_0001", "pilot", time.Hour, run.StatusDone)
	writeCostLedger(t, repo, "pilot", "run_0001")

	costs := f.lister.CostsByRun(repo, "pilot", []string{"run_0001"})
	rep, err := f.lister.LoadRunReport(repo, "run_0001", "")
	if err != nil {
		t.Fatalf("LoadRunReport: %v", err)
	}

	got, ok := costs["run_0001"]
	if !ok {
		t.Fatal("the list priced nothing for a run that spent money")
	}
	if got != rep.CostUSD {
		t.Errorf("the list says %.2f and the run says %.2f — one screen is lying about money", got, rep.CostUSD)
	}
	// And the number itself is the rule, not just self-consistent: the retried
	// attempt counts once (0.40 + 0.05 review) plus S02 (1.00 + 0.10).
	if want := 1.55; got != want {
		t.Errorf("cost = %.2f, want %.2f (a retried task counts once, review folded in)", got, want)
	}
}

func TestCostsByRun_DoesNotBillOneRunForAnother(t *testing.T) {
	f := newRunFixture(t)
	repo := f.add(t, "run_0001", "pilot", time.Hour, run.StatusDone)
	writeCostLedger(t, repo, "pilot", "run_0001")

	costs := f.lister.CostsByRun(repo, "pilot", []string{"run_0001", "run_ffff"})
	if costs["run_0001"] >= 9.99 {
		t.Errorf("run_0001 was billed for the other run's spend: %.2f", costs["run_0001"])
	}
	// The other run's own line is in the same ledger and is priced on its own.
	if costs["run_ffff"] != 9.99 {
		t.Errorf("run_ffff = %.2f, want 9.99", costs["run_ffff"])
	}
}

// A project with no ledger at all — a run that died before its first step — is
// priced at zero rather than failing the screen it feeds.
func TestCostsByRun_NoLedgerIsZeroNotAnError(t *testing.T) {
	f := newRunFixture(t)
	repo := f.add(t, "run_0002", "empty", time.Hour, run.StatusFailed)
	costs := f.lister.CostsByRun(repo, "empty", []string{"run_0002"})
	if len(costs) != 0 {
		t.Errorf("an unpriced run got a price: %v", costs)
	}
}

// What a FAILED attempt cost is on the run screen too.
//
// `attempt_cost` is the line written when an attempt does not end in a
// task_complete — a worker call that failed, a review that failed, the call that
// tripped a ceiling. `corvex inspect` has always accumulated it. The run report
// declared an `extra` map for exactly this, read it at the bottom, and had
// nobody writing to it: two screens over one ledger, disagreeing about money,
// with nothing saying so.
func TestRunReport_CountsWhatTheFailedAttemptsCost(t *testing.T) {
	f := newRunFixture(t)
	repo := f.add(t, "run_0003", "pilot", time.Hour, run.StatusFailed)
	dir := filepath.Join(repo, ".corvex", "tasks", "pilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tasks := "---\ngenerated_by: t\ndag:\n  S01: []\n---\n\n" +
		"## S01 — Caro ❌ FAILED\n\n```yaml\ntype: general\n```\n\n### O que fazer\nx\n\n### Critérios de sucesso\n- [ ] y\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(tasks), 0o644); err != nil {
		t.Fatal(err)
	}
	// The shape of a ceiling abort: the attempt spent, and no task_complete ever
	// came.
	body := `{"timestamp":"2026-08-17T11:00:00Z","type":"task_start","run_id":"run_0003","task_id":"S01"}
{"timestamp":"2026-08-17T11:01:00Z","type":"attempt_cost","run_id":"run_0003","task_id":"S01","phase":"worker","cost_usd":4.00,"message":"spend recorded at the ceiling abort"}
{"timestamp":"2026-08-17T11:02:00Z","type":"attempt_cost","run_id":"run_0003","task_id":"S01","phase":"review","cost_usd":0.50}
`
	if err := os.WriteFile(filepath.Join(dir, "activity.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := f.lister.LoadRunReport(repo, "run_0003", "")
	if err != nil {
		t.Fatalf("LoadRunReport: %v", err)
	}
	if rep.CostUSD != 4.50 {
		t.Errorf("the run screen says $%.2f for a run that spent $4.50 and aborted: it errs LOW, which is how a ceiling gets raised by somebody who thinks they have room", rep.CostUSD)
	}
	// And the list agrees with it, which is the property the other test in this
	// file exists to keep.
	if got := f.lister.CostsByRun(repo, "pilot", []string{"run_0003"})["run_0003"]; got != rep.CostUSD {
		t.Errorf("the list says %.2f and the run says %.2f", got, rep.CostUSD)
	}
}

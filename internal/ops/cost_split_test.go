package ops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/activity"
)

// The property the ledger split has to preserve: the LINES separate worker from
// reviewer, and the per-task total still means everything the step cost.
//
// Both halves matter and they pull in opposite directions — which is why this
// asserts them together. Splitting the lines is what makes the cost-by-nature
// bar honest; re-adding them per task is what stops "how much did S01 cost"
// from silently dropping the reviewer's share.
func TestCostSplit_LinesSeparateButTheTaskTotalStaysWhole(t *testing.T) {
	workDir := t.TempDir()
	const project = "alpha"
	dir := filepath.Join(workDir, ".corvex", "tasks", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"),
		[]byte("---\ngenerated_by: t\ndag:\n  S01: []\n---\n\n## S01 — Step ✅ PASSED\n\n```yaml\ntype: general\n```\n\n### O que fazer\nx\n\n### Critérios de sucesso\n- [ ] y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledger, err := activity.New(workDir, project, activity.Identity{RunID: "run_ab12"})
	if err != nil {
		t.Fatalf("activity.New: %v", err)
	}
	for _, e := range []activity.Entry{
		{Type: "attempt_cost", TaskID: "S01", Phase: "worker", CostUSD: 0.10, Message: "worker attempt rejected by review"},
		{Type: "review_result", TaskID: "S01", Phase: "review", CostUSD: 0.03, Message: "FAIL"},
		{Type: "review_result", TaskID: "S01", Phase: "review", CostUSD: 0.02, Message: "PASS"},
		{Type: "task_complete", TaskID: "S01", Phase: "worker", CostUSD: 0.20, Status: "PASSED"},
	} {
		if err := ledger.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	entries, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("activity.Read: %v", err)
	}

	// Half one: the phase buckets are honest. Review is 0.05, not zero.
	sum := activity.AggregateEntries(entries)
	if got := sum.PerPhase["review"].CostUSD; !closeTo(got, 0.05) {
		t.Errorf("review bucket = %.4f, want 0.05 — the reviewer's money is back in the worker bucket", got)
	}
	if got := sum.PerPhase["worker"].CostUSD; !closeTo(got, 0.30) {
		t.Errorf("worker bucket = %.4f, want 0.30 (0.10 rejected attempt + 0.20 winning attempt)", got)
	}

	// Half two: the step's own total is still everything it cost.
	report, err := BuildInspectReport(workDir, project, entries)
	if err != nil {
		t.Fatalf("BuildInspectReport: %v", err)
	}
	if len(report.Tasks) != 1 {
		t.Fatalf("got %d task rows, want 1", len(report.Tasks))
	}
	if got := report.Tasks[0].CostUSD; !closeTo(got, 0.35) {
		t.Errorf("task cost = %.4f, want 0.35 — the split must not make a step look cheaper than it was", got)
	}
}

// closeTo keeps this file out of the float-equality trap the repo already
// documented: aggregated cost is a sum of float64 and must never be compared
// with ==.
func closeTo(got, want float64) bool {
	d := got - want
	return d < 1e-9 && d > -1e-9
}

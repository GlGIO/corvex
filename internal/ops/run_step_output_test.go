package ops

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/stepout"
)

// `run show <id> --step S03` is the canonical detail surface, and it is the one
// that has to answer without the terminal that ran the recipe.
//
// The dogfood it comes from: a stage failed, the operator opened this screen, and
// it listed task_start / tool_use / tool_result / task_complete plus the same
// content-free phrase the terminal had shown. The reason the command gave was
// captured and dropped.
//
// It cannot come from the ledger. `activity.jsonl` is committed by corvex's own
// auto_commit, which is why activity.Entry carries a tool's NAME and never its
// arguments; a command's output is the same class of content. So this screen
// joins two stores, and what this test pins is the join: the (repo, run id, step
// id) tuple has to be the run's own, or the screen reads an empty file and looks
// exactly like the bug.
func TestLoadRunReport_StepShowsWhatTheFailedCommandSaid(t *testing.T) {
	const said = "ERROR: --repository is required"
	f := newRunFixture(t)
	repo := f.add(t, "run_0001", "ship", time.Hour, run.StatusFailed)
	writeStepFixture(t, repo, "ship")

	if err := stepout.Write(repo, "run_0001", "S03", said); err != nil {
		t.Fatalf("stepout.Write: %v", err)
	}

	rep, err := f.lister.LoadRunReport(repo, "run_0001", "S03")
	if err != nil {
		t.Fatalf("LoadRunReport: %v", err)
	}
	if rep.Step == nil {
		t.Fatal("no step detail")
	}
	if rep.Step.Output != said {
		t.Errorf("Step.Output = %q, want %q — the screen read the wrong (repo, run, step) tuple", rep.Step.Output, said)
	}

	// The other step of the same run stored nothing, and must report nothing:
	// this store is a diagnostic keyed per step, not a per-run log that every
	// step's screen shows.
	other, err := f.lister.LoadRunReport(repo, "run_0001", "S01")
	if err != nil {
		t.Fatalf("LoadRunReport S01: %v", err)
	}
	if other.Step.Output != "" {
		t.Errorf("S01 reports Output %q, want empty", other.Step.Output)
	}
}

// A run that predates the store, or one whose steps all passed, gets the screen
// it always got. This is the property the characterization goldens of `run show`
// rest on, asserted here rather than left to them.
func TestLoadRunReport_StepWithoutStoredOutputIsUnchanged(t *testing.T) {
	f := newRunFixture(t)
	repo := f.add(t, "run_0002", "ship", time.Hour, run.StatusDone)
	writeStepFixture(t, repo, "ship")

	rep, err := f.lister.LoadRunReport(repo, "run_0002", "S03")
	if err != nil {
		t.Fatalf("LoadRunReport: %v", err)
	}
	if rep.Step.Output != "" {
		t.Errorf("Step.Output = %q on a run with no stored output", rep.Step.Output)
	}
	// Positive control: the screen really was built, so "Output is empty" is a
	// claim about a populated step rather than about a zero struct.
	if rep.Step.ID != "S03" || len(rep.Tasks) != 2 {
		t.Fatalf("the fixture produced no screen: step %q, %d tasks", rep.Step.ID, len(rep.Tasks))
	}
}

// A run id and a step id that would escape the store's directory must yield an
// empty screen, never a read of somebody else's file. The ids reach here from
// argv.
func TestLoadRunReport_StepIDIsNotAPath(t *testing.T) {
	f := newRunFixture(t)
	repo := f.add(t, "run_0003", "ship", time.Hour, run.StatusFailed)
	writeStepFixture(t, repo, "ship")
	if err := stepout.Write(repo, "run_0003", "S03", "ERROR: --repository is required"); err != nil {
		t.Fatalf("stepout.Write: %v", err)
	}
	// A step id that is not in the plan is refused before any file is opened,
	// which is the layer that stops the traversal. Assert the refusal rather
	// than an empty string, so a future loosening of FindTask cannot slip a path
	// through unnoticed.
	if _, err := f.lister.LoadRunReport(repo, "run_0003", "../../../etc/passwd"); err == nil {
		t.Error("LoadRunReport accepted a path as a step id")
	}
}

// writeStepFixture is a two-step project on disk: enough for ReadProject and the
// DAG walk that buildStepDetail depends on.
func writeStepFixture(t *testing.T, repo, project string) {
	t.Helper()
	dir := filepath.Join(repo, ".corvex", "tasks", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ngenerated_by: t\ndag:\n  S01: []\n  S03: [S01]\n---\n\n" +
		"## S01 — Passa ✅ PASSED\n\n```yaml\ntype: general\n```\n\n### O que fazer\nx\n\n### Critérios de sucesso\n- [ ] y\n\n" +
		"## S03 — Abre o PR ❌ FAILED\n\n```yaml\ntype: general\ndepends_on: [S01]\n```\n\n### O que fazer\nx\n\n### Critérios de sucesso\n- [ ] y\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

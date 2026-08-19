package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/types"
)

// The terminal half of the fix: the failure line and the reason are one screen.
//
// The dogfood: an operator saw `! S03 failed · command exit did not pass after 1
// iteration(s)`, went to `run show --step S03`, saw the same phrase, and found
// the cause (a CLI wanting `--repository`) only by re-running the command by
// hand. The message existed and was discarded.
func TestPlainRenderer_PrintsWhatTheFailedStepSaid(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlainRenderer(&buf, true /*noColor*/, false /*quiet*/)
	r.render(orchestrator.Event{
		Type: orchestrator.EventTaskComplete, TaskID: "S03", Status: types.StatusFailed,
		Message: "command exit did not pass after 1 iteration(s)",
		Output:  "[3 earlier line(s) dropped]\nERROR: --repository is required",
	})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want the failure line plus the two output lines:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "S03") || !strings.Contains(lines[0], "failed") {
		t.Errorf("first line = %q, want the step's failure line", lines[0])
	}
	// Indented, and directly under its own failure line: under a parallel wave
	// two failures are two events, and what ties output to a step is that the
	// block follows its own header with nothing in between.
	if lines[1] != "    [3 earlier line(s) dropped]" || lines[2] != "    ERROR: --repository is required" {
		t.Errorf("output block = %q / %q, want both indented under the failure", lines[1], lines[2])
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("noColor output should have no ANSI escapes:\n%q", buf.String())
	}
}

// Quiet mode is the one an operator running CI reads, and it is the mode where
// the failure line is the ONLY thing printed. It must carry the reason too.
func TestPlainRenderer_QuietStillPrintsWhatTheFailedStepSaid(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlainRenderer(&buf, true, true /*quiet*/)
	r.render(orchestrator.Event{
		Type: orchestrator.EventTaskComplete, TaskID: "S03", Status: types.StatusFailed,
		Message: "command exit did not pass after 1 iteration(s)",
		Output:  "ERROR: --repository is required",
	})
	if !strings.Contains(buf.String(), "ERROR: --repository is required") {
		t.Errorf("quiet output lost the reason:\n%s", buf.String())
	}
}

// A run where everything passes prints exactly the bytes it printed before. The
// bug was a missing diagnostic on failure; a green run whose output moved would
// be a second bug.
func TestPlainRenderer_PassedAndSkippedAreByteIdentical(t *testing.T) {
	render := func(evs ...orchestrator.Event) string {
		var buf bytes.Buffer
		r := NewPlainRenderer(&buf, true, false)
		for _, ev := range evs {
			r.render(ev)
		}
		return buf.String()
	}
	passed := orchestrator.Event{Type: orchestrator.EventTaskComplete, TaskID: "S01", Status: types.StatusPassed, DurationMs: 72000, CostUSD: 0.41}
	skipped := orchestrator.Event{Type: orchestrator.EventTaskComplete, TaskID: "S02", Status: types.StatusSkipped, Message: "skipped: dependency failed"}
	base := render(passed, skipped)

	// Positive control for the comparison: an Output on these events is ignored,
	// which is what "only a failure carries it" has to mean at the renderer too.
	passed.Output = "tudo bem"
	skipped.Output = "tudo bem"
	if got := render(passed, skipped); got != base {
		t.Errorf("a passing/skipped step's output changed:\n--- want ---\n%s--- got ---\n%s", base, got)
	}
	if strings.Contains(base, "tudo bem") {
		t.Errorf("the baseline already leaked the output, so the comparison proves nothing:\n%s", base)
	}
}

// The canonical detail surface. `run show --step` is where an operator looks
// when the terminal that ran the recipe is gone, or when somebody else ran it.
func TestRenderRunStep_ShowsTheFailedCommandsOutput(t *testing.T) {
	out, _ := captureStdout(t, func() error {
		renderRunStep(ops.RunStepDetail{
			RunTaskRow: ops.RunTaskRow{ID: "S03", Title: "Abre o PR", Status: types.StatusFailed},
			Output:     "ERROR: --repository is required",
		})
		return nil
	})
	if !strings.Contains(out, "Output:") || !strings.Contains(out, "  ERROR: --repository is required") {
		t.Errorf("run show --step lost the command's output:\n%s", out)
	}
	// Before the events: the events are a timeline to scan, the output is the
	// answer the reader came for.
	if i, j := strings.Index(out, "ERROR: --repository"), strings.Index(out, "No events recorded"); i < 0 || j < 0 || i > j {
		t.Errorf("the output block is not above the events:\n%s", out)
	}
}

// A step with nothing stored prints what it always printed — the property the
// characterization goldens of `run show` depend on.
func TestRenderRunStep_WithoutOutputIsUnchanged(t *testing.T) {
	out, _ := captureStdout(t, func() error {
		renderRunStep(ops.RunStepDetail{
			RunTaskRow: ops.RunTaskRow{ID: "S01", Title: "Passa", Status: types.StatusPassed},
			Summary:    "ran command: echo tudo bem",
		})
		return nil
	})
	if strings.Contains(out, "Output:") {
		t.Errorf("a step with no stored output grew an Output block:\n%s", out)
	}
}

// The third symptom of the same dogfood: an operator whose stage failed has a
// run id in hand — it is what `--plain` and `run list` print — so `corvex logs
// run_8a2c` is the obvious next thing to type. It used to answer "parsing tasks
// .../.corvex/tasks/run_8a2c/tasks.md: no such file or directory": a filesystem
// path for a well-formed id, which reads as corruption and is really a category
// error.
//
// `logs` stays project-scoped (it prints every task of one project, and it has
// been deprecated since F3 in favour of `run show --step`), so the fix is to say
// so and name the command that does take a run id. One hop instead of a dead end.
func TestLogs_RunIDPointsAtTheCommandThatTakesOne(t *testing.T) {
	err := runLogs(nil, []string{"run_8a2c"})
	if err == nil {
		t.Fatal("runLogs accepted a run id")
	}
	msg := err.Error()
	for _, want := range []string{"run id, not a project", "corvex run show run_8a2c --step"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to contain %q", msg, want)
		}
	}
	// The old message named a tasks.md path. Naming one again would mean the
	// category error came back wearing different words.
	if strings.Contains(msg, "tasks.md") || strings.Contains(msg, "no such file") {
		t.Errorf("error = %q — still a filesystem answer to an identity question", msg)
	}
}

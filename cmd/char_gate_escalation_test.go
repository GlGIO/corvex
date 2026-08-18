package cmd

// Escalations inside the gate inbox (F3, D6 — the one decision of this phase
// that changes a FLOW rather than a name).
//
// Before: `corvex review` listed the files and the file itself told the human to
// delete it by hand and re-run. After: the inbox lists them next to the gates,
// and closing one is a command that also decides whether the step becomes
// runnable again.

import (
	"path/filepath"
	"strings"
	"testing"
)

const escalationBody = `# Escalation: alpha / S02

- Category: ` + "`tests`" + `
- Logged at: 2026-08-17T12:00:00Z

The task hit the escalation threshold for this category.
`

func withEscalation(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(filepath.Join(".corvex", "escalations", "alpha-S02.md"), escalationBody)
	return f
}

func TestCharacterizeGateListShowsEscalations(t *testing.T) {
	privateIndex(t)
	f := withEscalation(t)

	args := []string{"gate", "list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_list_escalation", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeGateShowEscalation(t *testing.T) {
	privateIndex(t)
	f := withEscalation(t)

	args := []string{"gate", "show", "alpha", "--step", "S02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_show_escalation", scrub(transcript(args, stdout, stderr, err)))
}

// Approving means "I fixed the underlying problem": the file goes, and the step
// becomes runnable again. Both halves are asserted — a command that deleted the
// file and left the step FAILED would look successful and change nothing.
func TestCharacterizeGateApproveClosesAnEscalationAndReopensTheStep(t *testing.T) {
	privateIndex(t)
	f := withEscalation(t)

	args := []string{"gate", "approve", "alpha", "--step", "S02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_approve_escalation", scrub(transcript(args, stdout, stderr, err)))

	if _, statErr := statFile(f.Path(".corvex", "escalations", "alpha-S02.md")); statErr == nil {
		t.Error("the escalation file survived an approval")
	}
	tasks := f.Read(filepath.Join(".corvex", "tasks", "alpha", "tasks.md"))
	if !strings.Contains(tasks, "S02 — Second Task ⬜ PENDING") {
		t.Errorf("S02 was not made runnable again:\n%s", tasks)
	}
}

// Rejecting means "I am not going to fix it": the file goes, the step does not
// come back.
func TestCharacterizeGateRejectClosesAnEscalationAndLeavesTheStep(t *testing.T) {
	privateIndex(t)
	f := withEscalation(t)
	tasksRel := filepath.Join(".corvex", "tasks", "alpha", "tasks.md")
	f.Write(tasksRel, strings.Replace(f.Read(tasksRel), "S02 — Second Task ⬜ PENDING", "S02 — Second Task ❌ FAILED", 1))

	args := []string{"gate", "reject", "alpha", "--step", "S02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_reject_escalation", scrub(transcript(args, stdout, stderr, err)))

	if _, statErr := statFile(f.Path(".corvex", "escalations", "alpha-S02.md")); statErr == nil {
		t.Error("the escalation file survived a rejection")
	}
	if !strings.Contains(f.Read(tasksRel), "S02 — Second Task ❌ FAILED") {
		t.Error("rejecting an escalation must not touch the step's status")
	}
}

func TestCharacterizeGateEscalationNeedsTheStep(t *testing.T) {
	privateIndex(t)
	f := withEscalation(t)

	args := []string{"gate", "approve", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_approve_escalation_no_step", scrub(transcript(args, stdout, stderr, err)))
	if _, statErr := statFile(f.Path(".corvex", "escalations", "alpha-S02.md")); statErr != nil {
		t.Error("a refused approval must not remove the file")
	}
}

// The legacy command still works and still prints what it always printed —
// which is what makes absorbing it safe to ship before the UI exists.
func TestCharacterizeReviewStillWorksAlongsideTheInbox(t *testing.T) {
	privateIndex(t)
	f := withEscalation(t)

	stdout, _, err := runCLIIn(t, f.Dir, "review")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(stdout, "alpha-S02.md") {
		t.Errorf("legacy review stopped listing escalations:\n%s", stdout)
	}
}

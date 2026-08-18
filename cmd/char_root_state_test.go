package cmd

// `corvex` with no arguments, dynamic completion, and the typo guard that the
// state screen could have destroyed.
//
// Making the root command runnable is the sharp edge here: cobra stops
// rejecting unknown commands by itself once a root has a RunE, so `corvex
// stauts` would print the state screen and exit 0 — a typo becoming a silent
// no-op. The guard has its own test for that reason.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCharacterizeBareCorvexOnAQuietMachine(t *testing.T) {
	privateIndex(t)
	f := newFixture(t)

	args := []string{}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "rootstate_quiet", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeBareCorvexShowsWhatIsWaiting(t *testing.T) {
	privateIndex(t)
	f := withEscalation(t)

	args := []string{}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "rootstate_waiting", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeBareCorvexAfterARealRun(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}

	args := []string{}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "rootstate_after_run", scrub(transcript(args, stdout, stderr, err)))
}

// The guard: a mistyped command must still be an error with a suggestion, not a
// state screen.
func TestCharacterizeUnknownCommandStillFails(t *testing.T) {
	privateIndex(t)
	f := newFixture(t)

	args := []string{"stauts"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "rootstate_unknown_command", scrub(transcript(args, stdout, stderr, err)))
	if err == nil {
		t.Fatal("an unknown command returned nil: the typo guard is gone")
	}
}

// Completion is deterministic and costs no token, which is the whole reason the
// roadmap chose it over natural language. What it must never do is offer a run
// that is not waiting for a decision.
func TestCharacterizeGateCompletionOffersOnlyWhatIsWaiting(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}
	f.Write(filepath.Join(".corvex", "escalations", "alpha-S02.md"), escalationBody)
	finished := onlyRunID(t, f.Dir)

	stdout, _, err := runCLIIn(t, f.Dir, "__complete", "gate", "approve", "")
	if err != nil {
		t.Fatalf("__complete: %v", err)
	}
	if strings.Contains(stdout, finished) {
		t.Errorf("completion offered %s, a run that finished and is waiting for nobody:\n%s", finished, stdout)
	}
	if !strings.Contains(stdout, "alpha") {
		t.Errorf("completion did not offer the project with an open escalation:\n%s", stdout)
	}
}

// `run show <TAB>` is the opposite case: there the ids of finished runs are
// exactly what the user wants.
func TestCharacterizeRunCompletionOffersRunIDs(t *testing.T) {
	privateIndex(t)
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)
	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("seeding run: %v", err)
	}
	id := onlyRunID(t, f.Dir)

	stdout, _, err := runCLIIn(t, f.Dir, "__complete", "run", "show", "")
	if err != nil {
		t.Fatalf("__complete: %v", err)
	}
	if !strings.Contains(stdout, id) {
		t.Errorf("completion did not offer %s:\n%s", id, stdout)
	}
}

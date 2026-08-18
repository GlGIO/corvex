package cmd

// The stopping rule for `grill` (roadmap backlog: "sem regra de parada = bug
// ativo"). The bug it bounds is cumulative and cross-session — 28 questions over
// two weeks, none of them ever planned — so the test that matters is the one
// where the questions were answered in a PREVIOUS session and the budget still
// stops the next one.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/ops"
)

// decisionsWith writes n already-answered questions the way ops.AppendDecision
// does, so the counter reads real files rather than a fixture shape it invented.
func decisionsWith(t *testing.T, f *fixture, project string, n int) string {
	t.Helper()
	path := filepath.Join(f.Dir, ".corvex", "tasks", project, "decisions.md")
	for i := 0; i < n; i++ {
		if err := ops.AppendDecision(path, "question "+string(rune('a'+i)), "answer"); err != nil {
			t.Fatalf("seeding decisions: %v", err)
		}
	}
	return path
}

func TestCountDecisions_CountsWhatIsOnDisk(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	path := decisionsWith(t, f, "alpha", 3)
	if got := ops.CountDecisions(path); got != 3 {
		t.Errorf("CountDecisions = %d, want 3", got)
	}
	if got := ops.CountDecisions(filepath.Join(f.Dir, "nope.md")); got != 0 {
		t.Errorf("a project that never grilled counted %d, want 0", got)
	}
}

// The budget stops a project that already spent it in earlier sessions, without
// calling the model at all — no stub is installed here, so a single provider
// call would fail the test loudly.
func TestCharacterizeGrillRefusesOnceTheProjectSpentItsBudget(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	decisionsWith(t, f, "alpha", 12)

	args := []string{"grill", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "grill_budget_reached", scrub(transcript(args, stdout, stderr, err)))
	if err != nil {
		t.Fatalf("hitting the budget must not be an error: %v", err)
	}
	if !strings.Contains(stdout, "corvex plan alpha") {
		t.Errorf("the stop does not say what to do next:\n%s", stdout)
	}
}

// 0 restores the old behaviour on purpose, and the help says it is the bug.
func TestCharacterizeGrillBudgetCanBeDisabled(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	decisionsWith(t, f, "alpha", 40)

	// A stub that fails immediately: the assertion is that the loop got PAST
	// the budget and reached the provider, not that it grilled — and a real
	// provider call here would make the test slow and network-dependent.
	stubClaude(t, "exit 1")
	stdout, _, _ := runCLIIn(t, f.Dir, "grill", "alpha", "--max-questions", "0")
	if strings.Contains(stdout, "at or over the budget") {
		t.Errorf("--max-questions 0 still applied a budget:\n%s", stdout)
	}
}

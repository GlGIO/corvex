package ops

// The leg nobody took, and the moment it stops being one.
//
// MEASURED on the first real incident: the run reached `done`, the fix sat on a
// branch on one machine, and the owner came back an hour later with "cadê o
// PR?". Nothing had failed — the last step PRINTED `Próximo: corvex run start
// ship` into a log that stops being read the moment the run stops being
// interesting. Prose was the only thing holding the thread.

import (
	"fmt"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/recipe"
	"github.com/giovannialves/corvex/internal/run"
)

// chained is the shape under test: a recipe that fixes and deliberately does
// not publish, naming the one that does.
func chained() *recipe.Recipe {
	return &recipe.Recipe{
		Name: "incident",
		Next: []recipe.NextStep{{Recipe: "ship", Why: "publicar o hotfix — este run não abre PR de propósito"}},
	}
}

func loader(byName map[string]*recipe.Recipe) func(string, string) (*recipe.Recipe, error) {
	return func(_, name string) (*recipe.Recipe, error) {
		if r, ok := byName[name]; ok {
			return r, nil
		}
		return nil, fmt.Errorf("no recipe %q", name)
	}
}

var recipes = loader(map[string]*recipe.Recipe{
	"incident": chained(),
	"ship":     {Name: "ship"},
})

const repo = "/repos/smartcare"

func TestHandoffs_AFinishedRunWithAnUntakenLegIsWaitingOnAPerson(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	rows := []RunRow{{
		RunID: "run_999b", Repo: repo, Recipe: "incident", Status: run.StatusDone,
		StartedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour),
	}}

	got := handoffsFrom(rows, recipes, now)
	if len(got) != 1 {
		t.Fatalf("got %d handoffs, want the one the recipe declares: %+v", len(got), got)
	}
	h := got[0]
	if h.Next != "ship" {
		t.Errorf("next = %q, want ship", h.Next)
	}
	if h.Why == "" {
		t.Error("the row carries no reason, so the button would say only a name")
	}
	// The clock runs from when the run FINISHED, not from when it started: the
	// number this row exists to show is how long the thread has been dropped.
	if h.Waiting != time.Hour {
		t.Errorf("waiting = %v, want an hour since the run finished", h.Waiting)
	}
}

// It stops being a handoff the moment the leg is actually taken. No dismiss
// button and no state of its own: the evidence is a run of that recipe, in that
// checkout, started after this one finished.
func TestHandoffs_TakingTheLegRemovesItAndAnEarlierRunDoesNot(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	finished := now.Add(-time.Hour)
	done := RunRow{
		RunID: "run_999b", Repo: repo, Recipe: "incident", Status: run.StatusDone,
		StartedAt: now.Add(-2 * time.Hour), UpdatedAt: finished,
	}
	if got := handoffsFrom([]RunRow{done}, recipes, now); len(got) != 1 {
		t.Fatal("the leg was not offered before it was taken")
	}

	// A `ship` that started BEFORE the incident finished cannot be the one this
	// handoff asks for, and counting it would hide a thread that really was
	// dropped — the shape of a repository where `ship` runs all day.
	earlier := RunRow{RunID: "run_0001", Repo: repo, Recipe: "ship", StartedAt: finished.Add(-30 * time.Minute)}
	if got := handoffsFrom([]RunRow{done, earlier}, recipes, now); len(got) != 1 {
		t.Errorf("an earlier ship was counted as having taken this leg: %+v", got)
	}

	// One started after it is the leg being taken.
	taken := RunRow{RunID: "run_0002", Repo: repo, Recipe: "ship", StartedAt: finished.Add(time.Minute)}
	if got := handoffsFrom([]RunRow{done, earlier, taken}, recipes, now); len(got) != 0 {
		t.Errorf("the leg was taken and the row is still there: %+v", got)
	}

	// And a ship in ANOTHER checkout does not count: the whole point of the
	// button is that the next leg runs on the branch this run left behind.
	elsewhere := RunRow{RunID: "run_0003", Repo: "/repos/outro", Recipe: "ship", StartedAt: finished.Add(time.Minute)}
	if got := handoffsFrom([]RunRow{done, elsewhere}, recipes, now); len(got) != 1 {
		t.Errorf("a ship in another checkout was counted: %+v", got)
	}
}

// A run that failed gets no suggestion. The next leg of this chain publishes,
// and offering it after a failure is offering to publish work that is not
// there.
func TestHandoffs_OnlyAnEndingThatDidTheWorkChains(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		status run.Status
		want   int
	}{
		{run.StatusDone, 1},
		{run.StatusPartial, 0}, // `when` defaults to done, and partial is not done
		{run.StatusFailed, 0},
		{run.StatusCanceled, 0},
		{run.StatusRunning, 0},
		{run.StatusParked, 0},
	} {
		rows := []RunRow{{RunID: "run_x", Repo: repo, Recipe: "incident", Status: tc.status, StartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}}
		if got := handoffsFrom(rows, recipes, now); len(got) != tc.want {
			t.Errorf("status %s offered %d legs, want %d", tc.status, len(got), tc.want)
		}
	}
}

// `when: partial` is how a recipe says what to do with an unfinished run, and
// it is a different leg from the one a clean ending gets.
func TestHandoffs_WhenChoosesTheLegForTheEnding(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	load := loader(map[string]*recipe.Recipe{
		"incident": {Name: "incident", Next: []recipe.NextStep{
			{Recipe: "ship", Why: "publicar", When: "done"},
			{Recipe: "retomar", Why: "terminar o que ficou", When: "partial"},
		}},
	})
	for status, want := range map[run.Status]string{run.StatusDone: "ship", run.StatusPartial: "retomar"} {
		rows := []RunRow{{RunID: "run_x", Repo: repo, Recipe: "incident", Status: status, StartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}}
		got := handoffsFrom(rows, load, now)
		if len(got) != 1 || got[0].Next != want {
			t.Errorf("ending %s offered %+v, want the %q leg", status, got, want)
		}
	}
}

// A recipe with no chain suggests nothing — the feature is opt-in, and a
// repository that never declares `next:` sees no new rows at all.
func TestHandoffs_NoChainNoRow(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	rows := []RunRow{{RunID: "run_x", Repo: repo, Recipe: "ship", Status: run.StatusDone, StartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}}
	if got := handoffsFrom(rows, recipes, now); len(got) != 0 {
		t.Errorf("a recipe with no next: produced %+v", got)
	}
}

// One parse per recipe, not one per run.
//
// The inbox is read on every screen refresh and this walk covers every finished
// run in the window — 64 of them on the machine this was written for, against
// four recipes. Without the cache that is 64 YAML parses to answer a question
// about four files, on a path that runs whenever the page ticks.
func TestHandoffs_ARecipeIsReadOncePerInbox(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	reads := 0
	counting := func(repo, name string) (*recipe.Recipe, error) {
		reads++
		return recipes(repo, name)
	}

	var rows []RunRow
	for i := 0; i < 20; i++ {
		rows = append(rows, RunRow{
			RunID: fmt.Sprintf("run_%04d", i), Repo: repo, Recipe: "incident", Status: run.StatusDone,
			StartedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour),
		})
	}
	if got := handoffsFrom(rows, counting, now); len(got) != 20 {
		t.Fatalf("got %d handoffs, want one per finished run", len(got))
	}
	if reads != 1 {
		t.Errorf("the recipe was read %d times for 20 runs of it", reads)
	}
}

// A recipe that no longer exists is not read again for every run that used it,
// and it does not stop the rest of the box being built.
func TestHandoffs_AMissingRecipeIsSkippedOnceAndDoesNotBreakTheBox(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	reads := 0
	counting := func(repo, name string) (*recipe.Recipe, error) {
		reads++
		return recipes(repo, name)
	}
	rows := []RunRow{
		{RunID: "run_a", Repo: repo, Recipe: "apagada", Status: run.StatusDone, StartedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{RunID: "run_b", Repo: repo, Recipe: "apagada", Status: run.StatusDone, StartedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{RunID: "run_c", Repo: repo, Recipe: "incident", Status: run.StatusDone, StartedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour)},
	}
	got := handoffsFrom(rows, counting, now)
	if len(got) != 1 || got[0].RunID != "run_c" {
		t.Errorf("a deleted recipe took the rest of the box with it: %+v", got)
	}
	if reads != 2 {
		t.Errorf("reads = %d, want one per distinct recipe (the missing one included, once)", reads)
	}
}

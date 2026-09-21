package ops

// The leg that follows a run, held by the system instead of by a sentence.
//
// Every recipe in the field ended with a step that PRINTS the next command —
// `Próximo: corvex run start ship` — into a log nobody reads once the run stops
// being interesting. MEASURED on the first real incident: the run reached
// `done`, the fix sat on a branch on one machine for an hour, and the owner
// came back with "cadê o PR?". Nothing had failed. The thread was dropped,
// because the only thing holding it was prose.
//
// So a finished run with a declared `next:` is something WAITING ON A PERSON,
// in the same inbox as a gate — and it stays there until a run of that recipe
// actually starts in that checkout. That last part is what makes it honest: the
// suggestion is not dismissed by being looked at, and it is not tracked in a
// file that could disagree with reality. It is derived from the run index,
// which is the same source that knows the run finished.

import (
	"sort"
	"time"

	"github.com/giovannialves/corvex/internal/recipe"
	"github.com/giovannialves/corvex/internal/run"
)

// Handoff is one finished run whose next leg has not been taken.
type Handoff struct {
	// From is the run that finished.
	RunID  string `json:"run_id"`
	Repo   string `json:"repo"`
	Recipe string `json:"recipe"`
	// Status is how the run ended — `done` or `partial`. It is on the wire
	// because it changes what the next leg means: shipping after a partial
	// incident publishes a fix whose proof did not finish running.
	Status run.Status `json:"status"`
	// Next is the recipe to run, and the author's one line about why.
	Next string `json:"next"`
	Why  string `json:"why"`
	// Waiting is how long the run has been finished with nobody taking the
	// next leg. It is the number that makes a dropped thread visible.
	Waiting time.Duration `json:"waiting_ns"`
}

// Handoffs finds the finished runs whose declared next leg has not started.
//
// The window is the caller's, and it is the same one the inbox and the history
// use: a handoff older than the window is not a dropped thread any more, it is
// history.
func (g GateLister) Handoffs(localRepo string, since time.Duration) []Handoff {
	rows, err := RunLister{Resolver: g.Resolver, Now: g.Now}.ListRuns(RunListOptions{Since: since})
	if err != nil {
		return nil
	}
	return handoffsFrom(rows, loadRecipe, g.now())
}

// handoffsFrom is the derivation, apart from the disk. Everything interesting
// about a handoff is a question about the SET of runs — which endings chain,
// when a leg counts as taken, how long a thread has been dropped — and none of
// it is a question about how the rows were read.
func handoffsFrom(rows []RunRow, load func(workDir, name string) (*recipe.Recipe, error), now time.Time) []Handoff {
	// started[repo][recipe] is the newest start of that recipe in that
	// checkout. A handoff is taken when its target started AFTER it finished —
	// which is exactly the evidence a person would look for, and it needs no
	// state of its own.
	started := map[string]map[string]time.Time{}
	for _, r := range rows {
		name := r.Recipe
		if name == "" {
			name = r.Project
		}
		if name == "" {
			continue
		}
		byRecipe, ok := started[r.Repo]
		if !ok {
			byRecipe = map[string]time.Time{}
			started[r.Repo] = byRecipe
		}
		if r.StartedAt.After(byRecipe[name]) {
			byRecipe[name] = r.StartedAt
		}
	}

	// One parse per recipe, not one per run. The inbox is read on every screen
	// refresh and this walk covers every finished run in the window — 64 of
	// them on the machine this was written for, which without the cache is 64
	// YAML parses to answer a question about four recipes.
	type key struct{ repo, name string }
	seen := map[key]*recipe.Recipe{}

	var out []Handoff
	for _, r := range rows {
		if r.Recipe == "" || !terminal(r.Status) {
			continue
		}
		k := key{r.Repo, r.Recipe}
		rec, cached := seen[k]
		if !cached {
			loaded, err := load(r.Repo, r.Recipe)
			if err != nil {
				// Cached as nil so a recipe that has been deleted is not read
				// again for every run that used it.
				seen[k] = nil
			} else {
				rec, seen[k] = loaded, loaded
			}
		}
		if rec == nil {
			continue
		}
		finished := r.UpdatedAt
		if finished.IsZero() {
			finished = r.StartedAt
		}
		for _, n := range rec.Next {
			if !n.AppliesTo(string(r.Status)) {
				continue
			}
			if at, ok := started[r.Repo][n.Recipe]; ok && at.After(finished) {
				continue // the leg was taken
			}
			out = append(out, Handoff{
				RunID: r.RunID, Repo: r.Repo, Recipe: r.Recipe, Status: r.Status,
				Next: n.Recipe, Why: n.Why, Waiting: now.Sub(finished),
			})
		}
	}
	// Longest wait first: the thread most likely to have been forgotten is the
	// one that has been dropped longest.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Waiting == out[j].Waiting {
			return out[i].RunID < out[j].RunID
		}
		return out[i].Waiting > out[j].Waiting
	})
	return out
}

// terminal reports an ending a next leg can follow. `failed` and `canceled` are
// NOT endings a chain continues from: the recipe did not do its job, and the
// next leg would publish or act on work that is not there.
func terminal(s run.Status) bool {
	return s == run.StatusDone || s == run.StatusPartial
}

// CheckNextTargets reports a chain that points at a recipe this repository does
// not have. It is the half of the `next:` validation that needs the directory,
// so it lives here rather than on the Recipe.
func CheckNextTargets(workDir string, r *recipe.Recipe) error {
	if len(r.Next) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, name := range RecipeNames(workDir) {
		have[name] = true
	}
	for _, n := range r.Next {
		if !have[n.Recipe] {
			return &RecipeNotFoundError{Path: RecipePath(workDir, n.Recipe)}
		}
	}
	return nil
}

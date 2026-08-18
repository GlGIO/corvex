package ops

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// RunRow is one line of `corvex run list`: who the run is, where it lives, and
// what a reader in another process may conclude about it.
//
// The JSON tags are a contract from F4 onwards — the UI (F7) reads exactly this
// shape over HTTP. Nothing here describes the machine beyond Repo, which the
// listing needs in order to be cross-repository at all; the index that feeds it
// is 0600 in a 0700 directory and never enters a user's history (F1).
type RunRow struct {
	RunID    string       `json:"run_id"`
	Repo     string       `json:"repo"`
	Project  string       `json:"project,omitempty"`
	Recipe   string       `json:"recipe,omitempty"`
	Status   run.Status   `json:"status"`
	Liveness run.Liveness `json:"liveness"`
	// Environment is what stood around the run (F6): `simple` or `stack`.
	// Empty on records written before F6.
	Environment string        `json:"environment,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	UpdatedAt   time.Time     `json:"updated_at,omitempty"`
	Age         time.Duration `json:"age_ns"`
}

// Live reports whether this run still has a process behind it — including one
// on its way out, because a cancelling run is still doing something.
func (r RunRow) Live() bool {
	return r.Liveness == run.LivenessAlive || r.Liveness == run.LivenessCanceling
}

// Label is what a human calls this run: the recipe when there is one, the
// project otherwise. A legacy spec.md run has no recipe by design (F1 refused to
// invent one), so the project name is the honest answer.
func (r RunRow) Label() string {
	if r.Recipe != "" {
		return r.Recipe
	}
	return r.Project
}

// RunListOptions narrows the listing. Zero values mean "everything", which is
// the default the product wants: the question is "what did my agents do", and
// that question has no repository boundary.
type RunListOptions struct {
	// Since drops runs that started longer ago than this. Zero keeps all.
	Since time.Duration
	// Repo keeps only runs of one repository. Empty keeps all.
	Repo string
}

// RunLister reads run identity. Like GateLister it owns no run and writes
// nothing, and its resolver is injectable so a test never reads the real $HOME.
type RunLister struct {
	Resolver run.Resolver
	Now      func() time.Time
}

func (l RunLister) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now().UTC()
}

// ListRuns answers "what ran, everywhere", newest first.
//
// A run appears here as soon as it starts, and stays after it ends: the index is
// append-only with snapshot lines, so the listing is history and inbox at once.
// Liveness is resolved per row rather than read from `status`, for the reason F1
// proved with a foreign reader: a SIGKILLed run keeps `running` on disk forever.
func (l RunLister) ListRuns(opts RunListOptions) ([]RunRow, error) {
	views, err := l.Resolver.List()
	if err != nil {
		return nil, err
	}
	now := l.now()
	want := canonicalRepo(opts.Repo)

	rows := make([]RunRow, 0, len(views))
	for _, v := range views {
		if want != "" && canonicalRepo(v.Record.Repo) != want {
			continue
		}
		age := now.Sub(v.Record.StartedAt)
		if opts.Since > 0 && age > opts.Since {
			continue
		}
		rows = append(rows, RunRow{
			RunID:       v.Record.RunID,
			Repo:        v.Record.Repo,
			Project:     v.Record.Project,
			Recipe:      v.Record.Recipe,
			Status:      v.Record.Status,
			Liveness:    v.Liveness,
			Environment: v.Record.Environment,
			StartedAt:   v.Record.StartedAt,
			UpdatedAt:   v.Record.UpdatedAt,
			Age:         age,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].StartedAt.Equal(rows[j].StartedAt) {
			return rows[i].RunID < rows[j].RunID
		}
		return rows[i].StartedAt.After(rows[j].StartedAt)
	})
	return rows, nil
}

// FindRun resolves one run id through the global index, so a run started in
// another repository is addressable from here.
func (l RunLister) FindRun(runID string) (RunRow, error) {
	view, ok, err := l.Resolver.Get(runID)
	if err != nil {
		return RunRow{}, err
	}
	if !ok {
		return RunRow{}, &UnknownRunError{RunID: runID}
	}
	return RunRow{
		RunID:       view.Record.RunID,
		Repo:        view.Record.Repo,
		Project:     view.Record.Project,
		Recipe:      view.Record.Recipe,
		Status:      view.Record.Status,
		Liveness:    view.Liveness,
		Environment: view.Record.Environment,
		StartedAt:   view.Record.StartedAt,
		UpdatedAt:   view.Record.UpdatedAt,
		Age:         l.now().Sub(view.Record.StartedAt),
	}, nil
}

// LastRunOf returns the most recent run of a project in one repository, and
// false when that project never ran. It is what lets `run show <project>` put an
// identity header on a screen the legacy spec.md flow reaches without ever
// having seen a run id.
func (l RunLister) LastRunOf(repo, project string) (RunRow, bool, error) {
	rows, err := l.ListRuns(RunListOptions{Repo: repo})
	if err != nil {
		return RunRow{}, false, err
	}
	for _, r := range rows {
		if r.Project == project || r.Recipe == project {
			return r, true, nil
		}
	}
	return RunRow{}, false, nil
}

// UnknownRunError reports an id that the index does not know. Ids recycle
// (F1 accepted a 4-digit space with an addressability ceiling), so "unknown"
// legitimately means "not any more" as well as "never".
type UnknownRunError struct{ RunID string }

func (e *UnknownRunError) Error() string {
	return "no such run in the index: " + e.RunID +
		" (ids are recycled once their run ages out — `corvex run list` shows what is addressable)"
}

// SameRepo reports whether two paths name the same repository, after resolving
// symlinks — /tmp is one on darwin, which is where every test lives.
func SameRepo(a, b string) bool { return canonicalRepo(a) == canonicalRepo(b) }

// IsRunID reports whether a positional argument is a run id rather than a
// project name (F3, D2). It lives here so `cmd/` does not have to import the
// identity package to answer a question about its own argument.
func IsRunID(s string) bool { return run.ValidID(s) }

// canonicalRepo makes two spellings of one repository compare equal. Symlinks
// are resolved because /tmp is one on darwin, which is where every test lives.
func canonicalRepo(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

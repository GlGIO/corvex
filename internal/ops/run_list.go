package ops

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
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
	// CostUSD is what this run has spent so far. Filled by the caller that has a
	// screen to draw (CostsByRun); zero in a listing nobody asked to price,
	// because pricing means reading a ledger per project and the index read is
	// on the stream's one-second tick.
	CostUSD float64 `json:"cost_usd,omitempty"`
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
	// Status keeps only runs whose own status matches. Empty keeps all.
	//
	// This is the run's REPORT of itself (`running`, `parked`, `done`, …) and
	// nothing else. Liveness is the other axis and has its own field, because
	// F1 established — with a foreign reader and a SIGKILL — that a status can
	// say `running` forever about a process that is gone. One flag answering
	// both questions would be one flag that is wrong half the time.
	Status run.Status
	// Live keeps only runs with a process behind them (alive or cancelling).
	Live bool
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
	want := CanonicalRepo(opts.Repo)

	rows := make([]RunRow, 0, len(views))
	for _, v := range views {
		if want != "" && CanonicalRepo(v.Record.Repo) != want {
			continue
		}
		age := now.Sub(v.Record.StartedAt)
		if opts.Since > 0 && age > opts.Since {
			continue
		}
		if opts.Status != "" && v.Record.Status != opts.Status {
			continue
		}
		if opts.Live && !(RunRow{Liveness: v.Liveness}).Live() {
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
func SameRepo(a, b string) bool { return CanonicalRepo(a) == CanonicalRepo(b) }

// IsRunID reports whether a positional argument is a run id rather than a
// project name (F3, D2). It lives here so `cmd/` does not have to import the
// identity package to answer a question about its own argument.
func IsRunID(s string) bool { return run.ValidID(s) }

// CanonicalRepo makes two spellings of one repository compare equal: absolute,
// with symlinks resolved — /tmp is one on darwin, which is where every test
// lives, and it is also how a path a person typed and a path a process recorded
// come to differ by nothing anybody can see.
//
// Exported because the UI has to answer the same question when it is asked to
// dispatch into a repository by name: "is this one of the repositories I know".
// Two spellings of that answer would be a door that opens for one and not the
// other.
func CanonicalRepo(path string) string {
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

// ParseRunStatus reads a status the user typed, refusing anything that is not a
// status this tool writes.
//
// Refusing matters more than it looks: the whole reason this filter exists is
// that scripting on top of an unfiltered listing bites. A misspelled `--status
// dnoe` that silently matched nothing would produce an empty list — which reads
// exactly like "nothing is running", the answer the script is waiting for.
func ParseRunStatus(raw string) (run.Status, error) {
	s := run.Status(strings.TrimSpace(raw))
	switch s {
	case "":
		return "", nil
	case run.StatusRunning, run.StatusParked, run.StatusPaused, run.StatusCanceling,
		run.StatusDone, run.StatusFailed, run.StatusCanceled:
		return s, nil
	default:
		return "", fmt.Errorf("unknown status %q: use running, parked, paused, canceling, done, failed or canceled "+
			"(for \"is a process behind it\", that is --live)", raw)
	}
}

// CostsByRun answers "what has each of these runs spent" for one project.
//
// It exists because the money has to be on the LIST, not only inside a run: a
// screen where a $0.40 run and a $23 run look identical until you open them is a
// screen that cannot answer the question people actually have about an agent
// runner. What it must NOT do is compute that number a second way — a row and a
// detail disagreeing about spend is worse than neither showing it, so this walks
// the same buildRunTaskRows the report walks, with the same last-write-wins rule
// that makes a retried task count once.
//
// The project's view and ledger are read ONCE for every run named, because the
// caller is a screen listing a week of runs and the alternative is one full
// report load per row, per poll.
func (l RunLister) CostsByRun(repo, project string, runIDs []string) map[string]float64 {
	out := make(map[string]float64, len(runIDs))
	if repo == "" || project == "" || len(runIDs) == 0 {
		return out
	}
	view, err := ReadProject(repo, project)
	if err != nil {
		return out
	}
	entries, err := activity.Read(repo, project)
	if err != nil || len(entries) == 0 {
		return out
	}
	for _, id := range runIDs {
		if id == "" {
			continue
		}
		var total float64
		for _, row := range buildRunTaskRows(view, activity.FilterByRun(entries, id)) {
			total += row.CostUSD
		}
		out[id] = total
	}
	return out
}

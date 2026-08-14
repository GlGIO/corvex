// Package activity records an append-only timeline of orchestration events
// to disk (`.corvex/tasks/<project>/activity.jsonl`) so debugging "why did
// task X take 11 minutes" or "what was happening between checkpoints" is a
// matter of grepping a file instead of re-running the project.
//
// The format is one JSON object per line (JSONL). High-volume events
// (per-token stream chunks) are intentionally skipped — only state
// transitions, retries, costs, and timings are persisted. A typical
// 30-task run produces ~200–500 entries (a few hundred KB).
//
// Every line also carries the identity of the run that wrote it (run_id, repo,
// recipe), because one project's ledger accumulates many runs: without it, two
// runs are indistinguishable. Lines written before run identity existed have the
// fields absent and must keep reading fine — see Read.
package activity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Identity is the run that owns the lines a Ledger writes: which run, in which
// repository, from which recipe. It is stamped onto every entry by Append so no
// call site has to remember to fill it in — a caller that forgets is exactly how
// a code path ends up producing unattributable lines.
//
// The zero Identity is legal and means "not known yet": Append leaves the
// entry's own values alone, and the fields are omitted from the JSON line. That
// keeps pre-F1 output byte-identical while the run identity is being wired.
type Identity struct {
	// RunID identifies one execution. Generation belongs to the run registry,
	// never to this package — the Ledger only transports the string it is given.
	RunID string
	// Repo is the repository the run executed in, as recorded by the run
	// registry (an absolute path today). Redundant with the ledger's own
	// location on disk, and kept anyway: a line pasted into an issue or shipped
	// to the global index has to be readable without its path.
	Repo string
	// Recipe is the workflow the run executed. Empty until recipes exist (F2).
	Recipe string
}

// Entry is one line in activity.jsonl. Optional fields use omitempty so the
// log stays compact and grep-friendly.
//
// Run identity is carried on *every* line rather than in a per-run header line.
// The header form is more compact but wrong here: two runs of the same project
// append to the same file concurrently, so line order does not establish
// ownership; a truncated header would orphan every line after it; and stateful
// reading would break the one property this package sells — that grepping a
// single line tells you what happened.
type Entry struct {
	Timestamp  time.Time `json:"ts"`
	Type       string    `json:"type"`
	RunID      string    `json:"run_id,omitempty"`
	Repo       string    `json:"repo,omitempty"`
	Recipe     string    `json:"recipe,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	Phase      string    `json:"phase,omitempty"` // worker | review | plan | recovery
	Attempt    int       `json:"attempt,omitempty"`
	DurationMs int64     `json:"duration_ms,omitempty"`
	CostUSD    float64   `json:"cost_usd,omitempty"`
	TokensIn   int       `json:"tokens_in,omitempty"`
	TokensOut  int       `json:"tokens_out,omitempty"`
	Status     string    `json:"status,omitempty"`
	Message    string    `json:"message,omitempty"`
}

// Ledger is a serialised JSONL writer scoped to one project and one run.
// Cheap to construct; each call to Append takes a file-level mutex.
type Ledger struct {
	path string
	id   Identity
	mu   sync.Mutex
}

// New creates a Ledger writing to `<workDir>/.corvex/tasks/<project>/activity.jsonl`,
// stamping every entry with id. Returns an error only if the parent tasks
// directory does not exist — callers should ensure the project has been planned
// at least once.
//
// id is a required argument rather than an optional setter on purpose: the
// compiler then asks every call site "which run is this?", which is the whole
// point of the field existing. Pass the zero Identity where the answer is not
// known yet.
func New(workDir, project string, id Identity) (*Ledger, error) {
	dir := filepath.Join(workDir, ".corvex", "tasks", project)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("activity ledger: project dir %s: %w", dir, err)
	}
	return &Ledger{path: filepath.Join(dir, "activity.jsonl"), id: id}, nil
}

// Append writes one Entry as a JSON line, stamped with the ledger's run
// identity. Errors are returned so callers can log them; we never block the
// orchestrator on ledger failures.
//
// The ledger's identity wins over whatever the entry carries, field by field:
// a Ledger built for a run cannot be talked into mislabelling a line. A field
// the ledger does not know is left as the entry had it, so a replay/import tool
// can preserve the identity of lines it did not produce.
func (l *Ledger) Append(e Entry) error {
	if l == nil {
		return nil
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if l.id.RunID != "" {
		e.RunID = l.id.RunID
	}
	if l.id.Repo != "" {
		e.Repo = l.id.Repo
	}
	if l.id.Recipe != "" {
		e.Recipe = l.id.Recipe
	}

	buf, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("activity ledger marshal: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("activity ledger open %s: %w", l.path, err)
	}
	defer f.Close()

	if _, err := f.Write(append(buf, '\n')); err != nil {
		return fmt.Errorf("activity ledger write: %w", err)
	}
	return nil
}

// Read returns all entries currently in the ledger. Used by `corvex inspect`.
// Skips malformed lines instead of erroring — partial writes from crashed
// runs should not block reading the rest.
func Read(workDir, project string) ([]Entry, error) {
	path := filepath.Join(workDir, ".corvex", "tasks", project, "activity.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading activity ledger %s: %w", path, err)
	}

	var entries []Entry
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // tolerate malformed/truncated entries
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// FilterByRun keeps the entries written by one run, preserving ledger order.
// The result is never nil so a JSON encoder emits [] and not null.
//
// runID == "" selects the lines that carry no run identity at all — ledgers
// written before run identity existed, which is a real population on disk and
// not an error.
func FilterByRun(entries []Entry, runID string) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.RunID == runID {
			out = append(out, e)
		}
	}
	return out
}

// TaskMetric carries the per-task metrics needed to seed the TUI when
// resuming a project that has tasks already completed in previous runs.
type TaskMetric struct {
	TaskID     string  `json:"task_id"`
	DurationMs int64   `json:"duration_ms"`
	CostUSD    float64 `json:"cost_usd"`
	TokensIn   int     `json:"tokens_in"`
	TokensOut  int     `json:"tokens_out"`
}

// Summary aggregates activity.jsonl into the shape the TUI needs on
// startup: per-task duration/cost for already-PASSED tasks (so the DAG
// panel doesn't render them as "0s"), and the cumulative cost/token
// totals (so the header doesn't show "$0.00" while $16.54 has actually
// been spent).
//
// Only the *latest* PASSED completion of each task is counted — retries
// produce multiple task_complete entries for the same task_id, and we
// want the metrics matching the run that actually stuck.
type Summary struct {
	PerTask        map[string]TaskMetric `json:"per_task"`
	TotalCostUSD   float64               `json:"total_cost_usd"`
	TotalTokensIn  int                   `json:"total_tokens_in"`
	TotalTokensOut int                   `json:"total_tokens_out"`
}

// Summarize reads the activity ledger and returns a Summary. Missing
// ledger files are not an error — the caller gets an empty Summary,
// matching the case of a fresh project.
//
// Decided in F1: Summarize stays *project-cumulative*, spanning every run in the
// file. That is not an oversight of run identity, it is the reason the function
// exists — resuming a project must show the $16.54 already spent and the
// durations of tasks that passed in earlier runs, and scoping it to the current
// run would put the "$0.00 after resume" bug back. Cross-run double counting is
// already impossible: metrics are keyed by task and the latest PASSED entry
// wins, so a task re-run in a second run replaces its own numbers instead of
// adding to them. Callers that want one run ask SummarizeRun.
func Summarize(workDir, project string) (Summary, error) {
	entries, err := Read(workDir, project)
	if err != nil {
		return Summary{}, err
	}
	return aggregate(entries), nil
}

// SummarizeRun is Summarize restricted to the lines one run wrote — what a
// per-run view (F7) needs, and what makes two runs of the same project
// distinguishable in a single ledger. Pass "" for the pre-identity lines.
func SummarizeRun(workDir, project, runID string) (Summary, error) {
	entries, err := Read(workDir, project)
	if err != nil {
		return Summary{}, err
	}
	return aggregate(FilterByRun(entries, runID)), nil
}

// aggregate is the shared reduction behind Summarize and SummarizeRun: latest
// PASSED completion per task, then totals over those winners.
func aggregate(entries []Entry) Summary {
	perTask := make(map[string]TaskMetric, len(entries))
	for _, e := range entries {
		if e.Type != "task_complete" || e.Status != "PASSED" || e.TaskID == "" {
			continue
		}
		// Last write wins: later PASSED entries override earlier ones from
		// retried attempts. (A task that previously FAILED then PASSED only
		// contributes its PASSED metrics.)
		perTask[e.TaskID] = TaskMetric{
			TaskID:     e.TaskID,
			DurationMs: e.DurationMs,
			CostUSD:    e.CostUSD,
			TokensIn:   e.TokensIn,
			TokensOut:  e.TokensOut,
		}
	}

	s := Summary{PerTask: perTask}
	for _, m := range perTask {
		s.TotalCostUSD += m.CostUSD
		s.TotalTokensIn += m.TokensIn
		s.TotalTokensOut += m.TokensOut
	}
	return s
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

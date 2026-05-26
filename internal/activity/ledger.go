// Package activity records an append-only timeline of orchestration events
// to disk (`.corvex/tasks/<project>/activity.jsonl`) so debugging "why did
// task X take 11 minutes" or "what was happening between checkpoints" is a
// matter of grepping a file instead of re-running the project.
//
// The format is one JSON object per line (JSONL). High-volume events
// (per-token stream chunks) are intentionally skipped — only state
// transitions, retries, costs, and timings are persisted. A typical
// 30-task run produces ~200–500 entries (a few hundred KB).
package activity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one line in activity.jsonl. Optional fields use omitempty so the
// log stays compact and grep-friendly.
type Entry struct {
	Timestamp  time.Time `json:"ts"`
	Type       string    `json:"type"`
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

// Ledger is a serialised JSONL writer scoped to one project. Cheap to
// construct; each call to Append takes a file-level mutex.
type Ledger struct {
	path string
	mu   sync.Mutex
}

// New creates a Ledger writing to `<workDir>/.corvex/tasks/<project>/activity.jsonl`.
// Returns an error only if the parent tasks directory does not exist —
// callers should ensure the project has been planned at least once.
func New(workDir, project string) (*Ledger, error) {
	dir := filepath.Join(workDir, ".corvex", "tasks", project)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("activity ledger: project dir %s: %w", dir, err)
	}
	return &Ledger{path: filepath.Join(dir, "activity.jsonl")}, nil
}

// Append writes one Entry as a JSON line. Errors are returned so callers can
// log them; we never block the orchestrator on ledger failures.
func (l *Ledger) Append(e Entry) error {
	if l == nil {
		return nil
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
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

// TaskMetric carries the per-task metrics needed to seed the TUI when
// resuming a project that has tasks already completed in previous runs.
type TaskMetric struct {
	TaskID     string
	DurationMs int64
	CostUSD    float64
	TokensIn   int
	TokensOut  int
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
	PerTask        map[string]TaskMetric
	TotalCostUSD   float64
	TotalTokensIn  int
	TotalTokensOut int
}

// Summarize reads the activity ledger and returns a Summary. Missing
// ledger files are not an error — the caller gets an empty Summary,
// matching the case of a fresh project.
func Summarize(workDir, project string) (Summary, error) {
	entries, err := Read(workDir, project)
	if err != nil {
		return Summary{}, err
	}

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
	return s, nil
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

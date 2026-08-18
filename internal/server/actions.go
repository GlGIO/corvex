package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	charmlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/run"
)

// Action is one thing the UI did, recorded as the command that would have done
// the same thing from a terminal.
//
// This is the roadmap's parity rule (2h) made checkable rather than promised:
// every mutating handler writes the CLI equivalent of what it just did, so
// "there is no path that exists only in the UI" can be audited by reading a
// file instead of by reading the source. It also gives the ⌘K panel its content
// for free — the line it shows is the line that was recorded.
type Action struct {
	At      time.Time `json:"at"`
	Command string    `json:"command"`
	// Result is "ok" or the error text. A refused action is worth recording:
	// "the UI tried to approve a gate whose run had died" is exactly the kind of
	// thing that is invisible otherwise.
	Result string `json:"result"`
}

// ActionLog appends actions to `<repo>/.corvex/runs/ui-actions.jsonl`.
//
// It lives under `.corvex/runs/` for the same reason the run record does: a
// command line carries repository paths, and this file is not the user's history
// to publish. It is scratch a supervisor reads, not an artefact a project keeps.
//
// The `*` gitignore that makes that true is ESTABLISHED here, not assumed. The
// first version of this comment said "which F1 gitignores with `*`" — but F1
// writes that file when a RUN RECORD is written, and a UI that has only ever
// approved a gate has no record: the directory existed, the guard did not, and
// the action log was committable. An audit found it. A safety property a
// comment asserts and no code establishes is the most expensive kind of comment.
type ActionLog struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
}

// NewActionLog builds the log for one workspace.
func NewActionLog(workDir string, now func() time.Time) *ActionLog {
	return &ActionLog{dir: filepath.Join(workDir, ".corvex", "runs"), now: now}
}

// Path is the file actions are appended to.
func (l *ActionLog) Path() string { return filepath.Join(l.dir, "ui-actions.jsonl") }

// Record appends one line. It never fails a request: losing the audit line of an
// approval is bad, refusing the approval because the audit line could not be
// written is worse — the run is blocked either way and only one of the two
// leaves the user able to work.
func (l *ActionLog) Record(command string, err error) Action {
	a := Action{At: l.clock(), Command: command, Result: "ok"}
	if err != nil {
		a.Result = err.Error()
	}
	line, merr := json.Marshal(a)
	if merr != nil {
		return a
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if mkErr := os.MkdirAll(l.dir, 0o755); mkErr != nil {
		return a
	}
	if igErr := run.EnsureScratchIgnored(l.dir); igErr != nil {
		// Refusing to record the action would be worse than recording it in a
		// directory that might be committed: the action already happened.
		charmlog.Warn("could not write the scratch gitignore", "dir", l.dir, "err", igErr)
	}
	f, oerr := os.OpenFile(l.Path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if oerr != nil {
		return a
	}
	defer f.Close()
	// One Write of one complete line: the same atomicity argument the global
	// index relies on (F1), and the reason two concurrent handlers cannot
	// interleave half-lines.
	_, _ = f.Write(append(line, '\n'))
	return a
}

// Read returns the recorded actions, newest first. A missing file is not an
// error: nothing has been done yet.
func (l *ActionLog) Read(limit int) ([]Action, error) {
	data, err := os.ReadFile(l.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Action
	dec := json.NewDecoder(newLineReader(data))
	for dec.More() {
		var a Action
		if derr := dec.Decode(&a); derr != nil {
			// A truncated last line is the normal shape of a file being
			// appended to; stop rather than fail the whole read.
			break
		}
		out = append(out, a)
	}
	// Newest first, because the question is "what did I just do".
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (l *ActionLog) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now().UTC()
}

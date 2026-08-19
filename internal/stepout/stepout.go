// Package stepout keeps the last words of a step that failed.
//
// # The bug this exists for
//
// A `kind: tool` / `kind: test` stage runs a shell command, and internal/step
// has always CAPTURED its output — `exec.Cmd.CombinedOutput`, stdout and stderr
// together. On the passing path that output becomes gate evidence and the
// fan-out's work list. On the failing path it went nowhere: the emitted event
// stopped at "command exit did not pass after 1 iteration(s)", the returned
// error added `exit status 3`, and the tool's own message — the one sentence
// naming what was wrong — was dropped on the floor.
//
// Measured on a real recipe: a stage failed because a CLI wanted a flag it had
// not been given, said so on stderr, and the operator's three surfaces
// (`--plain`, `run show --step`, `logs`) showed the same content-free phrase.
// The cause was found by re-running the command by hand. That is a diagnosis a
// tool should not make a person do twice.
//
// # Why a file of its own, and not the ledger
//
// `run show --step` is the canonical detail surface and it reads
// `.corvex/tasks/<project>/activity.jsonl` — which corvex's own auto_commit
// puts into the user's git history. That file is why activity.Entry carries the
// NAME of a tool and never its input: a command line, or the output of one, can
// carry a token the user exported. Command output is the same class of content
// as a command input and must not reach a committed file.
//
// So it lands here instead: `<repo>/.corvex/runs/output/`, under the directory
// whose `*` .gitignore already exists for the run record (which holds a pid and
// an absolute path) and for the gate store (which holds evidence text). Same
// property, inherited rather than re-argued. Mode 0600 rather than the record's
// 0644, because unlike a pid this content is arbitrary and may hold a secret;
// nothing is redacted on the way in, and pretending otherwise with a regex would
// only teach the next reader to trust it.
//
// The file is written ONLY for a step that failed. A passing step's output is
// already evidence, and writing every step's output here would turn a
// diagnostic into a log.
package stepout

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/task"
)

// MaxLines and MaxBytes bound what is kept. Truncation is not optional: a
// failing `go test ./...` prints thousands of lines and a failing HTTP call can
// print one line holding a megabyte of minified JSON.
//
// The tail is kept rather than the head because a command's verdict is its last
// words: `go test` prints FAIL last, a CLI prints its usage error last, a stack
// trace ends at the frame that matters to the caller. A head would reliably
// keep the banner and drop the answer.
const (
	// MaxLines = 20: enough for a test failure block or the tail of a stack
	// trace, and short enough that it fits on screen under the `! S03 failed`
	// line without pushing the run's own summary out of view. Bigger and the
	// terminal surface stops being a summary; smaller and a Go panic's tail
	// does not fit.
	MaxLines = 20

	// MaxBytes = 4096 is the second bound, and it exists because the first one
	// does not hold: line count says nothing about size, and one line of
	// minified JSON or base64 can be megabytes. 4 KiB is about fifty terminal
	// lines' worth of characters, so on normal output MaxLines always binds
	// first and this one is invisible; it only fires for a producer that emits
	// no newlines. It bounds the disk too — one file per failed step, so a
	// run's whole diagnostic footprint is steps × 4 KiB.
	MaxBytes = 4096
)

// Tail is the truncation, applied once by the producer so that every surface
// shows the same bytes.
//
// When anything was dropped the result gains a first line saying exactly how
// much, because a truncated diagnostic that does not admit it is worse than no
// diagnostic: the reader concludes the command said only this. The marker is
// corvex's own annotation and is deliberately not counted against MaxBytes —
// the bound is on the command's output, not on our note about it.
func Tail(out string) string {
	out = strings.TrimRight(out, "\n")
	if strings.TrimSpace(out) == "" {
		return ""
	}
	lines := strings.Split(out, "\n")
	droppedLines := 0
	if len(lines) > MaxLines {
		droppedLines = len(lines) - MaxLines
		lines = lines[droppedLines:]
	}
	tail := strings.Join(lines, "\n")
	trimmedBytes := 0
	if len(tail) > MaxBytes {
		trimmedBytes = len(tail) - MaxBytes
		tail = tail[trimmedBytes:]
		// Never start the tail inside a rune: a cut mid-sequence renders as a
		// replacement character and makes the first line look corrupted.
		for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
			tail = tail[1:]
			trimmedBytes++
		}
	}
	var notes []string
	if droppedLines > 0 {
		notes = append(notes, fmt.Sprintf("%d earlier line(s) dropped", droppedLines))
	}
	if trimmedBytes > 0 {
		notes = append(notes, fmt.Sprintf("%d earlier byte(s) trimmed", trimmedBytes))
	}
	if len(notes) > 0 {
		tail = "[" + strings.Join(notes, "; ") + "]\n" + tail
	}
	return tail
}

// Dir is where a repository's step output lives: `<repo>/.corvex/runs/output`.
//
// Under RecordsDir for the same reason internal/gate is: the `*` .gitignore
// written there covers subdirectories, so this content inherits the one
// property it cannot do without.
func Dir(repo string) string {
	return filepath.Join(run.RecordsDir(repo), "output")
}

// Path is the file holding one step's output for one run.
//
// The step id is percent-escaped, like the gate store's, because fan-out mints
// ids containing `/` (`S06/003/apply`) and because an id is not a path
// component until something makes it one. Both ids are validated first: this
// function builds a path from them, and a caller that hands it `..` must get an
// error rather than a write outside Dir.
func Path(repo, runID, stepID string) (string, error) {
	if strings.TrimSpace(repo) == "" {
		return "", fmt.Errorf("step output: empty repo path")
	}
	if !run.ValidID(runID) {
		return "", fmt.Errorf("step output: invalid run id %q", runID)
	}
	if !task.ValidTaskID(stepID) {
		return "", fmt.Errorf("step output: invalid step id %q", stepID)
	}
	return filepath.Join(Dir(repo), runID+"-"+url.PathEscape(stepID)+".txt"), nil
}

// Write stores out for one step, replacing whatever was there.
//
// Replacing rather than refusing (the gate store's O_EXCL) because `run retry
// --step S03` runs the same step of the same run again, and the second failure
// is the one the operator is looking at. A plain create-truncate rather than
// temp-and-rename: this file is written once at the end of a step and never
// updated in place, and a process killed mid-write leaves a shorter diagnostic,
// which is still a diagnostic — unlike a half-written run record, nothing reads
// this to make a decision.
func Write(repo, runID, stepID, out string) error {
	if strings.TrimSpace(out) == "" {
		return nil
	}
	path, err := Path(repo, runID, stepID)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("step output dir %s: %w", dir, err)
	}
	// Belt and braces, exactly as internal/gate does it: the run record's guard
	// normally already exists one level up, but content that may hold a token
	// must not depend on somebody else having written first.
	if err := run.EnsureScratchIgnored(run.RecordsDir(repo)); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(strings.TrimRight(out, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("step output write %s: %w", path, err)
	}
	return nil
}

// Read returns what a step's failure printed, or the empty string when there is
// nothing stored.
//
// A missing file is the normal case, not an error: every step that passed, and
// every step of every run that predates this store, has none. A read error of
// any other kind is also answered with the empty string — a diagnostic that
// cannot be loaded must not take down the screen that was going to show the
// step's status.
func Read(repo, runID, stepID string) string {
	path, err := Path(repo, runID, stepID)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\n")
}

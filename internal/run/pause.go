package run

// Pause: the cross-process control file that halts a run between waves.
//
// # Why a file, and not a signal
//
// corvex has no daemon. The process that asks for a pause is never the process
// that pauses — it is a second `corvex run pause`, or an HTTP handler — so the
// contract that matters is the one on disk, exactly as it is for run identity
// (F1) and for gates (F2). A Go channel is a contract between two goroutines and
// proves nothing about two processes; the orchestrator's Command channel is
// still the right mechanism for the TUI that owns the run, and useless for
// anybody else.
//
// A signal was the other candidate and is worse for a reason this repository
// already measured: a child nobody reaps becomes a zombie, and a zombie answers
// signal 0 (see liveness.go). Hanging "is this run paused" off a signal would
// make the answer depend on process bookkeeping that is already known to lie
// here. The file has no such ambiguity — it is there or it is not.
//
// # Why the file is not named `<run_id>.json`
//
// It sits next to the run record, in the same gitignored scratch directory, but
// with its own extension: ReadRecords reads every `*.json` in RecordsDir, and a
// second JSON file carrying a run_id would show up as a duplicate row in every
// listing on the machine.

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// PauseRequest is what the pausing process leaves behind. It is small on
// purpose: presence is the signal, the content is for a human reading the
// directory. Nothing here describes who asked — corvex is single-user with no
// RBAC, and a username would be exactly the machine-describing value F1 took
// out of the ledger.
type PauseRequest struct {
	RunID    string    `json:"run_id"`
	PausedAt time.Time `json:"paused_at"`
}

// PausePath is the control file of one run: `<repo>/.corvex/runs/<id>.pause`.
func PausePath(repo, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("run pause: invalid run id %q", id)
	}
	if repo == "" {
		return "", fmt.Errorf("run pause: empty repo path")
	}
	recordPath, err := RecordPath(repo, id)
	if err != nil {
		return "", err
	}
	// Derived from the record path so the two can never drift apart: whoever
	// moves one moves the other.
	return recordPath[:len(recordPath)-len(".json")] + ".pause", nil
}

// RequestPause writes the control file atomically.
//
// Atomic for the same reason WriteRecord is: the reader is a live run polling
// this path between waves, and a half-written file must never be the thing it
// reads. Temp-and-rename means it sees the previous state or the new one.
func RequestPause(repo, id string, at time.Time) error {
	path, err := PausePath(repo, id)
	if err != nil {
		return err
	}
	if err := prepareRecordsDir(repo); err != nil {
		return err
	}
	buf, err := json.Marshal(PauseRequest{RunID: id, PausedAt: at.UTC()})
	if err != nil {
		return fmt.Errorf("run pause marshal %s: %w", id, err)
	}
	return writeFileAtomic(path, append(buf, '\n'))
}

// PauseRequested reports whether a pause is standing for this run.
//
// Presence decides, not content. A file that does not parse still counts as
// paused: the alternative is a run that ignores a pause because the JSON was
// truncated by a crash, which fails in the direction that keeps spending money.
// The parsed request is returned when it is readable and left zero when it is
// not.
func PauseRequested(repo, id string) (PauseRequest, bool, error) {
	path, err := PausePath(repo, id)
	if err != nil {
		return PauseRequest{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PauseRequest{}, false, nil
		}
		return PauseRequest{}, false, fmt.Errorf("run pause read %s: %w", path, err)
	}
	var req PauseRequest
	if jerr := json.Unmarshal(data, &req); jerr != nil {
		return PauseRequest{}, true, nil
	}
	return req, true, nil
}

// ClearPause removes the control file. Idempotent: clearing a run that was never
// paused is not an error, because both `run resume` and the run's own teardown
// call it and neither can know whether the other got there first.
func ClearPause(repo, id string) error {
	path, err := PausePath(repo, id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("run pause clear %s: %w", path, err)
	}
	return nil
}

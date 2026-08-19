package ops

import (
	"fmt"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// PauseResult reports what happened to a run's control file, so the caller can
// say it out loud. The JSON shape is the contract the UI reads (F7), same as
// KillResult.
type PauseResult struct {
	RunID string `json:"run_id"`
	Repo  string `json:"repo"`
	// Paused is the state the run is in AFTER the call: true for pause, false
	// for resume.
	Paused bool `json:"paused"`
	// RequestedAt is when the standing pause was asked for. Zero on resume.
	RequestedAt time.Time `json:"requested_at,omitempty"`
}

// PauseRun asks a live run to stop at its next wave barrier.
//
// # What this does not promise
//
// It does not stop the run now. The run reads the control file between waves,
// so a wave already in flight runs to completion first — see
// orchestrator.waitWhilePausedOnDisk for why interrupting a paid-for provider
// call is worse than waiting for it. `run kill` is the verb for "stop now, and
// lose the work".
//
// # Why it refuses a run that is not live
//
// The control file has no owner once the run is gone, and a run id is
// recyclable (internal/run/retention.go): a pause left on a finished run is an
// order waiting for whoever draws that id next. Both the run's own teardown and
// claimID clear such a file, but the cheapest place to not create it is here.
func (l RunLister) PauseRun(runID string) (PauseResult, error) {
	row, err := l.FindRun(runID)
	if err != nil {
		return PauseResult{}, err
	}
	if !row.Live() {
		return PauseResult{}, fmt.Errorf("run %s is %s — there is nothing running to pause", runID, row.Liveness)
	}
	at := l.now()
	if err := run.RequestPause(row.Repo, runID, at); err != nil {
		return PauseResult{}, err
	}
	return PauseResult{RunID: runID, Repo: row.Repo, Paused: true, RequestedAt: at.UTC()}, nil
}

// ResumeRun clears a standing pause.
//
// Liveness is deliberately NOT required here, unlike PauseRun. Resume is the
// only verb that removes a control file a human can see, and refusing to clear
// one because the run behind it died would leave the user staring at a file the
// tool told them it would not touch. Removing an orphan is housekeeping, not a
// state change.
func (l RunLister) ResumeRun(runID string) (PauseResult, error) {
	row, err := l.FindRun(runID)
	if err != nil {
		return PauseResult{}, err
	}
	_, paused, err := run.PauseRequested(row.Repo, runID)
	if err != nil {
		return PauseResult{}, err
	}
	if !paused {
		return PauseResult{}, fmt.Errorf("run %s is not paused", runID)
	}
	if err := run.ClearPause(row.Repo, runID); err != nil {
		return PauseResult{}, err
	}
	return PauseResult{RunID: runID, Repo: row.Repo, Paused: false}, nil
}

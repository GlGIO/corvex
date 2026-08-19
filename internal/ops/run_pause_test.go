package ops

// The verbs, and the two ways they are asked to do something wrong: a run that
// does not exist, and a run that is already over.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

func TestPauseRun_WritesTheControlFileALiveRunWillRead(t *testing.T) {
	lister, repo, id := killFixture(t, 4242, run.StatusRunning, time.Now().UTC())

	res, err := lister.PauseRun(id)
	if err != nil {
		t.Fatalf("PauseRun: %v", err)
	}
	if !res.Paused || res.RunID != id || res.Repo != repo {
		t.Errorf("PauseResult = %+v, want paused true for %s in %s", res, id, repo)
	}
	if res.RequestedAt.IsZero() {
		t.Error("RequestedAt is zero: the UI shows how long a run has been held")
	}
	if _, paused, _ := run.PauseRequested(repo, id); !paused {
		t.Fatal("PauseRun reported success without leaving a control file")
	}

	res, err = lister.ResumeRun(id)
	if err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}
	if res.Paused {
		t.Error("PauseResult.Paused is true after a resume")
	}
	if _, paused, _ := run.PauseRequested(repo, id); paused {
		t.Error("the control file survived the resume")
	}
}

// A run that does not exist gets the index's own error, not a bespoke one:
// `pause` addresses runs exactly like `show`, `kill` and the rest.
func TestPauseRun_UnknownRun(t *testing.T) {
	lister, _, _ := killFixture(t, 4242, run.StatusRunning, time.Now().UTC())

	var unknown *UnknownRunError
	if _, err := lister.PauseRun("run_0000"); !errors.As(err, &unknown) {
		t.Fatalf("PauseRun on an unknown id: err = %v, want *UnknownRunError", err)
	}
	if _, err := lister.ResumeRun("run_0000"); !errors.As(err, &unknown) {
		t.Fatalf("ResumeRun on an unknown id: err = %v, want *UnknownRunError", err)
	}
}

// The orphan case, refused at the cheapest possible place. A control file on a
// finished run has no owner, and the id is recyclable: the next run wearing it
// would read a stop order written for somebody else.
func TestPauseRun_RefusesAFinishedRunAndLeavesNoFile(t *testing.T) {
	lister, repo, id := killFixture(t, 4242, run.StatusDone, time.Now().UTC())

	if _, err := lister.PauseRun(id); err == nil {
		t.Fatal("PauseRun on a finished run returned nil")
	}
	if _, paused, _ := run.PauseRequested(repo, id); paused {
		t.Error("a refused pause still left a control file behind for the next run to inherit")
	}
}

// Resume is deliberately NOT symmetric on liveness: it is the only verb that
// removes a file a human can see, and refusing to clear an orphan because the
// run behind it died would leave the user staring at a file the tool said it
// would not touch.
func TestResumeRun_ClearsAnOrphanLeftOnAFinishedRun(t *testing.T) {
	lister, repo, id := killFixture(t, 4242, run.StatusDone, time.Now().UTC())
	if err := run.RequestPause(repo, id, time.Now()); err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if _, err := lister.ResumeRun(id); err != nil {
		t.Fatalf("ResumeRun on an orphan: %v", err)
	}
	if _, paused, _ := run.PauseRequested(repo, id); paused {
		t.Error("the orphan survived the resume")
	}
}

// Resuming a run nobody paused says so, rather than reporting a success that
// changed nothing — the same reason ParseRunStatus refuses a misspelled status
// instead of matching nothing.
func TestResumeRun_RefusesARunThatIsNotPaused(t *testing.T) {
	lister, _, id := killFixture(t, 4242, run.StatusRunning, time.Now().UTC())
	if _, err := lister.ResumeRun(id); err == nil {
		t.Fatal("ResumeRun on a run that was never paused returned nil")
	}
}

// The status axis has to admit the word, or `run list --status paused` refuses
// the very state this feature produces.
func TestParseRunStatus_AcceptsPaused(t *testing.T) {
	got, err := ParseRunStatus("paused")
	if err != nil {
		t.Fatalf("ParseRunStatus(paused): %v", err)
	}
	if got != run.StatusPaused {
		t.Errorf("ParseRunStatus(paused) = %q", got)
	}
	if _, err := ParseRunStatus("pausd"); err == nil {
		t.Error("ParseRunStatus accepted a misspelling")
	}
}

// A paused run stays visible on BOTH axes, and the two say different things:
// the process is up (liveness alive, so --live keeps it) and the run reports
// that it is doing nothing on purpose (status paused). A row that only said
// `alive` would be true and useless.
func TestListRuns_PausedRunIsVisibleOnBothAxes(t *testing.T) {
	lister, _, id := killFixture(t, 4242, run.StatusPaused, time.Now().UTC())

	rows, err := lister.ListRuns(RunListOptions{Status: run.StatusPaused})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(rows) != 1 || rows[0].RunID != id {
		t.Fatalf("--status paused returned %+v, want the paused run %s", rows, id)
	}
	if rows[0].Liveness != run.LivenessAlive {
		t.Errorf("liveness = %q, want alive: the process really is up and beating", rows[0].Liveness)
	}
	if !rows[0].Live() {
		t.Error("a paused run dropped out of --live: it still holds a process")
	}

	// Negative control: it must not answer to somebody asking for `running`.
	rows, err = lister.ListRuns(RunListOptions{Status: run.StatusRunning})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("--status running returned the paused run: %+v", rows)
	}
}

// A run that ends while paused must not leave its control file behind. The id is
// recyclable, so the orphan would be a stop order waiting for whoever draws that
// id next — and the panic path is where a cleanup is normally forgotten.
func TestExecuteClearsThePauseControlFileOnEveryPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func(ctxDone <-chan struct{}) error
		pan  bool
	}{
		{name: "clean exit", body: func(<-chan struct{}) error { return nil }},
		{name: "failure", body: func(<-chan struct{}) error { return errBodyFailed }},
		{name: "panic", pan: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			r, err := NewRunner(identityRequest(t, repo, "alpha"))
			if err != nil {
				t.Fatalf("NewRunner: %v", err)
			}
			if err := run.RequestPause(repo, r.RunID, time.Now()); err != nil {
				t.Fatalf("RequestPause: %v", err)
			}

			func() {
				defer func() { _ = recover() }()
				_ = r.Execute(context.Background(), func(context.Context) error {
					if tc.pan {
						panic("worker exploded")
					}
					return tc.body(nil)
				})
			}()

			if _, paused, _ := run.PauseRequested(repo, r.RunID); paused {
				t.Errorf("the pause control file outlived the run (%s path)", tc.name)
			}
		})
	}
}

var errBodyFailed = errors.New("body failed")

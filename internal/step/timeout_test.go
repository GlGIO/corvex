package step

import (
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/config"
)

// The per-step wall clock (dogfood). The run-wide ceiling cannot be right for
// every step — the first real run of this repository was killed at 20 minutes
// while the worker was still working, and a step that runs a test suite has a
// different profile from one that edits a package.
func TestHardCeiling_StepBeatsTheRunAndNonsenseFallsBack(t *testing.T) {
	e := &Executor{cfg: &config.Config{}}
	e.cfg.Execution.TaskTimeoutMinutes = 20

	if got := e.hardCeiling(""); got != 20*time.Minute {
		t.Errorf("no declaration should inherit the run's 20m, got %s", got)
	}
	if got := e.hardCeiling("45m"); got != 45*time.Minute {
		t.Errorf("the step's own ceiling was ignored: got %s", got)
	}
	if got := e.hardCeiling("2h"); got != 2*time.Hour {
		t.Errorf("hours are a duration too: got %s", got)
	}
	// A ceiling nobody can read must degrade to the default, never to infinity:
	// "no ceiling" is how a hung provider holds a run forever.
	for _, bad := range []string{"soon", "45", "-5m", "0"} {
		if got := e.hardCeiling(bad); got != 20*time.Minute {
			t.Errorf("hardCeiling(%q) = %s, want the run's 20m", bad, got)
		}
	}
}

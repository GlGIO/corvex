package step

import (
	"context"
	"os"
	"os/exec"
	"strings"
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

// $CORVEX_RUN_BASE: the anchor a recipe can trust.
//
// The dogfood that produced this: a gate required reading `git show --stat HEAD`
// and, because auto_commit checkpoints every step, HEAD at gate time was a
// bookkeeping commit — the approver was asked to acknowledge a diff that did
// not contain the change. An anchor that moves is worse than no anchor, because
// the lock still closes.
func TestRunShell_ExposesTheRunBaseToEvidenceCommands(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t.com"}, {"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	head := gitHead(t, repo)

	e := &Executor{workDir: repo, cfg: &config.Config{}}
	r := &Run{Identity: RunIdentity{RunID: "run_base01"}}
	out, err := e.runShell(context.Background(), r, "echo $CORVEX_RUN_BASE")
	if err != nil {
		t.Fatalf("runShell: %v", err)
	}
	if strings.TrimSpace(out) != head {
		t.Fatalf("CORVEX_RUN_BASE = %q, want the run's starting commit %q", strings.TrimSpace(out), head)
	}

	// It must not move when the run commits: that is the entire point.
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", "corvex: checkpoint S01")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("checkpoint: %s: %v", out, cerr)
	}
	if gitHead(t, repo) == head {
		t.Fatal("the fixture did not actually move HEAD")
	}

	out, err = e.runShell(context.Background(), r, "echo $CORVEX_RUN_BASE")
	if err != nil {
		t.Fatalf("runShell: %v", err)
	}
	if strings.TrimSpace(out) != head {
		t.Errorf("the anchor drifted with HEAD: got %q, want %q", strings.TrimSpace(out), head)
	}
}

func gitHead(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

package cmd

import (
	"os"
	"os/exec"
	"testing"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "t@t.com"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
}

func TestCheckWorktreeMismatch(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	// conventional worktree dir for project "feat1"
	wt := root + "-feat1"
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	// from the main repo with a worktree present → error
	if err := checkWorktreeMismatch(root, "feat1", "plan", false); err == nil {
		t.Error("expected mismatch error when a worktree exists and running from main")
	}
	// --here overrides
	if err := checkWorktreeMismatch(root, "feat1", "plan", true); err != nil {
		t.Errorf("--here should bypass the guard, got %v", err)
	}
	// no worktree for this project → nil
	if err := checkWorktreeMismatch(root, "other", "plan", false); err != nil {
		t.Errorf("no worktree should be allowed, got %v", err)
	}
}

package ops

// Unit tests for the worktree operations. They lived in cmd/start_test.go while
// setupWorktree/linkWorktreePaths were still cmd functions; they moved down with
// the code they exercise, assertions unchanged. gitInit is duplicated from
// cmd/start_test.go on purpose: Go test helpers do not cross package
// boundaries, and cmd/characterize_test.go still needs the copy over there.

import (
	"os"
	"os/exec"
	"path/filepath"
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

// When .corvex is tracked, the worktree checkout already has it; setupWorktree
// must not fail trying to symlink over it.
func TestSetupWorktree_TrackedCorvexNoSymlink(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	if err := os.MkdirAll(filepath.Join(root, ".corvex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".corvex", "config.yaml"), []byte("project:\n  name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	add := exec.Command("git", "add", "-A")
	add.Dir = root
	add.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %v", out, err)
	}
	commit := exec.Command("git", "commit", "-m", "track corvex")
	commit.Dir = root
	commit.Env = add.Env
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %v", out, err)
	}

	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := SetupWorktree(root, wt, "feat1", "HEAD", os.Stdout, os.Stderr); err != nil {
		t.Fatalf("setupWorktree() error = %v, want nil for tracked .corvex", err)
	}
	info, err := os.Lstat(filepath.Join(wt, ".corvex"))
	if err != nil {
		t.Fatalf(".corvex missing in worktree: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("tracked .corvex should be the real checked-out dir, not a symlink")
	}
}

// When .corvex is NOT tracked, setupWorktree symlinks the main repo's .corvex
// into the worktree.
func TestSetupWorktree_UntrackedCorvexSymlinks(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	// .corvex exists in the main repo but is not committed (gitignored model).
	if err := os.MkdirAll(filepath.Join(root, ".corvex"), 0o755); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := SetupWorktree(root, wt, "feat2", "HEAD", os.Stdout, os.Stderr); err != nil {
		t.Fatalf("setupWorktree() error = %v", err)
	}
	info, err := os.Lstat(filepath.Join(wt, ".corvex"))
	if err != nil {
		t.Fatalf(".corvex missing in worktree: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("untracked .corvex should be symlinked into the worktree")
	}
}

func TestLinkWorktreePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.env"), []byte("X=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	// pre-existing dest must not be clobbered
	if err := os.WriteFile(filepath.Join(wt, "secret.env"), []byte("OWN"), 0o644); err != nil {
		t.Fatal(err)
	}

	LinkWorktreePaths(root, wt, []string{"node_modules", "secret.env", "missing.txt"})

	// node_modules → symlinked
	if fi, err := os.Lstat(filepath.Join(wt, "node_modules")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("node_modules should be a symlink (err=%v)", err)
	}
	// secret.env pre-existed → NOT a symlink (kept the worktree's own)
	fi, err := os.Lstat(filepath.Join(wt, "secret.env"))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("pre-existing secret.env should be kept, not clobbered by a symlink")
	}
	// missing source → skipped
	if _, err := os.Lstat(filepath.Join(wt, "missing.txt")); err == nil {
		t.Error("missing source should not produce a link")
	}
}

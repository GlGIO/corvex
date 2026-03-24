package recovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %s\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func initGitRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.email", "test@corvex.dev")
	gitExec(t, dir, "config", "user.name", "corvex-test")

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	gitExec(t, dir, "add", "-A")
	gitExec(t, dir, "commit", "-m", "initial")

	return dir
}

func TestActionString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action Action
		want   string
	}{
		{Continue, "continue"},
		{RetryTask, "retry"},
		{Action(99), "Action(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := tt.action.String()
			if got != tt.want {
				t.Errorf("Action(%d).String() = %q, want %q", int(tt.action), got, tt.want)
			}
		})
	}
}

func TestCheckCleanRepo(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	result, err := mgr.Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if result.Action != Continue {
		t.Errorf("Action = %v, want Continue", result.Action)
	}
	if len(result.DirtyFiles) != 0 {
		t.Errorf("DirtyFiles = %v, want empty", result.DirtyFiles)
	}
}

func TestCheckDirtyTrackedFile(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if result.Action != RetryTask {
		t.Errorf("Action = %v, want RetryTask", result.Action)
	}

	found := false
	for _, f := range result.DirtyFiles {
		if f == "README.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DirtyFiles = %v, want to contain README.md", result.DirtyFiles)
	}

	content, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "initial" {
		t.Errorf("file content after Check = %q, want %q (should be reverted)", string(content), "initial")
	}
}

func TestCheckUntrackedFile(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	newFile := filepath.Join(dir, "untracked.txt")
	if err := os.WriteFile(newFile, []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if result.Action != RetryTask {
		t.Errorf("Action = %v, want RetryTask", result.Action)
	}

	found := false
	for _, f := range result.DirtyFiles {
		if f == "untracked.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DirtyFiles = %v, want to contain untracked.txt", result.DirtyFiles)
	}

	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Error("untracked file should be removed after Check, but still exists")
	}
}

func TestCheckDirtyTrackedAndUntracked(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if result.Action != RetryTask {
		t.Errorf("Action = %v, want RetryTask", result.Action)
	}
	if len(result.DirtyFiles) < 2 {
		t.Errorf("DirtyFiles = %v, want at least 2 files", result.DirtyFiles)
	}
}

func TestCheckNotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	_, err := mgr.Check()
	if err == nil {
		t.Fatal("Check() on non-git dir should return error")
	}
}

func TestMarkCheckpointCreatesCommit(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	newFile := filepath.Join(dir, "feature.go")
	if err := os.WriteFile(newFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := mgr.MarkCheckpoint("S01"); err != nil {
		t.Fatalf("MarkCheckpoint() error = %v", err)
	}

	out := gitExec(t, dir, "log", "--oneline", "-1")
	if !strings.Contains(out, "corvex: checkpoint S01") {
		t.Errorf("latest commit = %q, want to contain %q", out, "corvex: checkpoint S01")
	}

	statusOut := gitExec(t, dir, "status", "--porcelain")
	if strings.TrimSpace(statusOut) != "" {
		t.Errorf("repo still dirty after checkpoint: %s", statusOut)
	}
}

func TestMarkCheckpointNoChanges(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	beforeLog := gitExec(t, dir, "log", "--oneline")

	if err := mgr.MarkCheckpoint("S01"); err != nil {
		t.Fatalf("MarkCheckpoint() error = %v", err)
	}

	afterLog := gitExec(t, dir, "log", "--oneline")
	if beforeLog != afterLog {
		t.Errorf("commit log changed after no-op checkpoint:\nbefore: %s\nafter: %s", beforeLog, afterLog)
	}
}

func TestMarkCheckpointNotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	if err := mgr.MarkCheckpoint("S01"); err == nil {
		t.Fatal("MarkCheckpoint() on non-git dir should return error")
	}
}

func TestMarkCheckpointMultipleFiles(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := mgr.MarkCheckpoint("S03"); err != nil {
		t.Fatalf("MarkCheckpoint() error = %v", err)
	}

	out := gitExec(t, dir, "log", "--oneline", "-1")
	if !strings.Contains(out, "corvex: checkpoint S03") {
		t.Errorf("latest commit = %q, want %q", out, "corvex: checkpoint S03")
	}

	statusOut := gitExec(t, dir, "status", "--porcelain")
	if strings.TrimSpace(statusOut) != "" {
		t.Errorf("repo still dirty: %s", statusOut)
	}
}

func TestNewManager(t *testing.T) {
	t.Parallel()
	mgr := NewManager("/some/path")
	if mgr.WorkDir != "/some/path" {
		t.Errorf("WorkDir = %q, want %q", mgr.WorkDir, "/some/path")
	}
}

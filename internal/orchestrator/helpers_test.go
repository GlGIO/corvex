package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s: %v", args[1:], out, err)
		}
	}
}

func setupProject(t *testing.T, dir, project, taskContent string) string {
	t.Helper()
	tasksDir := filepath.Join(dir, ".corvex", "tasks", project)
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tasksPath := filepath.Join(tasksDir, "tasks.md")
	if err := os.WriteFile(tasksPath, []byte(taskContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return tasksPath
}

func gitCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "add", "-A"},
		{"git", "commit", "-m", msg, "--allow-empty"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args[1:], out, err)
		}
	}
}

func hashFileContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func containsEvent(events []EventType, target EventType) bool {
	for _, e := range events {
		if e == target {
			return true
		}
	}
	return false
}

func countEvents(events []EventType, target EventType) int {
	count := 0
	for _, e := range events {
		if e == target {
			count++
		}
	}
	return count
}

func firstEventIndex(events []EventType, target EventType) int {
	for i, e := range events {
		if e == target {
			return i
		}
	}
	return -1
}

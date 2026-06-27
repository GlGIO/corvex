package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupStatusTestProject(t *testing.T) (tmpDir string, cleanup func()) {
	t.Helper()
	tmpDir = t.TempDir()

	taskDir := filepath.Join(tmpDir, ".corvex", "tasks", "test-project")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("creating task dir: %v", err)
	}

	tasksContent := "---\ngenerated_by: test\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — Setup Project ✅ PASSED\n\n" +
		"```yaml\ntype: general\n```\n\n" +
		"### O que fazer\nInitialize project\n\n" +
		"### Critérios de sucesso\n- [ ] Project compiles\n\n" +
		"---\n\n" +
		"## S02 — Add Features ⬜ PENDING\n\n" +
		"```yaml\ntype: backend\ndepends_on: [S01]\n```\n\n" +
		"### O que fazer\nAdd features\n\n" +
		"### Critérios de sucesso\n- [ ] Features work\n"

	if err := os.WriteFile(filepath.Join(taskDir, "tasks.md"), []byte(tasksContent), 0o644); err != nil {
		t.Fatalf("writing tasks.md: %v", err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting cwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	return tmpDir, func() { os.Chdir(origDir) }
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stdout = w

	fnErr := fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String(), fnErr
}

func TestStatusShowsTasks(t *testing.T) {
	_, cleanup := setupStatusTestProject(t)
	defer cleanup()

	output, err := captureStdout(t, func() error {
		return runStatus(nil, []string{"test-project"})
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	if !strings.Contains(output, "test-project") {
		t.Error("output should contain project name")
	}

	if !strings.Contains(output, "1/2 done") {
		t.Errorf("output should show 1/2 done, got:\n%s", output)
	}

	if !strings.Contains(output, "S01") {
		t.Error("output should contain S01")
	}

	if !strings.Contains(output, "S02") {
		t.Error("output should contain S02")
	}
}

func TestStatusShowsEmoji(t *testing.T) {
	_, cleanup := setupStatusTestProject(t)
	defer cleanup()

	output, err := captureStdout(t, func() error {
		return runStatus(nil, []string{"test-project"})
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	if !strings.Contains(output, "✅") {
		t.Error("output should contain ✅ for passed task")
	}

	if !strings.Contains(output, "⬜") {
		t.Error("output should contain ⬜ for pending task")
	}
}

func TestStatusShowsDependencies(t *testing.T) {
	_, cleanup := setupStatusTestProject(t)
	defer cleanup()

	output, err := captureStdout(t, func() error {
		return runStatus(nil, []string{"test-project"})
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	if !strings.Contains(output, "S01") {
		t.Error("output should show dependency info")
	}
}

func TestStatusMissingProject(t *testing.T) {
	tmpDir := t.TempDir()
	corvexDir := filepath.Join(tmpDir, ".corvex", "tasks")
	os.MkdirAll(corvexDir, 0o755)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	_, err := captureStdout(t, func() error {
		return runStatus(nil, []string{"nonexistent"})
	})
	if err == nil {
		t.Error("expected error for missing project")
	}
}

func TestStatusJSON(t *testing.T) {
	_, cleanup := setupStatusTestProject(t)
	defer cleanup()

	b := true
	statusJSON = &b
	defer func() { f := false; statusJSON = &f }()

	output, err := captureStdout(t, func() error {
		return runStatus(nil, []string{"test-project"})
	})
	if err != nil {
		t.Fatalf("runStatus --json failed: %v", err)
	}

	var out statusOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}

	if out.Project != "test-project" {
		t.Errorf("project: got %q, want %q", out.Project, "test-project")
	}
	if out.Total != 2 {
		t.Errorf("total: got %d, want 2", out.Total)
	}
	if out.Passed != 1 {
		t.Errorf("passed: got %d, want 1", out.Passed)
	}
	if out.Failed != 0 {
		t.Errorf("failed: got %d, want 0", out.Failed)
	}
	if out.Pending != 1 {
		t.Errorf("pending: got %d, want 1", out.Pending)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("tasks: got %d, want 2", len(out.Tasks))
	}

	byID := make(map[string]statusTask, len(out.Tasks))
	for _, task := range out.Tasks {
		byID[task.ID] = task
	}

	s01, ok := byID["S01"]
	if !ok {
		t.Fatal("S01 missing from tasks")
	}
	if s01.Status != "PASSED" {
		t.Errorf("S01 status: got %q, want PASSED", s01.Status)
	}
	if s01.DependsOn == nil {
		t.Error("S01 dependsOn should be [] not null")
	}

	s02, ok := byID["S02"]
	if !ok {
		t.Fatal("S02 missing from tasks")
	}
	if s02.Status != "PENDING" {
		t.Errorf("S02 status: got %q, want PENDING", s02.Status)
	}
	if len(s02.DependsOn) != 1 || s02.DependsOn[0] != "S01" {
		t.Errorf("S02 dependsOn: got %v, want [S01]", s02.DependsOn)
	}
}

func TestStatusJSONHumanUnchanged(t *testing.T) {
	_, cleanup := setupStatusTestProject(t)
	defer cleanup()

	f := false
	statusJSON = &f

	output, err := captureStdout(t, func() error {
		return runStatus(nil, []string{"test-project"})
	})
	if err != nil {
		t.Fatalf("runStatus failed: %v", err)
	}

	if len(output) > 0 && output[0] == '{' {
		t.Error("human output should not start with '{' (looks like JSON)")
	}
	if !strings.Contains(output, "test-project") {
		t.Error("human output should contain project name")
	}
}

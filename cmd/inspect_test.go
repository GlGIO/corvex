package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/ops"
)

func setupInspectTestProject(t *testing.T) (tmpDir string, cleanup func()) {
	t.Helper()
	tmpDir = t.TempDir()

	taskDir := filepath.Join(tmpDir, ".corvex", "tasks", "test-project")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("creating task dir: %v", err)
	}

	tasksContent := "---\ngenerated_by: test\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — Setup ✅ PASSED\n\n" +
		"```yaml\ntype: general\n```\n\n" +
		"### O que fazer\nInitialize\n\n" +
		"### Critérios de sucesso\n- [ ] Done\n\n" +
		"---\n\n" +
		"## S02 — Features ⬜ PENDING\n\n" +
		"```yaml\ntype: backend\ndepends_on: [S01]\n```\n\n" +
		"### O que fazer\nAdd features\n\n" +
		"### Critérios de sucesso\n- [ ] Works\n"

	if err := os.WriteFile(filepath.Join(taskDir, "tasks.md"), []byte(tasksContent), 0o644); err != nil {
		t.Fatalf("writing tasks.md: %v", err)
	}

	ledger, err := activity.New(tmpDir, "test-project")
	if err != nil {
		t.Fatalf("creating ledger: %v", err)
	}
	// S01: one retry then successful completion
	if err := ledger.Append(activity.Entry{Timestamp: time.Now(), Type: "retry", TaskID: "S01"}); err != nil {
		t.Fatalf("appending retry: %v", err)
	}
	if err := ledger.Append(activity.Entry{
		Timestamp:  time.Now(),
		Type:       "task_complete",
		TaskID:     "S01",
		Status:     "PASSED",
		DurationMs: 12000,
		CostUSD:    0.25,
		TokensIn:   1000,
		TokensOut:  500,
	}); err != nil {
		t.Fatalf("appending task_complete: %v", err)
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

func TestInspectJSONShape(t *testing.T) {
	_, cleanup := setupInspectTestProject(t)
	defer cleanup()

	b := true
	inspectJSON = &b
	inspectTask = ""
	defer func() { f := false; inspectJSON = &f }()

	output, err := captureStdout(t, func() error {
		return runInspect(nil, []string{"test-project"})
	})
	if err != nil {
		t.Fatalf("runInspect --json failed: %v", err)
	}

	var out ops.InspectReport
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}

	if out.Project != "test-project" {
		t.Errorf("project: got %q, want %q", out.Project, "test-project")
	}
	if out.Total != 2 {
		t.Errorf("total: got %d, want 2", out.Total)
	}
	if out.Completed != 1 {
		t.Errorf("completed: got %d, want 1", out.Completed)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("tasks: got %d, want 2", len(out.Tasks))
	}
}

func TestInspectJSONTaskMetrics(t *testing.T) {
	_, cleanup := setupInspectTestProject(t)
	defer cleanup()

	b := true
	inspectJSON = &b
	inspectTask = ""
	defer func() { f := false; inspectJSON = &f }()

	output, err := captureStdout(t, func() error {
		return runInspect(nil, []string{"test-project"})
	})
	if err != nil {
		t.Fatalf("runInspect --json failed: %v", err)
	}

	var out ops.InspectReport
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	byID := make(map[string]ops.InspectTaskStat, len(out.Tasks))
	for _, s := range out.Tasks {
		byID[s.ID] = s
	}

	s01, ok := byID["S01"]
	if !ok {
		t.Fatal("S01 missing from tasks")
	}
	if s01.Status != "PASSED" {
		t.Errorf("S01 status: got %q, want PASSED", s01.Status)
	}
	if s01.DurationMs != 12000 {
		t.Errorf("S01 durationMs: got %d, want 12000", s01.DurationMs)
	}
	if s01.Retries != 1 {
		t.Errorf("S01 retries: got %d, want 1", s01.Retries)
	}
	if s01.CostUSD != 0.25 {
		t.Errorf("S01 costUSD: got %f, want 0.25", s01.CostUSD)
	}
	if s01.TokensIn != 1000 {
		t.Errorf("S01 tokensIn: got %d, want 1000", s01.TokensIn)
	}
	if s01.TokensOut != 500 {
		t.Errorf("S01 tokensOut: got %d, want 500", s01.TokensOut)
	}

	s02, ok := byID["S02"]
	if !ok {
		t.Fatal("S02 missing from tasks")
	}
	if s02.Status != "PENDING" {
		t.Errorf("S02 status: got %q, want PENDING", s02.Status)
	}
	if s02.DurationMs != 0 {
		t.Errorf("S02 durationMs: got %d, want 0 (no activity)", s02.DurationMs)
	}
	if s02.Retries != 0 {
		t.Errorf("S02 retries: got %d, want 0", s02.Retries)
	}
}

func TestInspectJSONTotalCost(t *testing.T) {
	_, cleanup := setupInspectTestProject(t)
	defer cleanup()

	b := true
	inspectJSON = &b
	inspectTask = ""
	defer func() { f := false; inspectJSON = &f }()

	output, err := captureStdout(t, func() error {
		return runInspect(nil, []string{"test-project"})
	})
	if err != nil {
		t.Fatalf("runInspect --json failed: %v", err)
	}

	var out ops.InspectReport
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if out.TotalCostUSD != 0.25 {
		t.Errorf("totalCostUSD: got %f, want 0.25", out.TotalCostUSD)
	}
}

func TestInspectJSONHumanUnchanged(t *testing.T) {
	_, cleanup := setupInspectTestProject(t)
	defer cleanup()

	f := false
	inspectJSON = &f
	inspectTask = ""

	output, err := captureStdout(t, func() error {
		return runInspect(nil, []string{"test-project"})
	})
	if err != nil {
		t.Fatalf("runInspect human failed: %v", err)
	}

	if len(output) > 0 && output[0] == '{' {
		t.Error("human output should not start with '{' (looks like JSON)")
	}
	if !strings.Contains(output, "test-project") {
		t.Error("human output should contain project name")
	}
	if !strings.Contains(output, "S01") {
		t.Error("human output should contain S01")
	}
	if !strings.Contains(output, "S02") {
		t.Error("human output should contain S02")
	}
}

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
)

// setupCombinedFixture creates a tempdir with a "fixture" project that has
// spec.md, tasks.md (S01 PASSED, S02 PENDING), an activity ledger for inspect,
// and a config.yaml for doctor. All four --json commands can run against it.
func setupCombinedFixture(t *testing.T) (tmpDir string, cleanup func()) {
	t.Helper()
	tmpDir = t.TempDir()

	corvexDir := filepath.Join(tmpDir, ".corvex")
	taskDir := filepath.Join(corvexDir, "tasks", "fixture")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("creating task dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(taskDir, "spec.md"), []byte("# fixture spec"), 0o644); err != nil {
		t.Fatalf("writing spec.md: %v", err)
	}

	tasksContent := "---\ngenerated_by: test\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — First Task ✅ PASSED\n\n" +
		"```yaml\ntype: general\n```\n\n" +
		"### O que fazer\nFirst task\n\n" +
		"### Critérios de sucesso\n- [ ] Done\n\n---\n\n" +
		"## S02 — Second Task ⬜ PENDING\n\n" +
		"```yaml\ntype: backend\ndepends_on: [S01]\n```\n\n" +
		"### O que fazer\nSecond task\n\n" +
		"### Critérios de sucesso\n- [ ] Done\n"
	if err := os.WriteFile(filepath.Join(taskDir, "tasks.md"), []byte(tasksContent), 0o644); err != nil {
		t.Fatalf("writing tasks.md: %v", err)
	}

	ledger, err := activity.New(tmpDir, "fixture")
	if err != nil {
		t.Fatalf("creating ledger: %v", err)
	}
	if err := ledger.Append(activity.Entry{Timestamp: time.Now(), Type: "retry", TaskID: "S01"}); err != nil {
		t.Fatalf("appending retry: %v", err)
	}
	if err := ledger.Append(activity.Entry{
		Timestamp:  time.Now(),
		Type:       "task_complete",
		TaskID:     "S01",
		Status:     "PASSED",
		DurationMs: 8000,
		CostUSD:    0.15,
		TokensIn:   600,
		TokensOut:  300,
	}); err != nil {
		t.Fatalf("appending task_complete: %v", err)
	}

	configYAML := `
provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`
	if err := os.WriteFile(filepath.Join(corvexDir, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatalf("writing config.yaml: %v", err)
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

func TestListJSONFixture(t *testing.T) {
	_, cleanup := setupCombinedFixture(t)
	defer cleanup()

	b := true
	listJSON = &b
	defer func() { f := false; listJSON = &f }()

	output, err := captureStdout(t, func() error {
		return runList(nil, nil)
	})
	if err != nil {
		t.Fatalf("runList --json failed: %v", err)
	}

	var projects []listProject
	if err := json.Unmarshal([]byte(output), &projects); err != nil {
		t.Fatalf("invalid JSON: %v\noutput:\n%s", err, output)
	}

	byName := make(map[string]listProject, len(projects))
	for _, p := range projects {
		byName[p.Name] = p
	}

	tests := []struct {
		name     string
		hasSpec  bool
		hasTasks bool
		status   string
	}{
		{"fixture", true, true, "ready"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := byName[tc.name]
			if !ok {
				t.Fatalf("project %q missing from JSON output", tc.name)
			}
			if p.HasSpec != tc.hasSpec {
				t.Errorf("hasSpec: got %v, want %v", p.HasSpec, tc.hasSpec)
			}
			if p.HasTasks != tc.hasTasks {
				t.Errorf("hasTasks: got %v, want %v", p.HasTasks, tc.hasTasks)
			}
			if p.Status != tc.status {
				t.Errorf("status: got %q, want %q", p.Status, tc.status)
			}
		})
	}
}

func TestStatusJSONFixture(t *testing.T) {
	_, cleanup := setupCombinedFixture(t)
	defer cleanup()

	b := true
	statusJSON = &b
	defer func() { f := false; statusJSON = &f }()

	output, err := captureStdout(t, func() error {
		return runStatus(nil, []string{"fixture"})
	})
	if err != nil {
		t.Fatalf("runStatus --json failed: %v", err)
	}

	var out statusOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		t.Fatalf("invalid JSON: %v\noutput:\n%s", err, output)
	}

	if out.Project != "fixture" {
		t.Errorf("project: got %q, want fixture", out.Project)
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

	byID := make(map[string]statusTask, len(out.Tasks))
	for _, task := range out.Tasks {
		byID[task.ID] = task
	}

	tests := []struct {
		id        string
		status    string
		nDepends  int
		dependsOn []string
	}{
		{"S01", "PASSED", 0, []string{}},
		{"S02", "PENDING", 1, []string{"S01"}},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			task, ok := byID[tc.id]
			if !ok {
				t.Fatalf("task %q missing from JSON output", tc.id)
			}
			if task.Status != tc.status {
				t.Errorf("status: got %q, want %q", task.Status, tc.status)
			}
			if task.DependsOn == nil {
				t.Errorf("dependsOn must be [] not null")
			}
			if len(task.DependsOn) != tc.nDepends {
				t.Errorf("dependsOn len: got %d, want %d", len(task.DependsOn), tc.nDepends)
			}
			for i, dep := range tc.dependsOn {
				if i >= len(task.DependsOn) || task.DependsOn[i] != dep {
					t.Errorf("dependsOn[%d]: got %q, want %q", i, task.DependsOn[i], dep)
				}
			}
		})
	}
}

func TestInspectJSONFixture(t *testing.T) {
	_, cleanup := setupCombinedFixture(t)
	defer cleanup()

	b := true
	inspectJSON = &b
	inspectTask = ""
	defer func() { f := false; inspectJSON = &f }()

	output, err := captureStdout(t, func() error {
		return runInspect(nil, []string{"fixture"})
	})
	if err != nil {
		t.Fatalf("runInspect --json failed: %v", err)
	}

	var out inspectOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		t.Fatalf("invalid JSON: %v\noutput:\n%s", err, output)
	}

	if out.Project != "fixture" {
		t.Errorf("project: got %q, want fixture", out.Project)
	}
	if out.Total != 2 {
		t.Errorf("total: got %d, want 2", out.Total)
	}
	if out.Completed != 1 {
		t.Errorf("completed: got %d, want 1", out.Completed)
	}
	if out.TotalCostUSD != 0.15 {
		t.Errorf("totalCostUSD: got %f, want 0.15", out.TotalCostUSD)
	}

	byID := make(map[string]inspectTaskStat, len(out.Tasks))
	for _, s := range out.Tasks {
		byID[s.ID] = s
	}

	tests := []struct {
		id         string
		status     string
		durationMs int64
		retries    int
		costUSD    float64
		tokensIn   int
		tokensOut  int
	}{
		{"S01", "PASSED", 8000, 1, 0.15, 600, 300},
		{"S02", "PENDING", 0, 0, 0, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			s, ok := byID[tc.id]
			if !ok {
				t.Fatalf("task %q missing from JSON output", tc.id)
			}
			if s.Status != tc.status {
				t.Errorf("status: got %q, want %q", s.Status, tc.status)
			}
			if s.DurationMs != tc.durationMs {
				t.Errorf("durationMs: got %d, want %d", s.DurationMs, tc.durationMs)
			}
			if s.Retries != tc.retries {
				t.Errorf("retries: got %d, want %d", s.Retries, tc.retries)
			}
			if s.CostUSD != tc.costUSD {
				t.Errorf("costUSD: got %f, want %f", s.CostUSD, tc.costUSD)
			}
			if s.TokensIn != tc.tokensIn {
				t.Errorf("tokensIn: got %d, want %d", s.TokensIn, tc.tokensIn)
			}
			if s.TokensOut != tc.tokensOut {
				t.Errorf("tokensOut: got %d, want %d", s.TokensOut, tc.tokensOut)
			}
		})
	}
}

func TestDoctorJSONFixture(t *testing.T) {
	tests := []struct {
		name       string
		configYAML string
		claudeBin  string
		wantFailed int
		wantErr    bool
	}{
		{
			name: "valid config passes all checks",
			configYAML: `
provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`,
			claudeBin:  os.Args[0],
			wantFailed: 0,
			wantErr:    false,
		},
		{
			name: "bad provider fails and exits non-zero",
			configYAML: `
provider:
  default: bad-provider
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`,
			wantFailed: 1,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			corvexDir := filepath.Join(tmpDir, ".corvex")
			if err := os.MkdirAll(corvexDir, 0o755); err != nil {
				t.Fatalf("creating .corvex dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(corvexDir, "config.yaml"), []byte(tc.configYAML), 0o644); err != nil {
				t.Fatalf("writing config.yaml: %v", err)
			}

			origDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("getting cwd: %v", err)
			}
			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("chdir: %v", err)
			}
			defer os.Chdir(origDir)

			if tc.claudeBin != "" {
				t.Setenv("CORVEX_CLAUDE_BIN", tc.claudeBin)
			}

			b := true
			doctorJSON = &b
			defer func() { f := false; doctorJSON = &f }()

			output, runErr := captureStdout(t, func() error {
				return runDoctor(nil, nil)
			})

			if tc.wantErr && runErr == nil {
				t.Error("runDoctor --json expected non-nil error but got nil")
			}
			if !tc.wantErr && runErr != nil {
				t.Errorf("runDoctor --json unexpected error: %v", runErr)
			}

			var out doctorOutput
			if err := json.Unmarshal([]byte(output), &out); err != nil {
				t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
			}

			if len(out.Checks) == 0 {
				t.Error("checks array must not be empty")
			}
			for _, c := range out.Checks {
				if c.Name == "" {
					t.Error("check name must not be empty")
				}
				if c.Status != "pass" && c.Status != "warn" && c.Status != "fail" {
					t.Errorf("check %q has unexpected status %q", c.Name, c.Status)
				}
			}
			if out.Passed+out.Warnings+out.Failed != len(out.Checks) {
				t.Errorf("passed+warnings+failed (%d) != len(checks) (%d)",
					out.Passed+out.Warnings+out.Failed, len(out.Checks))
			}
			if out.Failed < tc.wantFailed {
				t.Errorf("failed: got %d, want >= %d", out.Failed, tc.wantFailed)
			}
		})
	}
}

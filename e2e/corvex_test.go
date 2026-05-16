package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

var binaryPath string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "corvex-e2e-bin-*")
	if err != nil {
		panic(err)
	}
	binaryPath = filepath.Join(tmp, "corvex")

	build := exec.Command("go", "build", "-o", binaryPath, "..")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(tmp)
		panic("failed to build corvex binary: " + err.Error())
	}

	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// mockProvider implements provider.Provider for testing.
type mockProvider struct{}

func (m *mockProvider) Execute(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
	return &types.ExecuteResult{
		Output:     "All criteria verified successfully.\n\nVERDICT: PASS",
		ExitCode:   0,
		TokensIn:   100,
		TokensOut:  50,
		CostUSD:    0.01,
		DurationMs: 200,
	}, nil
}

func (m *mockProvider) Stream(_ context.Context, _ types.ExecuteRequest) (<-chan types.StreamEvent, error) {
	ch := make(chan types.StreamEvent, 2)
	go func() {
		ch <- types.StreamEvent{Type: types.EventText, Content: "Working..."}
		ch <- types.StreamEvent{Type: types.EventDone}
		close(ch)
	}()
	return ch, nil
}

func (m *mockProvider) Name() string    { return "mock" }
func (m *mockProvider) Models() []string { return []string{"mock-model"} }

func TestInitCreatesStructure(t *testing.T) {
	dir := t.TempDir()

	cmd := exec.Command(binaryPath, "init")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init failed: %s\n%s", err, out)
	}

	expected := []string{
		".corvex/config.yaml",
		".corvex/agents/default.md",
		".corvex/context/README.md",
	}
	for _, rel := range expected {
		path := filepath.Join(dir, rel)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file not created: %s", rel)
		}
	}

	expectedDirs := []string{
		".corvex/hooks",
		".corvex/tasks",
	}
	for _, rel := range expectedDirs {
		path := filepath.Join(dir, rel)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			t.Errorf("expected directory not created: %s", rel)
		} else if err == nil && !info.IsDir() {
			t.Errorf("expected directory but got file: %s", rel)
		}
	}
}

func TestInitAlreadyExists(t *testing.T) {
	dir := setupInitializedProject(t)

	cmd := exec.Command(binaryPath, "init")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected init to fail when .corvex/ already exists")
	}
	if !strings.Contains(string(out), ".corvex/ already exists") {
		t.Errorf("unexpected error message: %s", out)
	}
}

func TestListShowsProject(t *testing.T) {
	dir := setupInitializedProject(t)
	createTestSpec(t, dir, "testproject")

	cmd := exec.Command(binaryPath, "list")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list failed: %s\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "testproject") {
		t.Errorf("expected output to contain 'testproject', got: %s", output)
	}
	if !strings.Contains(output, "needs planning") {
		t.Errorf("expected output to contain 'needs planning', got: %s", output)
	}
}

func TestStatusShowsTasks(t *testing.T) {
	dir := setupProjectWithTasks(t)

	cmd := exec.Command(binaryPath, "status", "testproject")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %s\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "S01") {
		t.Errorf("expected output to contain 'S01', got: %s", output)
	}
	if !strings.Contains(output, "S02") {
		t.Errorf("expected output to contain 'S02', got: %s", output)
	}
	if !strings.Contains(output, "testproject") {
		t.Errorf("expected output to contain project name, got: %s", output)
	}
}

func TestRunDryRun(t *testing.T) {
	dir := setupProjectWithTasks(t)

	cmd := exec.Command(binaryPath, "run", "testproject", "--dry-run")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %s\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "Dry run") {
		t.Errorf("expected 'Dry run' in output, got: %s", output)
	}
	if !strings.Contains(output, "S01") {
		t.Errorf("expected 'S01' in output, got: %s", output)
	}
	if !strings.Contains(output, "would be executed") {
		t.Errorf("expected 'would be executed' in output, got: %s", output)
	}

	// Verify no task statuses were changed
	tasksPath := filepath.Join(dir, ".corvex", "tasks", "testproject", "tasks.md")
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		t.Fatalf("parsing tasks after dry-run: %s", err)
	}
	for _, tk := range tasks {
		if tk.Status != types.StatusPending {
			t.Errorf("task %s status changed to %s after dry-run", tk.ID, tk.Status)
		}
	}
}

func TestOrchestratorEndToEnd(t *testing.T) {
	dir := setupProjectWithTasks(t)

	cfg := config.Default()
	cfg.Execution.AutoCommit = false

	events := make(chan orchestrator.Event, 128)
	mock := &mockProvider{}

	orc := orchestrator.New(orchestrator.Options{
		Config:   cfg,
		Provider: mock,
		WorkDir:  dir,
		Events:   events,
	})

	go func() {
		for range events {
		}
	}()

	ctx := context.Background()
	if err := orc.Run(ctx, "testproject"); err != nil {
		t.Fatalf("orchestrator run failed: %s", err)
	}

	tasksPath := filepath.Join(dir, ".corvex", "tasks", "testproject", "tasks.md")
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		t.Fatalf("parsing tasks after run: %s", err)
	}

	for _, tk := range tasks {
		if tk.Status != types.StatusPassed {
			t.Errorf("task %s: expected PASSED, got %s", tk.ID, tk.Status)
		}
	}

	anchorPath := filepath.Join(dir, ".corvex", "tasks", "testproject", "anchor.yaml")
	state, err := anchor.Load(anchorPath)
	if err != nil {
		t.Fatalf("loading anchor after run: %s", err)
	}

	if len(state.Completed) != 2 {
		t.Errorf("expected 2 completed entries in anchor, got %d", len(state.Completed))
	}
	if len(state.Completed) > 0 && state.Completed[0].ID != "S01" {
		t.Errorf("expected first completed task to be S01, got %s", state.Completed[0].ID)
	}
}

// --- helpers ---

func setupInitializedProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(binaryPath, "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init failed: %s\n%s", err, out)
	}
	return dir
}

func createTestSpec(t *testing.T, dir, project string) {
	t.Helper()
	pDir := filepath.Join(dir, ".corvex", "tasks", project)
	if err := os.MkdirAll(pDir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := "# Test Project\n\n## Objective\nTest project for e2e validation.\n"
	if err := os.WriteFile(filepath.Join(pDir, "spec.md"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupProjectWithTasks(t *testing.T) string {
	t.Helper()
	dir := setupInitializedProject(t)
	pDir := filepath.Join(dir, ".corvex", "tasks", "testproject")
	if err := os.MkdirAll(pDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tasksContent := `---
generated_by: test
generated_at: "2026-03-23T00:00:00Z"
dag:
  S01: []
  S02: [S01]
---

## S01 — Setup Foundation ⬜ PENDING

` + "```yaml\ntype: general\n```" + `

### O que fazer
1. Create base structure

### Critérios de sucesso
- [ ] Structure created

### Arquivos
- **Criar:** test/setup.go

---

## S02 — Add Feature ⬜ PENDING

` + "```yaml\ntype: general\ndepends_on: [S01]\n```" + `

### O que fazer
1. Add the feature

### Critérios de sucesso
- [ ] Feature working

### Arquivos
- **Criar:** test/feature.go
`

	if err := os.WriteFile(filepath.Join(pDir, "tasks.md"), []byte(tasksContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

package task_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertSliceEqual(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s length = %d, want %d; got=%v want=%v", name, len(got), len(want), got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", name, i, got[i], want[i])
		}
	}
}

func TestParseTasksFile_FullFile(t *testing.T) {
	tasks, dag, err := task.ParseTasksFile("../../testdata/tasks/valid.md")
	if err != nil {
		t.Fatalf("ParseTasksFile() error = %v", err)
	}

	// DAGSpec
	if dag.GeneratedBy != "planner" {
		t.Errorf("DAGSpec.GeneratedBy = %q, want %q", dag.GeneratedBy, "planner")
	}
	if dag.GeneratedAt != "2026-03-22T15:00:00Z" {
		t.Errorf("DAGSpec.GeneratedAt = %q, want %q", dag.GeneratedAt, "2026-03-22T15:00:00Z")
	}
	if len(dag.Dependencies) != 4 {
		t.Fatalf("DAGSpec.Dependencies length = %d, want 4", len(dag.Dependencies))
	}
	if deps := dag.Dependencies["S01"]; len(deps) != 0 {
		t.Errorf("DAGSpec.Dependencies[S01] = %v, want empty slice", deps)
	}
	if deps := dag.Dependencies["S01"]; deps == nil {
		t.Error("DAGSpec.Dependencies[S01] is nil, want non-nil empty slice")
	}
	assertSliceEqual(t, "DAGSpec.Dependencies[S02]", dag.Dependencies["S02"], []string{"S01"})
	assertSliceEqual(t, "DAGSpec.Dependencies[S03]", dag.Dependencies["S03"], []string{"S01"})
	assertSliceEqual(t, "DAGSpec.Dependencies[S04]", dag.Dependencies["S04"], []string{"S02", "S03"})

	// Tasks count
	if len(tasks) != 4 {
		t.Fatalf("got %d tasks, want 4", len(tasks))
	}

	// S01
	s01 := tasks[0]
	if s01.ID != "S01" {
		t.Errorf("tasks[0].ID = %q, want %q", s01.ID, "S01")
	}
	if s01.Title != "Foundation Tables" {
		t.Errorf("tasks[0].Title = %q, want %q", s01.Title, "Foundation Tables")
	}
	if s01.Status != types.StatusPassed {
		t.Errorf("tasks[0].Status = %q, want %q", s01.Status, types.StatusPassed)
	}
	if s01.Type != types.TypeDatabase {
		t.Errorf("tasks[0].Type = %q, want %q", s01.Type, types.TypeDatabase)
	}
	if s01.MaxTurns != 30 {
		t.Errorf("tasks[0].MaxTurns = %d, want 30", s01.MaxTurns)
	}
	if s01.Description == "" {
		t.Error("tasks[0].Description is empty")
	}
	if len(s01.Criteria) != 2 {
		t.Errorf("tasks[0].Criteria length = %d, want 2", len(s01.Criteria))
	}
	assertSliceEqual(t, "tasks[0].Files.Create", s01.Files.Create, []string{
		"backend/src/migrations/001-create-tenants-table.js",
		"backend/src/models/tenant.model.js",
	})
	assertSliceEqual(t, "tasks[0].Files.Modify", s01.Files.Modify, []string{
		"backend/src/models/index.js",
	})

	// S02
	s02 := tasks[1]
	if s02.ID != "S02" {
		t.Errorf("tasks[1].ID = %q, want %q", s02.ID, "S02")
	}
	if s02.Title != "Tenant Context" {
		t.Errorf("tasks[1].Title = %q, want %q", s02.Title, "Tenant Context")
	}
	if s02.Status != types.StatusRunning {
		t.Errorf("tasks[1].Status = %q, want %q", s02.Status, types.StatusRunning)
	}
	if s02.Type != types.TypeBackend {
		t.Errorf("tasks[1].Type = %q, want %q", s02.Type, types.TypeBackend)
	}
	if s02.MaxTurns != 25 {
		t.Errorf("tasks[1].MaxTurns = %d, want 25", s02.MaxTurns)
	}
	assertSliceEqual(t, "tasks[1].DependsOn", s02.DependsOn, []string{"S01"})

	// S03
	s03 := tasks[2]
	if s03.Status != types.StatusPending {
		t.Errorf("tasks[2].Status = %q, want %q", s03.Status, types.StatusPending)
	}
	if s03.Type != types.TypeDatabase {
		t.Errorf("tasks[2].Type = %q, want %q", s03.Type, types.TypeDatabase)
	}
	if s03.MaxTurns != 40 {
		t.Errorf("tasks[2].MaxTurns = %d, want 40", s03.MaxTurns)
	}

	// S04
	s04 := tasks[3]
	if s04.Status != types.StatusFailed {
		t.Errorf("tasks[3].Status = %q, want %q", s04.Status, types.StatusFailed)
	}
	if s04.Type != types.TypeReview {
		t.Errorf("tasks[3].Type = %q, want %q", s04.Type, types.TypeReview)
	}
	assertSliceEqual(t, "tasks[3].DependsOn", s04.DependsOn, []string{"S02", "S03"})
}

func TestParseTasksFile_Minimal(t *testing.T) {
	tasks, dag, err := task.ParseTasksFile("../../testdata/tasks/minimal.md")
	if err != nil {
		t.Fatalf("ParseTasksFile() error = %v", err)
	}

	if dag.GeneratedBy != "" || dag.GeneratedAt != "" || len(dag.Dependencies) != 0 {
		t.Errorf("expected empty DAGSpec, got %+v", dag)
	}

	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}

	tk := tasks[0]
	if tk.ID != "S01" {
		t.Errorf("ID = %q, want %q", tk.ID, "S01")
	}
	if tk.Title != "Setup Project" {
		t.Errorf("Title = %q, want %q", tk.Title, "Setup Project")
	}
	if tk.Status != types.StatusPending {
		t.Errorf("Status = %q, want %q", tk.Status, types.StatusPending)
	}
	if tk.Type != "" {
		t.Errorf("Type = %q, want empty", tk.Type)
	}
	if tk.MaxTurns != 0 {
		t.Errorf("MaxTurns = %d, want 0", tk.MaxTurns)
	}
	if tk.DependsOn != nil {
		t.Errorf("DependsOn = %v, want nil", tk.DependsOn)
	}
}

func TestParseTasksFile_NoFrontmatter(t *testing.T) {
	content := "## S01 — First Task ✅ PASSED\n\n### O que fazer\n1. Do step one\n\n---\n\n## S02 — Second Task ⬜ PENDING\n\n### O que fazer\n1. Do step two\n"
	path := writeTemp(t, content)

	tasks, dag, err := task.ParseTasksFile(path)
	if err != nil {
		t.Fatalf("ParseTasksFile() error = %v", err)
	}

	if dag.GeneratedBy != "" || len(dag.Dependencies) != 0 {
		t.Errorf("expected empty DAGSpec, got %+v", dag)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if tasks[0].ID != "S01" || tasks[0].Status != types.StatusPassed {
		t.Errorf("tasks[0] = {ID:%q Status:%q}, want {S01 PASSED}", tasks[0].ID, tasks[0].Status)
	}
	if tasks[1].ID != "S02" || tasks[1].Status != types.StatusPending {
		t.Errorf("tasks[1] = {ID:%q Status:%q}, want {S02 PENDING}", tasks[1].ID, tasks[1].Status)
	}
}

func TestParseTasksFile_AllStatuses(t *testing.T) {
	content := "## S01 — Pending ⬜ PENDING\n\n### O que fazer\n1. Step\n\n---\n\n" +
		"## S02 — Running 🔄 RUNNING\n\n### O que fazer\n1. Step\n\n---\n\n" +
		"## S03 — Passed ✅ PASSED\n\n### O que fazer\n1. Step\n\n---\n\n" +
		"## S04 — Failed ❌ FAILED\n\n### O que fazer\n1. Step\n\n---\n\n" +
		"## S05 — Skipped ⏭ SKIPPED\n\n### O que fazer\n1. Step\n"
	path := writeTemp(t, content)

	tasks, _, err := task.ParseTasksFile(path)
	if err != nil {
		t.Fatalf("ParseTasksFile() error = %v", err)
	}

	if len(tasks) != 5 {
		t.Fatalf("got %d tasks, want 5", len(tasks))
	}

	expected := []struct {
		id     string
		status types.TaskStatus
	}{
		{"S01", types.StatusPending},
		{"S02", types.StatusRunning},
		{"S03", types.StatusPassed},
		{"S04", types.StatusFailed},
		{"S05", types.StatusSkipped},
	}

	for i, exp := range expected {
		if tasks[i].ID != exp.id {
			t.Errorf("tasks[%d].ID = %q, want %q", i, tasks[i].ID, exp.id)
		}
		if tasks[i].Status != exp.status {
			t.Errorf("tasks[%d].Status = %q, want %q", i, tasks[i].Status, exp.status)
		}
	}
}

func TestParseTasksFile_FileNotFound(t *testing.T) {
	_, _, err := task.ParseTasksFile("/nonexistent/path/tasks.md")
	if err == nil {
		t.Fatal("ParseTasksFile() expected error for missing file, got nil")
	}
}

func TestParseTasksFile_EmptyFile(t *testing.T) {
	path := writeTemp(t, "")

	tasks, dag, err := task.ParseTasksFile(path)
	if err != nil {
		t.Fatalf("ParseTasksFile() error = %v", err)
	}

	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0", len(tasks))
	}
	if dag.GeneratedBy != "" || dag.GeneratedAt != "" || len(dag.Dependencies) != 0 {
		t.Errorf("expected empty DAGSpec, got %+v", dag)
	}
}

// TestParseTasksFile_SkipEmojiVariant verifies the ⏭️ (U+23ED + U+FE0F) variant is handled.
func TestParseTasksFile_SkipEmojiVariant(t *testing.T) {
	content := "## S01 — Skip Task ⏭" + "\uFE0F" + " SKIPPED\n\n### O que fazer\n1. Skipped\n"
	path := writeTemp(t, content)

	tasks, _, err := task.ParseTasksFile(path)
	if err != nil {
		t.Fatalf("ParseTasksFile() error = %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
	if tasks[0].Status != types.StatusSkipped {
		t.Errorf("Status = %q, want %q", tasks[0].Status, types.StatusSkipped)
	}
}

// TestParseTasksFile_CriteriaWithBackticks verifies criteria text preserves inline code.
func TestParseTasksFile_CriteriaWithBackticks(t *testing.T) {
	content := "## S01 — Test ⬜ PENDING\n\n### Critérios de sucesso\n- [ ] `npm test` passes\n- [ ] Coverage above 80%\n"
	path := writeTemp(t, content)

	tasks, _, err := task.ParseTasksFile(path)
	if err != nil {
		t.Fatalf("ParseTasksFile() error = %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}

	want := []string{"`npm test` passes", "Coverage above 80%"}
	assertSliceEqual(t, "Criteria", tasks[0].Criteria, want)
}

func TestParseTasksFile_DashVariants(t *testing.T) {
	tests := []struct {
		name    string
		heading string
	}{
		{"em dash", "## S01 — Title ⬜ PENDING"},
		{"en dash", "## S01 – Title ⬜ PENDING"},
		{"hyphen", "## S01 - Title ⬜ PENDING"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := fmt.Sprintf("%s\n\n### O que fazer\n1. Step\n", tt.heading)
			path := writeTemp(t, content)

			tasks, _, err := task.ParseTasksFile(path)
			if err != nil {
				t.Fatalf("ParseTasksFile() error = %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("got %d tasks, want 1", len(tasks))
			}
			if tasks[0].ID != "S01" {
				t.Errorf("ID = %q, want S01", tasks[0].ID)
			}
		})
	}
}

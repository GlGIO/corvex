package task_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

func assertTasksEqual(t *testing.T, got, want []types.Task) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("task count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.ID != w.ID {
			t.Errorf("task[%d].ID = %q, want %q", i, g.ID, w.ID)
		}
		if g.Title != w.Title {
			t.Errorf("task[%d].Title = %q, want %q", i, g.Title, w.Title)
		}
		if g.Status != w.Status {
			t.Errorf("task[%d].Status = %q, want %q", i, g.Status, w.Status)
		}
		if g.Type != w.Type {
			t.Errorf("task[%d].Type = %q, want %q", i, g.Type, w.Type)
		}
		if g.Description != w.Description {
			t.Errorf("task[%d].Description = %q, want %q", i, g.Description, w.Description)
		}
		assertSliceEqual(t, "task[%d].DependsOn", g.DependsOn, w.DependsOn)
		assertSliceEqual(t, "task[%d].Criteria", g.Criteria, w.Criteria)
		assertSliceEqual(t, "task[%d].Files.Create", g.Files.Create, w.Files.Create)
		assertSliceEqual(t, "task[%d].Files.Modify", g.Files.Modify, w.Files.Modify)
	}
}

func TestWriteTasksFile_RoundTrip(t *testing.T) {
	tasks, dag, err := task.ParseTasksFile("../../testdata/tasks/valid.md")
	if err != nil {
		t.Fatalf("ParseTasksFile() error = %v", err)
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "output.md")
	if err := task.WriteTasksFile(out, tasks, dag); err != nil {
		t.Fatalf("WriteTasksFile() error = %v", err)
	}

	tasks2, dag2, err := task.ParseTasksFile(out)
	if err != nil {
		t.Fatalf("ParseTasksFile() re-read error = %v", err)
	}

	assertTasksEqual(t, tasks2, tasks)

	if dag2.GeneratedBy != dag.GeneratedBy {
		t.Errorf("DAG.GeneratedBy = %q, want %q", dag2.GeneratedBy, dag.GeneratedBy)
	}
	if dag2.GeneratedAt != dag.GeneratedAt {
		t.Errorf("DAG.GeneratedAt = %q, want %q", dag2.GeneratedAt, dag.GeneratedAt)
	}
	if len(dag2.Dependencies) != len(dag.Dependencies) {
		t.Errorf("DAG.Dependencies length = %d, want %d", len(dag2.Dependencies), len(dag.Dependencies))
	}
	for k, v := range dag.Dependencies {
		assertSliceEqual(t, "DAG.Dependencies["+k+"]", dag2.Dependencies[k], v)
	}
}

func TestWriteTasksFile_MinimalRoundTrip(t *testing.T) {
	tasks, dag, err := task.ParseTasksFile("../../testdata/tasks/minimal.md")
	if err != nil {
		t.Fatalf("ParseTasksFile() error = %v", err)
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "output.md")
	if err := task.WriteTasksFile(out, tasks, dag); err != nil {
		t.Fatalf("WriteTasksFile() error = %v", err)
	}

	tasks2, dag2, err := task.ParseTasksFile(out)
	if err != nil {
		t.Fatalf("ParseTasksFile() re-read error = %v", err)
	}

	assertTasksEqual(t, tasks2, tasks)

	if dag2.GeneratedBy != "" || dag2.GeneratedAt != "" || len(dag2.Dependencies) != 0 {
		t.Errorf("expected empty DAGSpec after round-trip, got %+v", dag2)
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/tasks/valid.md")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	if err := task.UpdateTaskStatus(path, "S03", types.StatusRunning); err != nil {
		t.Fatalf("UpdateTaskStatus() error = %v", err)
	}

	tasks, _, err := task.ParseTasksFile(path)
	if err != nil {
		t.Fatalf("ParseTasksFile() after update error = %v", err)
	}

	if len(tasks) != 4 {
		t.Fatalf("got %d tasks, want 4", len(tasks))
	}

	expected := map[string]types.TaskStatus{
		"S01": types.StatusPassed,
		"S02": types.StatusRunning,
		"S03": types.StatusRunning,
		"S04": types.StatusFailed,
	}

	for _, tk := range tasks {
		want, ok := expected[tk.ID]
		if !ok {
			t.Errorf("unexpected task %q", tk.ID)
			continue
		}
		if tk.Status != want {
			t.Errorf("%s.Status = %q, want %q", tk.ID, tk.Status, want)
		}
	}
}

func TestUpdateTaskStatus_NotFound(t *testing.T) {
	data, err := os.ReadFile("../../testdata/tasks/valid.md")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	err = task.UpdateTaskStatus(path, "S99", types.StatusRunning)
	if err == nil {
		t.Fatal("UpdateTaskStatus() expected error for unknown task, got nil")
	}
}

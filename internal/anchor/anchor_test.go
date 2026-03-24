package anchor

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

const fixtureDir = "../../testdata/anchor"

func fixturePath(name string) string {
	return filepath.Join(fixtureDir, name)
}

func TestLoad_existing(t *testing.T) {
	t.Parallel()

	state, err := Load(fixturePath("existing.yaml"))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if state.Project != "smartcare" {
		t.Errorf("Project = %q, want %q", state.Project, "smartcare")
	}
	if state.SpecHash != "a3f8c2e1b9d4f0a7c8e3b2d1f9a4c6e8b0d2f4a6c8e0b2d4f6a8c0e2b4d6f8a0" {
		t.Errorf("SpecHash = %q, want fixture hash", state.SpecHash)
	}
	if state.Intent != "Implementar multitenancy no SmartCare CRM" {
		t.Errorf("Intent = %q, want fixture intent", state.Intent)
	}
	if len(state.Completed) != 1 {
		t.Fatalf("Completed len = %d, want 1", len(state.Completed))
	}
	if state.Completed[0].ID != "S01" {
		t.Errorf("Completed[0].ID = %q, want %q", state.Completed[0].ID, "S01")
	}
	if state.Completed[0].Title != "Foundation Tables" {
		t.Errorf("Completed[0].Title = %q, want %q", state.Completed[0].Title, "Foundation Tables")
	}
	if len(state.Completed[0].FilesCreated) != 1 {
		t.Fatalf("FilesCreated len = %d, want 1", len(state.Completed[0].FilesCreated))
	}
	if state.Completed[0].FilesCreated[0] != "backend/src/models/tenant.model.js" {
		t.Errorf("FilesCreated[0] = %q, want fixture value", state.Completed[0].FilesCreated[0])
	}
	if len(state.Completed[0].FilesModified) != 1 {
		t.Fatalf("FilesModified len = %d, want 1", len(state.Completed[0].FilesModified))
	}
	if state.Completed[0].FilesModified[0] != "backend/src/models/index.js" {
		t.Errorf("FilesModified[0] = %q, want fixture value", state.Completed[0].FilesModified[0])
	}
	if len(state.Completed[0].Decisions) != 1 {
		t.Fatalf("Decisions len = %d, want 1", len(state.Completed[0].Decisions))
	}
	if state.CurrentState.TotalTasks != 18 {
		t.Errorf("TotalTasks = %d, want 18", state.CurrentState.TotalTasks)
	}
	if state.CurrentState.CompletedTasks != 1 {
		t.Errorf("CompletedTasks = %d, want 1", state.CurrentState.CompletedTasks)
	}
	if state.NextTask != "S02" {
		t.Errorf("NextTask = %q, want %q", state.NextTask, "S02")
	}
	if state.NextTaskContext == "" {
		t.Error("NextTaskContext is empty, want non-empty")
	}
}

func TestLoad_missing(t *testing.T) {
	t.Parallel()

	state, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for missing file", err)
	}
	assertEmptyState(t, state, "missing file")
}

func TestLoad_empty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for empty file", err)
	}
	assertEmptyState(t, state, "empty file")
}

func TestLoad_whitespaceOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "whitespace.yaml")
	if err := os.WriteFile(path, []byte("   \n\t\n  "), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for whitespace-only file", err)
	}
	assertEmptyState(t, state, "whitespace-only file")
}

func TestLoad_invalidYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("\x00\x01\x02:::{{[invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want non-nil for invalid YAML")
	}
	if !strings.Contains(err.Error(), "parsing anchor") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "parsing anchor")
	}
}

func TestSave_roundTrip(t *testing.T) {
	t.Parallel()

	original, err := Load(fixturePath("existing.yaml"))
	if err != nil {
		t.Fatalf("Load() fixture error: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "round-trip.yaml")

	if err := Save(path, original); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after save error: %v", err)
	}

	if reloaded.Project != original.Project {
		t.Errorf("Project = %q, want %q", reloaded.Project, original.Project)
	}
	if reloaded.SpecHash != original.SpecHash {
		t.Errorf("SpecHash = %q, want %q", reloaded.SpecHash, original.SpecHash)
	}
	if reloaded.Intent != original.Intent {
		t.Errorf("Intent = %q, want %q", reloaded.Intent, original.Intent)
	}
	if reloaded.NextTask != original.NextTask {
		t.Errorf("NextTask = %q, want %q", reloaded.NextTask, original.NextTask)
	}
	if reloaded.CurrentState != original.CurrentState {
		t.Errorf("CurrentState = %+v, want %+v", reloaded.CurrentState, original.CurrentState)
	}
	if len(reloaded.Completed) != len(original.Completed) {
		t.Fatalf("Completed len = %d, want %d", len(reloaded.Completed), len(original.Completed))
	}
	if reloaded.Completed[0].ID != original.Completed[0].ID {
		t.Errorf("Completed[0].ID = %q, want %q", reloaded.Completed[0].ID, original.Completed[0].ID)
	}
}

func TestSave_createsParentDirs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "anchor.yaml")

	state := types.AnchorState{Project: "test"}
	if err := Save(path, state); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if reloaded.Project != "test" {
		t.Errorf("Project = %q, want %q", reloaded.Project, "test")
	}
}

func TestUpdate_afterTask(t *testing.T) {
	t.Parallel()

	initial, err := Load(fixturePath("existing.yaml"))
	if err != nil {
		t.Fatalf("Load() fixture error: %v", err)
	}

	tr := TaskResult{
		Completed: types.CompletedTask{
			ID:            "S02",
			Title:         "Tenant Middleware",
			Summary:       "AsyncLocalStorage + tenant middleware criados",
			FilesCreated:  []string{"backend/src/middleware/tenant.js"},
			FilesModified: []string{"backend/src/app.js"},
			Decisions:     []string{"AsyncLocalStorage per-request isolation"},
		},
		NextTask:        "S03",
		NextTaskContext: "Middleware de tenant está ativo para todas as rotas.",
		TotalTasks:      18,
	}

	updated := Update(initial, tr)

	if len(updated.Completed) != 2 {
		t.Fatalf("Completed len = %d, want 2", len(updated.Completed))
	}
	if updated.Completed[0].ID != "S01" {
		t.Errorf("Completed[0].ID = %q, want %q", updated.Completed[0].ID, "S01")
	}
	if updated.Completed[1].ID != "S02" {
		t.Errorf("Completed[1].ID = %q, want %q", updated.Completed[1].ID, "S02")
	}
	if updated.CurrentState.CompletedTasks != 2 {
		t.Errorf("CompletedTasks = %d, want 2", updated.CurrentState.CompletedTasks)
	}
	if updated.CurrentState.TotalTasks != 18 {
		t.Errorf("TotalTasks = %d, want 18", updated.CurrentState.TotalTasks)
	}
	if updated.NextTask != "S03" {
		t.Errorf("NextTask = %q, want %q", updated.NextTask, "S03")
	}
	if updated.NextTaskContext != "Middleware de tenant está ativo para todas as rotas." {
		t.Errorf("NextTaskContext = %q, want expected value", updated.NextTaskContext)
	}
	if updated.UpdatedAt == "" {
		t.Error("UpdatedAt is empty after Update")
	}
	if updated.UpdatedAt == initial.UpdatedAt {
		t.Error("UpdatedAt was not refreshed by Update")
	}
}

func TestUpdate_doesNotMutateOriginal(t *testing.T) {
	t.Parallel()

	initial := types.AnchorState{
		Project:  "test",
		NextTask: "S01",
		Completed: []types.CompletedTask{
			{ID: "S00", Title: "Setup"},
		},
		CurrentState: types.CurrentState{TotalTasks: 5, CompletedTasks: 1},
	}

	tr := TaskResult{
		Completed: types.CompletedTask{ID: "S01", Title: "First"},
		NextTask:  "S02",
	}

	_ = Update(initial, tr)

	if len(initial.Completed) != 1 {
		t.Errorf("original Completed was mutated: len = %d, want 1", len(initial.Completed))
	}
	if initial.NextTask != "S01" {
		t.Errorf("original NextTask was mutated: %q, want %q", initial.NextTask, "S01")
	}
}

func TestUpdate_totalTasksZeroPreservesExisting(t *testing.T) {
	t.Parallel()

	initial := types.AnchorState{
		CurrentState: types.CurrentState{TotalTasks: 10},
	}

	tr := TaskResult{
		Completed:  types.CompletedTask{ID: "S01"},
		TotalTasks: 0,
	}

	updated := Update(initial, tr)
	if updated.CurrentState.TotalTasks != 10 {
		t.Errorf("TotalTasks = %d, want 10 (preserved when TotalTasks==0)", updated.CurrentState.TotalTasks)
	}
}

func TestGenerateContext(t *testing.T) {
	t.Parallel()

	state, err := Load(fixturePath("existing.yaml"))
	if err != nil {
		t.Fatalf("Load() fixture error: %v", err)
	}

	tests := []struct {
		name          string
		taskID        string
		wantIntent    bool
		wantCompleted bool
		wantHandoff   bool
	}{
		{
			name:          "matching NextTask includes handoff",
			taskID:        "S02",
			wantIntent:    true,
			wantCompleted: true,
			wantHandoff:   true,
		},
		{
			name:          "mismatched taskID omits handoff",
			taskID:        "S03",
			wantIntent:    true,
			wantCompleted: true,
			wantHandoff:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := GenerateContext(state, tt.taskID)

			if tt.wantIntent {
				if !strings.Contains(ctx, "## Project Intent") {
					t.Error("output missing '## Project Intent'")
				}
				if !strings.Contains(ctx, state.Intent) {
					t.Error("output missing intent text")
				}
			}

			if tt.wantCompleted {
				if !strings.Contains(ctx, "## Completed Work") {
					t.Error("output missing '## Completed Work'")
				}
				if !strings.Contains(ctx, "S01") {
					t.Error("output missing completed task ID S01")
				}
				if !strings.Contains(ctx, "Foundation Tables") {
					t.Error("output missing completed task title")
				}
				if !strings.Contains(ctx, state.Completed[0].Summary) {
					t.Error("output missing completed task summary")
				}
				if !strings.Contains(ctx, "Created: `backend/src/models/tenant.model.js`") {
					t.Error("output missing created file")
				}
				if !strings.Contains(ctx, "Modified: `backend/src/models/index.js`") {
					t.Error("output missing modified file")
				}
				if !strings.Contains(ctx, "slug UNIQUE, modulos JSONB array") {
					t.Error("output missing decision")
				}
			}

			if tt.wantHandoff {
				if !strings.Contains(ctx, "## Handoff Context") {
					t.Error("output missing '## Handoff Context'")
				}
			} else {
				if strings.Contains(ctx, "## Handoff Context") {
					t.Error("output should NOT contain '## Handoff Context'")
				}
			}
		})
	}
}

func TestGenerateContext_emptyState(t *testing.T) {
	t.Parallel()

	ctx := GenerateContext(types.AnchorState{}, "S01")
	if ctx != "" {
		t.Errorf("GenerateContext(empty) = %q, want empty string", ctx)
	}
}

func TestSpecHash(t *testing.T) {
	t.Parallel()

	content := []byte("hello corvex spec\n")
	expected := sha256.Sum256(content)
	expectedHex := hex.EncodeToString(expected[:])

	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := SpecHash(path)
	if err != nil {
		t.Fatalf("SpecHash() error: %v", err)
	}
	if got != expectedHex {
		t.Errorf("SpecHash() = %q, want %q", got, expectedHex)
	}
}

func TestSpecHash_missingFile(t *testing.T) {
	t.Parallel()

	_, err := SpecHash(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("SpecHash() error = nil, want non-nil for missing file")
	}
	if !strings.Contains(err.Error(), "reading spec") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "reading spec")
	}
}

func assertEmptyState(t *testing.T, s types.AnchorState, label string) {
	t.Helper()
	if s.Project != "" || s.Intent != "" || s.SpecHash != "" || s.NextTask != "" || len(s.Completed) != 0 {
		t.Errorf("Load() returned non-empty state for %s: %+v", label, s)
	}
}

func TestSpecHash_deterministic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte("deterministic content"), 0o644); err != nil {
		t.Fatal(err)
	}

	h1, err := SpecHash(path)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := SpecHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("SpecHash not deterministic: %q != %q", h1, h2)
	}
}

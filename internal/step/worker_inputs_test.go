package step

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestLoadContextDocs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "DESIGN.md"), []byte("design content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	docs := loadContextDocs(dir, []string{"*.md"})

	if len(docs) != 2 {
		t.Fatalf("loadContextDocs() returned %d docs, want 2", len(docs))
	}

	combined := strings.Join(docs, " ")
	if !strings.Contains(combined, "readme content") {
		t.Error("missing README.md content")
	}
	if !strings.Contains(combined, "design content") {
		t.Error("missing DESIGN.md content")
	}
}

func TestLoadContextDocs_Empty(t *testing.T) {
	t.Parallel()
	docs := loadContextDocs("/tmp", nil)
	if docs != nil {
		t.Errorf("loadContextDocs(nil) = %v, want nil", docs)
	}

	docs = loadContextDocs("/tmp", []string{})
	if docs != nil {
		t.Errorf("loadContextDocs(empty) = %v, want nil", docs)
	}
}

func TestLoadContextDocs_EmptyFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "whitespace.md"), []byte("   \n  "), 0o644); err != nil {
		t.Fatal(err)
	}

	docs := loadContextDocs(dir, []string{"*.md"})
	if len(docs) != 0 {
		t.Errorf("loadContextDocs() returned %d docs for empty files, want 0", len(docs))
	}
}

func TestLoadAgentPrompt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "backend.md"), []byte("You are a backend expert."), 0o644); err != nil {
		t.Fatal(err)
	}

	routing := map[string]string{
		"backend": "agents/backend.md",
	}

	got := loadAgentPrompt(dir, routing, types.TypeBackend)
	if got != "You are a backend expert." {
		t.Errorf("loadAgentPrompt() = %q, want %q", got, "You are a backend expert.")
	}
}

func TestLoadAgentPrompt_NoMatch(t *testing.T) {
	t.Parallel()
	routing := map[string]string{
		"backend": "agents/backend.md",
	}

	got := loadAgentPrompt("/tmp", routing, types.TypeFrontend)
	if got != "" {
		t.Errorf("loadAgentPrompt() = %q, want empty string", got)
	}
}

func TestLoadAgentPrompt_NilRouting(t *testing.T) {
	t.Parallel()
	got := loadAgentPrompt("/tmp", nil, types.TypeGeneral)
	if got != "" {
		t.Errorf("loadAgentPrompt(nil routing) = %q, want empty string", got)
	}
}

func TestLoadAgentPrompt_MissingFile(t *testing.T) {
	t.Parallel()
	routing := map[string]string{
		"general": "agents/missing.md",
	}
	got := loadAgentPrompt("/tmp", routing, types.TypeGeneral)
	if got != "" {
		t.Errorf("loadAgentPrompt(missing file) = %q, want empty string", got)
	}
}

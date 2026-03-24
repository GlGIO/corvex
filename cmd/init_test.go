package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitCreatesStructure(t *testing.T) {
	tmpDir := t.TempDir()

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting cwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir to tmpdir: %v", err)
	}
	defer os.Chdir(origDir)

	if err := runInit(nil, nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	expectedDirs := []string{
		".corvex",
		".corvex/agents",
		".corvex/context",
		".corvex/hooks",
		".corvex/templates",
		".corvex/tasks",
	}

	for _, dir := range expectedDirs {
		path := filepath.Join(tmpDir, dir)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("directory %s not created: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}

	expectedFiles := []string{
		".corvex/config.yaml",
		".corvex/agents/default.md",
		".corvex/context/README.md",
	}

	for _, file := range expectedFiles {
		path := filepath.Join(tmpDir, file)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("file %s not created: %v", file, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("file %s is empty", file)
		}
	}
}

func TestInitFailsWhenExists(t *testing.T) {
	tmpDir := t.TempDir()

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting cwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir to tmpdir: %v", err)
	}
	defer os.Chdir(origDir)

	if err := os.MkdirAll(filepath.Join(tmpDir, ".corvex"), 0o755); err != nil {
		t.Fatalf("creating .corvex: %v", err)
	}

	if err := runInit(nil, nil); err == nil {
		t.Fatal("expected error when .corvex/ already exists")
	}
}

func TestInitConfigContent(t *testing.T) {
	tmpDir := t.TempDir()

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting cwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir to tmpdir: %v", err)
	}
	defer os.Chdir(origDir)

	if err := runInit(nil, nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".corvex", "config.yaml"))
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}

	content := string(data)
	mustContain := []string{"project:", "provider:", "claude-cli", "sandbox:", "execution:"}
	for _, s := range mustContain {
		if !contains(content, s) {
			t.Errorf("config.yaml missing expected content: %q", s)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

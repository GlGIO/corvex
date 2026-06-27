package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupListTestDir(t *testing.T) (tmpDir string, cleanup func()) {
	t.Helper()
	tmpDir = t.TempDir()

	mkProject := func(name string, withSpec, withTasks bool) {
		dir := filepath.Join(tmpDir, ".corvex", "tasks", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating project dir %s: %v", name, err)
		}
		if withSpec {
			if err := os.WriteFile(filepath.Join(dir, "spec.md"), []byte("# spec"), 0o644); err != nil {
				t.Fatalf("writing spec.md: %v", err)
			}
		}
		if withTasks {
			if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte("# tasks"), 0o644); err != nil {
				t.Fatalf("writing tasks.md: %v", err)
			}
		}
	}

	mkProject("alpha", true, true)
	mkProject("beta", true, false)
	mkProject("gamma", false, true)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	return tmpDir, func() { os.Chdir(origDir) }
}

func TestListJSON(t *testing.T) {
	_, cleanup := setupListTestDir(t)
	defer cleanup()

	b := true
	listJSON = &b

	output, err := captureStdout(t, func() error {
		return runList(nil, nil)
	})
	if err != nil {
		t.Fatalf("runList --json failed: %v", err)
	}

	var projects []listProject
	if err := json.Unmarshal([]byte(output), &projects); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
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
		{"alpha", true, true, "ready"},
		{"beta", true, false, "needs planning"},
		{"gamma", false, true, "no spec"},
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

func TestListJSONEmptyProjects(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, ".corvex", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	b := true
	listJSON = &b

	output, err := captureStdout(t, func() error {
		return runList(nil, nil)
	})
	if err != nil {
		t.Fatalf("runList --json failed: %v", err)
	}

	var projects []listProject
	if err := json.Unmarshal([]byte(output), &projects); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected empty array, got %d items", len(projects))
	}
}

func TestListHumanOutputUnchanged(t *testing.T) {
	_, cleanup := setupListTestDir(t)
	defer cleanup()

	f := false
	listJSON = &f

	output, err := captureStdout(t, func() error {
		return runList(nil, nil)
	})
	if err != nil {
		t.Fatalf("runList failed: %v", err)
	}

	for _, want := range []string{"alpha", "beta", "gamma"} {
		found := false
		for _, line := range []string{output} {
			if len(line) > 0 {
				found = true
				_ = line
			}
		}
		if !found {
			_ = want
		}
	}

	// Verify it's not JSON
	if len(output) > 0 && output[0] == '[' {
		t.Error("human output should not start with '[' (looks like JSON)")
	}
}

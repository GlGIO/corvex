package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func makeTasksDir(t *testing.T, projects map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	tasksDir := filepath.Join(root, ".corvex", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, files := range projects {
		dir := filepath.Join(tasksDir, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte{}, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestProjectNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		projects map[string][]string // dir name → files to create
		want     []string
	}{
		{
			name:     "empty tasks dir",
			projects: map[string][]string{},
			want:     nil,
		},
		{
			name:     "spec.md only",
			projects: map[string][]string{"alpha": {"spec.md"}},
			want:     []string{"alpha"},
		},
		{
			name:     "tasks.md only",
			projects: map[string][]string{"beta": {"tasks.md"}},
			want:     []string{"beta"},
		},
		{
			name:     "both files",
			projects: map[string][]string{"gamma": {"spec.md", "tasks.md"}},
			want:     []string{"gamma"},
		},
		{
			name:     "dir with neither file excluded",
			projects: map[string][]string{"orphan": {"activity.jsonl"}},
			want:     nil,
		},
		{
			name: "mixed dirs",
			projects: map[string][]string{
				"aaa": {"spec.md"},
				"bbb": {"tasks.md"},
				"ccc": {"other.txt"},
			},
			want: []string{"aaa", "bbb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := makeTasksDir(t, tt.projects)
			got := projectNames(root)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			gotSet := make(map[string]bool, len(got))
			for _, n := range got {
				gotSet[n] = true
			}
			for _, w := range tt.want {
				if !gotSet[w] {
					t.Errorf("missing %q in result %v", w, got)
				}
			}
		})
	}
}

func TestSuggestProject(t *testing.T) {
	t.Parallel()

	projects := map[string][]string{
		"cli-basics":  {"spec.md"},
		"doctor":      {"spec.md"},
		"resilience":  {"spec.md"},
		"handoff":     {"spec.md"},
	}

	tests := []struct {
		input string
		want  string
	}{
		// Exact match (prefix of itself)
		{"cli-basics", "cli-basics"},
		// Case-insensitive prefix match
		{"CLI", "cli-basics"},
		{"doc", "doctor"},
		// Substring match
		{"basics", "cli-basics"},
		{"sili", "resilience"},
		// Levenshtein distance 1
		{"docter", "doctor"},
		// Levenshtein distance 2
		{"resilonce", "resilience"},
		// No close match
		{"zzzzz", ""},
		{"xyz123", ""},
	}

	root := makeTasksDir(t, projects)

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := suggestProject(root, tt.input)
			if got != tt.want {
				t.Errorf("suggestProject(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPrintJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   any
		wantKey string
		wantVal string
	}{
		{
			name:    "simple struct",
			input:   struct {
				Name   string `json:"name"`
				Count  int    `json:"count"`
			}{Name: "alpha", Count: 3},
			wantKey: `"name"`,
			wantVal: `"alpha"`,
		},
		{
			name:    "slice of strings",
			input:   []string{"a", "b"},
			wantKey: `"a"`,
		},
		{
			name:    "map",
			input:   map[string]int{"passed": 2, "failed": 0},
			wantKey: `"passed"`,
			wantVal: `2`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := printJSON(&buf, tt.input); err != nil {
				t.Fatalf("printJSON() error: %v", err)
			}
			out := buf.String()
			// Must be valid JSON.
			var raw json.RawMessage
			if err := json.Unmarshal([]byte(out), &raw); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, out)
			}
			// Must end with a newline.
			if out[len(out)-1] != '\n' {
				t.Errorf("output does not end with newline: %q", out)
			}
			// Must contain expected key/value substrings.
			if tt.wantKey != "" && !bytes.Contains([]byte(out), []byte(tt.wantKey)) {
				t.Errorf("output missing %q:\n%s", tt.wantKey, out)
			}
			if tt.wantVal != "" && !bytes.Contains([]byte(out), []byte(tt.wantVal)) {
				t.Errorf("output missing %q:\n%s", tt.wantVal, out)
			}
		})
	}
}

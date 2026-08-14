package cmd

// Unit tests for cmd/output.go. The project-inventory tests that used to share
// this file moved to internal/ops/project_test.go along with the code they
// exercise, when cmd/helpers.go (a shim of aliases over internal/ops) was
// deleted.

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPrintJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   any
		wantKey string
		wantVal string
	}{
		{
			name: "simple struct",
			input: struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
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

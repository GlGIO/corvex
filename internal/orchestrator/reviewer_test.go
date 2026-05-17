package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestParseVerdict_Pass(t *testing.T) {
	t.Parallel()
	output := "All checks passed.\nVERDICT: PASS"
	result := parseVerdict(output)
	if result.Verdict != VerdictPass {
		t.Errorf("Verdict = %q, want %q", result.Verdict, VerdictPass)
	}
}

func TestParseVerdict_Fail(t *testing.T) {
	t.Parallel()
	output := "Files missing.\nVERDICT: FAIL"
	result := parseVerdict(output)
	if result.Verdict != VerdictFail {
		t.Errorf("Verdict = %q, want %q", result.Verdict, VerdictFail)
	}
}

func TestParseVerdict_CaseInsensitive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  ReviewVerdict
	}{
		{"lowercase pass", "verdict: pass", VerdictPass},
		{"mixed case pass", "Verdict: Pass", VerdictPass},
		{"lowercase fail", "verdict: fail", VerdictFail},
		{"mixed case fail", "Verdict: Fail", VerdictFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseVerdict(tt.input)
			if result.Verdict != tt.want {
				t.Errorf("Verdict = %q, want %q", result.Verdict, tt.want)
			}
		})
	}
}

func TestParseVerdict_NoMarker(t *testing.T) {
	t.Parallel()
	output := "Some analysis output without any verdict marker."
	result := parseVerdict(output)
	if result.Verdict != VerdictFail {
		t.Errorf("Verdict = %q, want %q (default)", result.Verdict, VerdictFail)
	}
	if result.Summary != output {
		t.Errorf("Summary = %q, want %q", result.Summary, output)
	}
}

func TestParseVerdict_MultipleLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  ReviewVerdict
	}{
		{
			"last is PASS overrides earlier FAIL",
			"Line 1\nVERDICT: FAIL\nMore analysis\nVERDICT: PASS",
			VerdictPass,
		},
		{
			"last is FAIL overrides earlier PASS",
			"VERDICT: PASS\nSome issues found\nVERDICT: FAIL",
			VerdictFail,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseVerdict(tt.input)
			if result.Verdict != tt.want {
				t.Errorf("Verdict = %q, want %q", result.Verdict, tt.want)
			}
		})
	}
}

func TestParseVerdict_Category(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		verdict  ReviewVerdict
		category string
	}{
		{
			name:     "fail with category",
			input:    "Analysis says X is wrong.\nCATEGORY: wrong-approach\nVERDICT: FAIL",
			verdict:  VerdictFail,
			category: "wrong-approach",
		},
		{
			name:     "fail with mixed-case category",
			input:    "...\nCategory: Missing-Edge-Case\nVERDICT: FAIL",
			verdict:  VerdictFail,
			category: "missing-edge-case",
		},
		{
			name:     "fail without category",
			input:    "...\nVERDICT: FAIL",
			verdict:  VerdictFail,
			category: "",
		},
		{
			name:     "pass ignores category line",
			input:    "...\nCATEGORY: wrong-approach\nVERDICT: PASS",
			verdict:  VerdictPass,
			category: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseVerdict(tt.input)
			if got.Verdict != tt.verdict {
				t.Errorf("Verdict = %q, want %q", got.Verdict, tt.verdict)
			}
			if got.Category != tt.category {
				t.Errorf("Category = %q, want %q", got.Category, tt.category)
			}
		})
	}
}

func TestParseVerdict_SummaryExtraction(t *testing.T) {
	t.Parallel()
	output := "Check 1 ok\nCheck 2 ok\nVERDICT: PASS"
	result := parseVerdict(output)
	if result.Verdict != VerdictPass {
		t.Errorf("Verdict = %q, want PASS", result.Verdict)
	}
	want := "Check 1 ok\nCheck 2 ok"
	if result.Summary != want {
		t.Errorf("Summary = %q, want %q", result.Summary, want)
	}
}

func TestBuildReviewerPrompt(t *testing.T) {
	t.Parallel()
	task := &types.Task{
		ID:          "S01",
		Title:       "Test Task",
		Description: "Do the thing",
		Criteria:    []string{"criterion A", "criterion B"},
		Files: types.TaskFiles{
			Create: []string{"file1.go"},
			Modify: []string{"file2.go"},
		},
	}

	prompt := buildReviewerPrompt(task)

	checks := []struct {
		label string
		want  string
	}{
		{"task ID", "S01"},
		{"task title", "Test Task"},
		{"description", "Do the thing"},
		{"criterion A", "criterion A"},
		{"criterion B", "criterion B"},
		{"created file", "file1.go"},
		{"modified file", "file2.go"},
		{"verdict instruction", "VERDICT: PASS"},
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c.want) {
			t.Errorf("prompt missing %s (%q)", c.label, c.want)
		}
	}
}

func TestReview_Pass(t *testing.T) {
	t.Parallel()
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "All good.\nVERDICT: PASS"}, nil
		},
	}
	r := NewReviewer(mock, "test-model", "/tmp")
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := r.Review(context.Background(), task)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != VerdictPass {
		t.Errorf("Verdict = %q, want %q", result.Verdict, VerdictPass)
	}
}

func TestReview_Fail(t *testing.T) {
	t.Parallel()
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "Missing files.\nVERDICT: FAIL"}, nil
		},
	}
	r := NewReviewer(mock, "test-model", "/tmp")
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := r.Review(context.Background(), task)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != VerdictFail {
		t.Errorf("Verdict = %q, want %q", result.Verdict, VerdictFail)
	}
}

func TestReview_AllowedToolsEnforcement(t *testing.T) {
	t.Parallel()
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
		},
	}
	r := NewReviewer(mock, "test-model", "/tmp")
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	if _, err := r.Review(context.Background(), task); err != nil {
		t.Fatalf("Review() error = %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}

	got := mock.calls[0].AllowedTools
	want := []string{"Read", "Glob", "Grep", "Bash"}
	if len(got) != len(want) {
		t.Fatalf("AllowedTools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllowedTools[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReview_ProviderError(t *testing.T) {
	t.Parallel()
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return nil, fmt.Errorf("provider unavailable")
		},
	}
	r := NewReviewer(mock, "test-model", "/tmp")
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	_, err := r.Review(context.Background(), task)
	if err == nil {
		t.Fatal("Review() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "S01") {
		t.Errorf("error %q should mention task ID S01", err)
	}
}

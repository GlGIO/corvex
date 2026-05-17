package orchestrator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestDecideABWinner(t *testing.T) {
	t.Parallel()

	pass := func() *ReviewResult { return &ReviewResult{Verdict: VerdictPass} }
	fail := func() *ReviewResult { return &ReviewResult{Verdict: VerdictFail, Category: "wrong-approach"} }

	tests := []struct {
		name       string
		results    []abRunResult
		wantWinner int
		wantReason string
	}{
		{
			name: "A passes, B fails",
			results: []abRunResult{
				{Review: pass()},
				{Review: fail()},
			},
			wantWinner: 0,
			wantReason: "a-passed-b-failed",
		},
		{
			name: "B passes, A fails",
			results: []abRunResult{
				{Review: fail()},
				{Review: pass()},
			},
			wantWinner: 1,
			wantReason: "b-passed-a-failed",
		},
		{
			name: "both fail",
			results: []abRunResult{
				{Review: fail()},
				{Review: fail()},
			},
			wantWinner: -1,
			wantReason: "both-failed",
		},
		{
			name: "both pass, no costs → A wins",
			results: []abRunResult{
				{Review: pass(), Worker: &types.ExecuteResult{}},
				{Review: pass(), Worker: &types.ExecuteResult{}},
			},
			wantWinner: 0,
			wantReason: "tie-picked-a",
		},
		{
			name: "both pass, B cheaper → B wins",
			results: []abRunResult{
				{Review: pass(), Worker: &types.ExecuteResult{CostUSD: 0.50}},
				{Review: pass(), Worker: &types.ExecuteResult{CostUSD: 0.10}},
			},
			wantWinner: 1,
			wantReason: "tie-picked-b-cheaper",
		},
		{
			name: "A errored, B passed",
			results: []abRunResult{
				{Err: errors.New("worker crash")},
				{Review: pass()},
			},
			wantWinner: 1,
			wantReason: "b-passed-a-failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			winner, reason := decideABWinner(tt.results)
			if winner != tt.wantWinner {
				t.Errorf("winner = %d, want %d", winner, tt.wantWinner)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

func TestAppendABStats(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	run1 := abStatsRun{
		TaskID:    "S03",
		TaskType:  "backend",
		Models:    []string{"sonnet", "opus"},
		Winner:    "opus",
		Reason:    "a-passed-b-failed",
		Timestamp: "2026-05-17T10:00:00Z",
	}
	if err := appendABStats(dir, run1); err != nil {
		t.Fatalf("appendABStats(1) = %v", err)
	}

	run2 := abStatsRun{
		TaskID:    "S04",
		Models:    []string{"haiku", "sonnet"},
		Winner:    "sonnet",
		Reason:    "tie-picked-b-cheaper",
		Timestamp: "2026-05-17T10:05:00Z",
	}
	if err := appendABStats(dir, run2); err != nil {
		t.Fatalf("appendABStats(2) = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".corvex", "ab-stats.json"))
	if err != nil {
		t.Fatalf("read ab-stats: %v", err)
	}

	var got abStats
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal ab-stats: %v\nraw: %s", err, data)
	}

	if len(got.Runs) != 2 {
		t.Fatalf("Runs = %d, want 2", len(got.Runs))
	}
	if got.Runs[0].TaskID != "S03" || got.Runs[1].TaskID != "S04" {
		t.Errorf("Runs order wrong: %+v", got.Runs)
	}
	if got.Runs[1].Reason != "tie-picked-b-cheaper" {
		t.Errorf("Run[1].Reason = %q, want tie-picked-b-cheaper", got.Runs[1].Reason)
	}
}

func TestAbSideSuffix(t *testing.T) {
	t.Parallel()
	if got := abSideSuffix("S03", 0); got != "S03-a" {
		t.Errorf("got %q, want S03-a", got)
	}
	if got := abSideSuffix("S03", 1); got != "S03-b" {
		t.Errorf("got %q, want S03-b", got)
	}
}

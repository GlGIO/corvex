package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/types"
)

// abRunResult records the outcome of one side of an A/B comparison.
type abRunResult struct {
	Model    string
	Worktree *sandbox.Worktree
	Worker   *types.ExecuteResult
	Review   *ReviewResult
	Err      error
}

// abStats is the on-disk schema for .corvex/ab-stats.json.
type abStats struct {
	Runs []abStatsRun `json:"runs"`
}

type abStatsRun struct {
	TaskID    string   `json:"task_id"`
	TaskType  string   `json:"task_type"`
	Models    []string `json:"models"`
	Winner    string   `json:"winner"`     // empty when neither passed
	Reason    string   `json:"reason"`     // e.g. "a-passed-b-failed", "tie-picked-a", "both-failed"
	Timestamp string   `json:"timestamp"`
}

// runAB executes a single task on two worktrees with different models,
// reviews each side independently, merges the winner's branch back into the
// current HEAD, and removes the loser. Stats are appended to
// .corvex/ab-stats.json. Sandbox isolation is bypassed during A/B because
// each side runs in a dedicated worktree on disk; future work can wire
// per-worktree containerised sandboxes.
func (o *Orchestrator) runAB(ctx context.Context, t *types.Task, models []string) error {
	if len(models) != 2 {
		return fmt.Errorf("a/b run requires exactly 2 models, got %d", len(models))
	}

	contextDocs := loadContextDocs(o.workDir, o.cfg.Context.AlwaysInclude)
	agentPrompt := loadAgentPrompt(o.workDir, o.cfg.AgentRouting, t.Type)
	anchorState, _ := anchor.Load(filepath.Join(o.workDir, ".corvex", "tasks", o.cfg.Project.Name, "anchor.yaml"))
	anchorCtx := anchor.GenerateContext(anchorState, t.ID)

	results := make([]abRunResult, 2)
	var wg sync.WaitGroup
	for i, m := range models {
		i, m := i, m
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = o.runABSide(ctx, t, m, abSideSuffix(t.ID, i), anchorCtx, contextDocs, agentPrompt)
		}()
	}
	wg.Wait()

	defer func() {
		// Worktree cleanup for the loser happens below; this defer covers
		// abnormal early returns so we never leak worktrees on disk.
		for _, r := range results {
			if r.Worktree != nil {
				_ = r.Worktree.Remove(context.Background(), o.workDir)
			}
		}
	}()

	winner, reason := decideABWinner(results)

	o.emit(Event{
		Type:    EventTaskComplete,
		TaskID:  t.ID,
		Status:  statusForWinner(winner),
		Message: fmt.Sprintf("a/b: %s (%s)", reason, summariseModels(models, winner)),
	})

	if err := appendABStats(o.workDir, abStatsRun{
		TaskID:    t.ID,
		TaskType:  string(t.Type),
		Models:    models,
		Winner:    modelForWinner(winner, models),
		Reason:    reason,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		log.Warn("appending ab stats", "task", t.ID, "err", err)
	}

	if winner < 0 {
		return fmt.Errorf("task %s failed in both a/b branches", t.ID)
	}

	// Merge the winner branch into HEAD. The loser worktree is removed by
	// the deferred cleanup; null out the winner's pointer so it survives.
	winnerWT := results[winner].Worktree
	results[winner].Worktree = nil

	if err := mergeBranchIntoHEAD(ctx, o.workDir, winnerWT.Branch); err != nil {
		// Re-add to cleanup list so it is removed.
		results[winner].Worktree = winnerWT
		return fmt.Errorf("merging winner branch %s: %w", winnerWT.Branch, err)
	}

	// Successful merge → remove winner worktree now (kept its commits, not the dir).
	if err := winnerWT.Remove(ctx, o.workDir); err != nil {
		log.Warn("removing winner worktree after merge", "path", winnerWT.Path, "err", err)
	}

	log.Info("a/b run complete", "task", t.ID, "winner", modelForWinner(winner, models), "reason", reason)
	return nil
}

func (o *Orchestrator) runABSide(
	ctx context.Context,
	t *types.Task,
	model, suffix, anchorCtx string,
	contextDocs []string,
	agentPrompt string,
) abRunResult {
	wt, err := sandbox.CreateWorktree(ctx, o.workDir, suffix)
	if err != nil {
		return abRunResult{Model: model, Err: fmt.Errorf("create worktree: %w", err)}
	}

	worker := NewWorker(o.provider, model, wt.Path, nil)
	workerResult, err := worker.Execute(ctx, t, anchorCtx, contextDocs, agentPrompt, "")
	if err != nil {
		return abRunResult{Model: model, Worktree: wt, Err: fmt.Errorf("worker: %w", err)}
	}

	reviewer := NewReviewer(o.provider, o.cfg.Provider.Models.Reviewer, wt.Path)
	reviewResult, err := reviewer.Review(ctx, t)
	if err != nil {
		return abRunResult{Model: model, Worktree: wt, Worker: workerResult, Err: fmt.Errorf("reviewer: %w", err)}
	}

	return abRunResult{
		Model:    model,
		Worktree: wt,
		Worker:   workerResult,
		Review:   reviewResult,
	}
}

// decideABWinner returns the index of the winning side (0 or 1) and a short
// reason string. Returns -1 when neither side passed review.
func decideABWinner(results []abRunResult) (int, string) {
	a, b := results[0], results[1]
	aPass := a.Err == nil && a.Review != nil && a.Review.Verdict == VerdictPass
	bPass := b.Err == nil && b.Review != nil && b.Review.Verdict == VerdictPass

	switch {
	case aPass && !bPass:
		return 0, "a-passed-b-failed"
	case bPass && !aPass:
		return 1, "b-passed-a-failed"
	case aPass && bPass:
		// Tie-breaker: prefer the cheaper run. CostUSD is reported by the
		// provider when available; fall back to "a wins" if costs are
		// missing or equal.
		if a.Worker != nil && b.Worker != nil && b.Worker.CostUSD < a.Worker.CostUSD && b.Worker.CostUSD > 0 {
			return 1, "tie-picked-b-cheaper"
		}
		return 0, "tie-picked-a"
	default:
		return -1, "both-failed"
	}
}

func abSideSuffix(taskID string, side int) string {
	label := "a"
	if side == 1 {
		label = "b"
	}
	return fmt.Sprintf("%s-%s", taskID, label)
}

func summariseModels(models []string, winner int) string {
	if winner < 0 {
		return fmt.Sprintf("both failed: %s, %s", models[0], models[1])
	}
	return fmt.Sprintf("%s beat %s", models[winner], models[1-winner])
}

func modelForWinner(winner int, models []string) string {
	if winner < 0 || winner >= len(models) {
		return ""
	}
	return models[winner]
}

func statusForWinner(winner int) types.TaskStatus {
	if winner < 0 {
		return types.StatusFailed
	}
	return types.StatusPassed
}

func appendABStats(workDir string, run abStatsRun) error {
	path := filepath.Join(workDir, ".corvex", "ab-stats.json")

	var stats abStats
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if jerr := json.Unmarshal(data, &stats); jerr != nil {
			log.Warn("existing ab-stats.json is unreadable, starting fresh", "err", jerr)
			stats = abStats{}
		}
	}

	stats.Runs = append(stats.Runs, run)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create stats dir: %w", err)
	}
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func mergeBranchIntoHEAD(ctx context.Context, repoRoot, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "merge", "--no-ff",
		"-m", fmt.Sprintf("corvex: merge a/b winner %s", branch), branch)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git merge %s: %w (output: %s)", branch, err, strings.TrimSpace(out.String()))
	}
	return nil
}

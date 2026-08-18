package step

import (
	"context"
	"fmt"
	"strings"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// applyEscalation charges a rejection against the category's counter and
// applies whatever the policy decided: upgrade the worker's model, spawn a
// read-only investigation that produces a sharper diagnosis, or stop the run
// for a human. Only the human hand-off returns an error, and it is fatal.
func (e *Executor) applyEscalation(
	ctx context.Context,
	r *Run,
	t *types.Task,
	st *aiTask,
	attempt int,
	reviewResult *ReviewResult,
) error {
	cat := reviewResult.Category
	if cat == "" {
		return nil
	}

	st.categoryCounts[cat]++
	decision := resolveEscalation(e.cfg.Review, cat, st.categoryCounts[cat])
	switch decision.Action {
	case ActionUpgradeModel:
		if decision.UpgradeTo != "" && decision.UpgradeTo != st.worker.model {
			charmbraceletlog.Info("escalation: upgrading worker model",
				"task", t.ID, "category", cat, "from", st.worker.model, "to", decision.UpgradeTo)
			st.worker.model = decision.UpgradeTo
		}
	case ActionHumanPrompt:
		path, err := writeHumanEscalation(e.workDir, e.cfg.Project.Name, t.ID, cat, reviewResult.Summary)
		if err != nil {
			charmbraceletlog.Warn("writing human escalation", "task", t.ID, "err", err)
		} else {
			charmbraceletlog.Warn("escalation: human review requested",
				"task", t.ID, "category", cat, "file", path)
		}
		if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
			charmbraceletlog.Warn("updating task status to failed after escalation", "task", t.ID, "err", statusErr)
		}
		return Fatal(fmt.Errorf("task %s escalated to human review (category %s); see %s", t.ID, cat, path))
	case ActionSpawnInvestigation:
		investigationDiagnosis := e.runInvestigation(ctx, t, reviewResult.Summary)
		st.diagnosis = investigationDiagnosis
		// `worker` for the same reason as the plain retry: the line marks the
		// worker going again. The investigator's own spend is invisible either
		// way — runInvestigation drops result.CostUSD on the floor, so there is
		// no number here to attribute to anybody.
		e.emit(event.Event{
			Type:    event.Retry,
			TaskID:  t.ID,
			Phase:   event.PhaseWorker,
			Attempt: attempt,
			Message: "spawn-investigation: " + investigationDiagnosis,
		})
	}
	return nil
}

// runInvestigation asks a read-only agent to root-cause the rejection and
// returns its diagnosis, falling back to the reviewer's summary whenever the
// investigation fails or comes back empty.
func (e *Executor) runInvestigation(ctx context.Context, t *types.Task, reviewerSummary string) string {
	prompt := buildInvestigationPrompt(t, reviewerSummary)
	result, err := e.provider.Execute(ctx, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        e.investigationModel,
		WorkDir:      e.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep"},
	})
	if err != nil {
		charmbraceletlog.Warn("spawn-investigation failed", "task", t.ID, "err", err)
		return reviewerSummary
	}
	if result == nil || strings.TrimSpace(result.Output) == "" {
		return reviewerSummary
	}
	return strings.TrimSpace(result.Output)
}

func buildInvestigationPrompt(t *types.Task, reviewerSummary string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a software engineering investigator. A task has been rejected by the reviewer.\n\n")
	fmt.Fprintf(&b, "## Task\n\n")
	fmt.Fprintf(&b, "ID: %s\n", t.ID)
	fmt.Fprintf(&b, "Title: %s\n", t.Title)
	if t.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", t.Description)
	}
	if len(t.Criteria) > 0 {
		b.WriteString("\nSuccess criteria:\n")
		for _, c := range t.Criteria {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	fmt.Fprintf(&b, "\n## Reviewer Feedback\n\n%s\n\n", reviewerSummary)
	b.WriteString("## Your Goal\n\n")
	b.WriteString("Investigate the codebase (use Read, Glob, Grep — read-only) and provide:\n")
	b.WriteString("1. A concrete root-cause diagnosis: what exactly is wrong?\n")
	b.WriteString("2. A recommended fix approach: what specific changes should be made?\n\n")
	b.WriteString("Be concise and actionable. Your output will guide the next implementation attempt.\n")
	return b.String()
}

package cmd

import (
	"bufio"
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/planning"
)

const maxGrillIterations = 50

// DefaultGrillBudget is the stopping rule the roadmap's backlog demanded, and
// the number is a JUDGMENT, not a measurement — which is why the knob exists.
//
// What was measured is the failure: the `host-inheritance` interview asked 28
// questions across two weeks and was never planned. Every one of those sessions
// respected the per-session cap of 50, because the cap counted iterations of a
// loop and the thing that ran away was the PROJECT. So the budget below is
// cumulative and read from disk: 12 answered questions is where an interview
// stops discovering and starts restating, in the only sample anybody has. Raise
// it with --max-questions when the sample is wrong; 0 restores the old
// behaviour, which is the bug.
const DefaultGrillBudget = 12

// runGrillLoop is the shared interactive Q&A loop used by both `grill` and `start`.
func runGrillLoop(ctx context.Context, griller *planning.Griller, reader *bufio.Reader, project, specPath, decisionsPath string) error {
	totalCost := 0.0
	answered := 0

	// Cumulative, across every session this project ever had — see
	// DefaultGrillBudget for why per-session counting missed the failure.
	already := ops.CountDecisions(decisionsPath)
	budget := grillBudget
	if budget > 0 && already >= budget {
		fmt.Printf("%s already has %d recorded decision(s), at or over the budget of %d.\n", project, already, budget)
		fmt.Printf("An interview that keeps going stops discovering and starts restating.\n")
		fmt.Printf("  → corvex plan %s            (use what is there)\n", project)
		fmt.Printf("  → corvex grill %s --max-questions %d   (deliberately keep going)\n", project, already+5)
		return nil
	}

	fmt.Printf("Grilling %s — Ctrl+C to stop (decisions persist in decisions.md)\n", project)
	// Announced only once there is something to announce. On a fresh project
	// the budget is not information — it is a line the eye learns to skip, and
	// it would have rewritten five goldens of a command this change never
	// intended to touch.
	if budget > 0 && already > 0 {
		fmt.Printf("Budget: %d question(s) for this project, %d already answered.\n", budget, already)
	}

	for i := 0; i < maxGrillIterations; i++ {
		if budget > 0 && already+answered >= budget {
			fmt.Printf("\n■ Budget reached: %d question(s) answered for %s, $%.2f spent.\n", already+answered, project, totalCost)
			fmt.Printf("  → corvex plan %s\n", project)
			fmt.Printf("  → corvex grill %s --max-questions %d   (if it is genuinely not resolved yet)\n", project, budget+5)
			return nil
		}
		log.Info("grilling", "iteration", i+1)
		step, err := griller.Grill(ctx, specPath, decisionsPath)
		if err != nil {
			return fmt.Errorf("grill step: %w", err)
		}
		totalCost += step.CostUSD

		if step.Done {
			fmt.Printf("\n✓ No further ambiguities. %d decision(s) recorded, $%.2f spent.\n", answered, totalCost)
			fmt.Printf("  Next: corvex plan %s\n", project)
			return nil
		}

		if step.Reflection != "" {
			fmt.Printf("\n💬 %s\n", step.Reflection)
		}
		fmt.Printf("\n🔍 %s\n", step.Question)
		if step.Recommended != "" {
			fmt.Printf("💡 Recommended: %s\n", step.Recommended)
		}
		if step.Rationale != "" {
			fmt.Printf("   why: %s\n", step.Rationale)
		}

		answer, action, err := readGrillAnswer(ctx, reader, griller, specPath, decisionsPath, step.Recommended)
		if err != nil {
			return err
		}
		switch action {
		case answerDone:
			fmt.Printf("\n✓ Stopped early. %d decision(s) recorded, $%.2f spent.\n", answered, totalCost)
			fmt.Printf("  Next: corvex plan %s\n", project)
			return nil
		case answerSkip:
			if err := ops.AppendDecision(decisionsPath, step.Question, "(skipped — leave to planner)"); err != nil {
				return err
			}
		case answerProvide:
			if err := ops.AppendDecision(decisionsPath, step.Question, answer); err != nil {
				return err
			}
		}
		answered++
	}

	fmt.Printf("\nReached iteration cap (%d). Run 'corvex plan %s' with what we have or continue with another 'corvex grill'.\n",
		maxGrillIterations, project)
	return nil
}

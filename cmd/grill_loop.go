package cmd

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/planning"
)

const maxGrillIterations = 50

// runGrillLoop is the shared interactive Q&A loop used by both `grill` and `start`.
func runGrillLoop(ctx context.Context, griller *planning.Griller, reader *bufio.Reader, project, specPath, decisionsPath string) error {
	totalCost := 0.0
	answered := 0

	fmt.Printf("Grilling %s — Ctrl+C to stop (decisions persist in decisions.md)\n", project)

	for i := 0; i < maxGrillIterations; i++ {
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

// readGrillAnswer mirrors readBrainstormAnswer for the grill loop:
// supports /ask (Griller.AskFollowup), /summary, /skip, /done, and re-prompts
// after any interjection so the user keeps the same 🔍 in front of them.
func readGrillAnswer(ctx context.Context, reader *bufio.Reader, griller *planning.Griller, specPath, decisionsPath, recommended string) (string, answerAction, error) {
	for {
		fmt.Print("Your answer (Enter to accept, /ask <q>, /summary, /skip, /done): ")
		raw, err := reader.ReadString('\n')
		if err != nil {
			return "", 0, fmt.Errorf("reading answer: %w", err)
		}
		trimmed := strings.TrimSpace(raw)

		switch {
		case trimmed == "/done":
			return "", answerDone, nil
		case trimmed == "/skip":
			return "", answerSkip, nil
		case trimmed == "/summary":
			printDecisionsSummary(decisionsPath)
		case strings.HasPrefix(trimmed, "/ask"):
			question := strings.TrimSpace(strings.TrimPrefix(trimmed, "/ask"))
			if question == "" {
				fmt.Println("(usage: /ask <your question>)")
				continue
			}
			reply, askErr := griller.AskFollowup(ctx, specPath, decisionsPath, question)
			if askErr != nil {
				fmt.Printf("(ask failed: %v)\n", askErr)
				continue
			}
			fmt.Printf("\n💬 %s\n\n", reply)
		case trimmed == "":
			if recommended == "" {
				fmt.Println("(sem recomendação concreta — digite uma resposta, /ask para perguntar ao modelo, /summary para rever, /skip pra pular)")
				continue
			}
			return recommended, answerProvide, nil
		default:
			return trimmed, answerProvide, nil
		}
	}
}

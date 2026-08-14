package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/planning"
	"github.com/giovannialves/corvex/internal/provider"
)

const maxBrainstormIterations = 20

// brainstormPath runs an AI-driven Q&A to explore requirements, writes spec.md, then grills it.
func brainstormPath(ctx context.Context, p provider.Provider, model, workDir, project, specPath, decisionsPath, qaPath string, reader *bufio.Reader) error {
	description := readMultilineInput(reader, "Briefly describe the feature (blank line to submit):\n> ")
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("feature description cannot be empty")
	}

	br := planning.NewBrainstormer(p, model, workDir)
	br.SetProgressWriter(os.Stdout) // surface tool calls live so the model never looks hung
	griller := planning.NewGriller(p, model, workDir)
	griller.SetProgressWriter(os.Stdout)

	fmt.Println()
	fmt.Printf("Brainstorming %s — Ctrl+C to stop, /done to finish early\n", project)

	totalCost := 0.0
	answered := 0

	for i := 0; i < maxBrainstormIterations; i++ {
		log.Info("brainstorming", "iteration", i+1)
		step, err := br.Interview(ctx, description, qaPath)
		if err != nil {
			return fmt.Errorf("brainstorm step: %w", err)
		}
		totalCost += step.CostUSD

		if step.Done {
			fmt.Printf("\n✓ Design questions resolved (%d answers, $%.2f). Writing spec.md...\n", answered, totalCost)
			break
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

		answer, action, err := readBrainstormAnswer(ctx, reader, br, description, qaPath, step.Recommended)
		if err != nil {
			return err
		}
		switch action {
		case answerDone:
			fmt.Printf("\n✓ Stopped early (%d answers). Writing spec.md...\n", answered)
			goto writeSpec
		case answerSkip:
			if err := appendDecision(qaPath, step.Question, "(skipped)"); err != nil {
				return err
			}
		case answerProvide:
			if err := appendDecision(qaPath, step.Question, answer); err != nil {
				return err
			}
		}
		answered++
	}

writeSpec:
	log.Info("generating spec.md from brainstorm Q&A")
	if err := br.GenerateSpec(ctx, description, qaPath, specPath); err != nil {
		return fmt.Errorf("generating spec.md: %w", err)
	}
	fmt.Printf("✓ spec.md written to %s\n\n", specPath)

	return runGrillLoop(ctx, griller, reader, project, specPath, decisionsPath)
}

// grillPath captures a quick feature description as spec.md then runs the grill loop.
func grillPath(ctx context.Context, p provider.Provider, model, workDir, project, specPath, decisionsPath string, reader *bufio.Reader) error {
	description := readMultilineInput(reader, "Describe the feature (blank line to submit):\n> ")
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("feature description cannot be empty")
	}

	if err := ops.WriteMinimalSpec(specPath, project, description); err != nil {
		return err
	}
	fmt.Printf("✓ spec.md written to %s\n\n", specPath)

	return planPath(ctx, p, model, workDir, project, specPath, decisionsPath, reader)
}

// planPath runs the grill loop on an existing spec.md.
func planPath(ctx context.Context, p provider.Provider, model, workDir, project, specPath, decisionsPath string, reader *bufio.Reader) error {
	griller := planning.NewGriller(p, model, workDir)
	griller.SetProgressWriter(os.Stdout)
	return runGrillLoop(ctx, griller, reader, project, specPath, decisionsPath)
}

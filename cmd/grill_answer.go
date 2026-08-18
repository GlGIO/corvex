package cmd

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/giovannialves/corvex/internal/planning"
)

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

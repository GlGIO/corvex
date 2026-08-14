package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/giovannialves/corvex/internal/planning"
)

// answerAction signals what the input loop decided after the user typed.
// Shared with the grill loop in grill.go, which reads the same slash commands.
type answerAction int

const (
	answerProvide answerAction = iota // user supplied an answer (text or accepted recommendation)
	answerSkip                        // user typed /skip
	answerDone                        // user typed /done
)

// readBrainstormAnswer drives the inner Q&A prompt: it reads a line, handles
// slash commands (`/ask`, `/summary`, `/skip`, `/done`), and only returns
// when the user supplies a real answer (or chooses to skip/finish). The
// caller stays focused on appending the answer and advancing the loop.
//
// The hint line is printed every time so the user remembers the commands
// after an interjection.
func readBrainstormAnswer(ctx context.Context, reader *bufio.Reader, br *planning.Brainstormer, description, qaPath, recommended string) (string, answerAction, error) {
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
			printDecisionsSummary(qaPath)
		case strings.HasPrefix(trimmed, "/ask"):
			question := strings.TrimSpace(strings.TrimPrefix(trimmed, "/ask"))
			if question == "" {
				fmt.Println("(usage: /ask <your question>)")
				continue
			}
			reply, askErr := br.AskFollowup(ctx, description, qaPath, question)
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

// printDecisionsSummary lists the decisions accumulated in qaPath so far.
// Best-effort: missing or empty file just prints a friendly placeholder
// rather than erroring out, since this is a UX helper, not a load-bearing
// operation.
func printDecisionsSummary(qaPath string) {
	data, err := os.ReadFile(qaPath)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		fmt.Println("\n(nenhuma decisão registrada ainda)")
		return
	}
	fmt.Printf("\nDecisões até agora (de %s):\n%s\n", qaPath, strings.TrimSpace(string(data)))
}

// readMultilineInput prints prompt and reads lines until a blank line or EOF.
func readMultilineInput(reader *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	var lines []string
	for {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" || err != nil {
			break
		}
		lines = append(lines, trimmed)
		fmt.Print("> ")
	}
	return strings.Join(lines, "\n")
}

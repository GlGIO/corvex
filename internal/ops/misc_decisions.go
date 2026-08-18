package ops

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AppendDecision records one resolved question in a decisions/Q&A markdown file,
// creating the file and its directory when they do not exist yet. Appending —
// never rewriting — is what makes an interrupted interview resumable: the
// answers already given survive.
func AppendDecision(path, question, answer string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating decisions dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening decisions file: %w", err)
	}
	defer f.Close()

	ts := time.Now().UTC().Format(time.RFC3339)
	entry := fmt.Sprintf("## %s\n_recorded: %s_\n\n**A:** %s\n\n", question, ts, answer)
	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("writing decision: %w", err)
	}
	return nil
}

// CountDecisions reports how many questions a project has already answered.
//
// It exists for the stopping rule (roadmap backlog: "grill sem regra de parada =
// bug ativo"). The count has to be read from DISK rather than kept in the loop,
// because the failure it bounds happens ACROSS invocations: the measured case
// was 28 questions over two weeks, none of which was ever planned, and each
// individual session stayed under its own per-session cap.
//
// A missing file is zero, not an error: a project that never grilled has
// answered nothing.
func CountDecisions(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	// A decision is written as `## <question>` by AppendDecision, so counting the
	// headings counts the questions. Long questions are normal, so the default
	// 64 KiB line budget stays.
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "## ") {
			n++
		}
	}
	return n
}

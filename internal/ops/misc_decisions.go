package ops

import (
	"fmt"
	"os"
	"path/filepath"
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

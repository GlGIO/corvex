package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/ops"
)

// doctorGate runs the same checks as `corvex doctor` and returns an error
// listing any that FAILED (warnings don't block), so a misconfigured run aborts
// before spending tokens.
func doctorGate(cfg *config.Config, workDir string) error {
	var failed []string
	for _, r := range ops.AllChecks(cfg, workDir) {
		if r.Status == ops.CheckFail {
			failed = append(failed, fmt.Sprintf("  ✗ %s: %s", r.Name, r.Message))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("config preflight failed (run `corvex doctor` for details):\n%s\n\n→ fix the above, or pass --skip-doctor to bypass",
		strings.Join(failed, "\n"))
}

// confirmRun returns true when the run should proceed. It auto-proceeds when
// --yes is set or stdout is not an interactive TTY (CI/pipes must not block);
// otherwise it prompts on stdin and defaults to No.
func confirmRun() bool {
	if runYes || !isInteractive() {
		return true
	}
	fmt.Fprint(os.Stderr, "Proceed? [y/N] ")
	return confirmYes()
}

// confirmYes reads one line from stdin and answers "did they say yes", with No
// as the default on anything else — including EOF. The prompt itself belongs to
// the caller, because the question differs (spending money, stopping a run).
func confirmYes() bool {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

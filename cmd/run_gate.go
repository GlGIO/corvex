package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/tui"
	"github.com/giovannialves/corvex/internal/types"
)

// doctorGate runs the same checks as `corvex doctor` and returns an error
// listing any that FAILED (warnings don't block), so a misconfigured run aborts
// before spending tokens.
func doctorGate(cfg *config.Config, workDir string) error {
	var failed []string
	for _, r := range allChecks(cfg, workDir) {
		if r.status == checkFail {
			failed = append(failed, fmt.Sprintf("  ✗ %s: %s", r.name, r.msg))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("config preflight failed (run `corvex doctor` for details):\n%s\n\n→ fix the above, or pass --skip-doctor to bypass",
		strings.Join(failed, "\n"))
}

// runPreview builds a one-or-two-line summary of what `corvex run` is about to
// do: how many PENDING tasks will execute and the configured cost ceilings,
// plus any spend already recorded for the project.
func runPreview(workDir, project string, cfg *config.Config) string {
	pending := 0
	tasksPath := filepath.Join(projectDir(workDir, project), "tasks.md")
	if tasks, _, err := task.ParseTasksFile(tasksPath); err == nil {
		for _, t := range tasks {
			if t.Status == types.StatusPending {
				pending++
			}
		}
	}

	ceil := "no cost ceiling set"
	if cfg.Execution.MaxCostUSD > 0 || cfg.Execution.MaxCostPerTaskUSD > 0 {
		ceil = fmt.Sprintf("ceilings: %s/run, %s/task",
			tui.FormatCost(cfg.Execution.MaxCostUSD), tui.FormatCost(cfg.Execution.MaxCostPerTaskUSD))
	}

	line := fmt.Sprintf("Run %q: %d pending task(s) · %s", project, pending, ceil)
	if summary, err := activity.Summarize(workDir, project); err == nil && summary.TotalCostUSD > 0 {
		line += fmt.Sprintf(" · already spent %s", tui.FormatCost(summary.TotalCostUSD))
	}
	return line
}

// confirmRun returns true when the run should proceed. It auto-proceeds when
// --yes is set or stdout is not an interactive TTY (CI/pipes must not block);
// otherwise it prompts on stdin and defaults to No.
func confirmRun() bool {
	if runYes || !isInteractive() {
		return true
	}
	fmt.Fprint(os.Stderr, "Proceed? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

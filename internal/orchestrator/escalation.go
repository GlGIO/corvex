package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/config"
)

// Escalation action identifiers used in config.EscalationPolicy.Action.
const (
	ActionUpgradeModel        = "upgrade-model"
	ActionSpawnInvestigation  = "spawn-investigation"
	ActionHumanPrompt         = "human-prompt"
)

// escalationDecision is produced by the policy engine for a given task and
// rejection category.
type escalationDecision struct {
	// Action mirrors EscalationPolicy.Action. Empty when no policy matches.
	Action string
	// UpgradeTo is set when Action == ActionUpgradeModel.
	UpgradeTo string
	// CategoryCount is the running count of failures of this category for
	// the current task, used purely for logging / events.
	CategoryCount int
}

// resolveEscalation looks up the policy for the given category and returns
// the decision to apply on the next retry. Returns an empty decision when
// the policy is undefined, disabled (After == 0), or the count has not yet
// reached the trigger threshold.
func resolveEscalation(cfg config.ReviewConfig, category string, count int) escalationDecision {
	if category == "" {
		return escalationDecision{}
	}
	policy, ok := cfg.Escalation[category]
	if !ok {
		return escalationDecision{}
	}
	if policy.After <= 0 || count < policy.After {
		return escalationDecision{}
	}
	return escalationDecision{
		Action:        policy.Action,
		UpgradeTo:     policy.To,
		CategoryCount: count,
	}
}

// writeHumanEscalation persists a human-readable note describing why a task
// was paused for review. The file lives at
// <workDir>/.corvex/escalations/<project>-<taskID>.md and is surfaced by the
// `corvex review` command.
func writeHumanEscalation(workDir, project, taskID, category, summary string) (string, error) {
	dir := filepath.Join(workDir, ".corvex", "escalations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create escalations dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.md", project, taskID))

	var b strings.Builder
	fmt.Fprintf(&b, "# Escalation: %s / %s\n\n", project, taskID)
	fmt.Fprintf(&b, "- Category: `%s`\n", category)
	fmt.Fprintf(&b, "- Logged at: %s\n\n", time.Now().Format(time.RFC3339))
	b.WriteString("The task hit the escalation threshold for this category.\n")
	b.WriteString("It has been marked FAILED so execution can stop for human review.\n\n")
	b.WriteString("## Last reviewer summary\n\n")
	b.WriteString(summary)
	b.WriteString("\n\n## Next steps\n\n")
	b.WriteString("1. Read the diff and reviewer summary above.\n")
	b.WriteString("2. Adjust the spec, decisions, or task description if needed.\n")
	b.WriteString("3. Delete this file when resolved.\n")
	b.WriteString("4. Re-run `corvex run` to retry the task.\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

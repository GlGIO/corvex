package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/provider"
	sandboxpkg "github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/tui"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var (
	runTask     string
	runSingle   bool
	runDryRun   bool
	runPlain    bool
	flagValidate bool
	runAB       string
)

var runCmd = &cobra.Command{
	Use:   "run <project>",
	Short: "Execute pending tasks for a project",
	Long:  "Run the orchestration loop: DAG resolve → Worker → Reviewer → checkpoint → next.",
	Args:  cobra.ExactArgs(1),
	RunE:  runRun,
}

func init() {
	runCmd.Flags().StringVar(&runTask, "task", "", "run only a specific task (e.g. S03)")
	runCmd.Flags().BoolVar(&runSingle, "single", false, "run only the next pending task")
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "show execution plan without running")
	runCmd.Flags().BoolVar(&runPlain, "plain", false, "disable TUI, use plain log output")
	runCmd.Flags().BoolVar(&flagValidate, "validate", false, "run integration validation after all tasks complete")
	runCmd.Flags().StringVar(&runAB, "ab", "", "A/B run two models against one task (e.g. --ab sonnet,opus); requires --task")
	rootCmd.AddCommand(runCmd)
}

func runRun(cmd *cobra.Command, args []string) error {
	project := args[0]

	cfg, workDir, err := loadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	if runDryRun {
		pDir := projectDir(workDir, project)
		tasksPath := filepath.Join(pDir, "tasks.md")
		return dryRun(tasksPath, project)
	}

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	sb := sandboxpkg.NewSandbox(cfg.Sandbox)

	events := make(chan orchestrator.Event, 64)
	commands := make(chan orchestrator.Command, 16)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	abModels, err := parseABModels(runAB)
	if err != nil {
		return err
	}
	if len(abModels) > 0 && runTask == "" && !runSingle {
		return fmt.Errorf("--ab requires --task <id> or --single to scope the comparison")
	}

	orc := orchestrator.New(orchestrator.Options{
		Config:     cfg,
		Provider:   p,
		WorkDir:    workDir,
		Events:     events,
		TargetTask: runTask,
		SingleTask: runSingle,
		Sandbox:    sb,
		ABModels:   abModels,
		Commands:   commands,
	})

	if !runPlain && isInteractive() {
		return runWithTUI(ctx, orc, events, commands, cancel, project, workDir)
	}

	go drainEvents(events)

	log.Info("running", "project", project)
	if err := orc.Run(ctx, project); err != nil {
		return fmt.Errorf("run failed: %w", err)
	}

	log.Info("completed", "project", project)

	if flagValidate {
		if !validateConfigured(cfg.Validate) {
			return fmt.Errorf("--validate set but validate: not configured — run 'corvex validate %s' first to set it up", project)
		}
		return validateProject(cmd.Context(), cfg, workDir, project)
	}
	return nil
}

// parseABModels splits a comma-separated flag value like "sonnet,opus" into
// a 2-element slice, trimming whitespace. Returns nil when the flag is empty.
// Errors when fewer than 2 distinct models are provided.
func parseABModels(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	models := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			models = append(models, p)
		}
	}
	if len(models) != 2 {
		return nil, fmt.Errorf("--ab needs exactly 2 models separated by a comma (got %d: %q)", len(models), raw)
	}
	if models[0] == models[1] {
		return nil, fmt.Errorf("--ab models must differ (got %q twice)", models[0])
	}
	return models, nil
}

func isInteractive() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func runWithTUI(ctx context.Context, orc *orchestrator.Orchestrator, events chan orchestrator.Event, commands chan orchestrator.Command, cancel context.CancelFunc, project, workDir string) error {
	m := tui.NewWithCommands(events, commands, cancel, project)

	// Pre-populate the DAG panel from tasks.md so the TUI shows the task list
	// immediately on startup. Without this, the panel renders "no tasks loaded"
	// until (and unless) the orchestrator emits per-task events.
	tasksPath := filepath.Join(projectDir(workDir, project), "tasks.md")
	if tasks, _, err := task.ParseTasksFile(tasksPath); err == nil {
		entries := make([]tui.TaskEntry, 0, len(tasks))
		completed := 0
		for _, t := range tasks {
			entries = append(entries, tui.TaskEntry{
				ID:     t.ID,
				Title:  t.Title,
				Status: t.Status,
			})
			if t.Status == types.StatusPassed || t.Status == types.StatusSkipped {
				completed++
			}
		}
		m = m.AddDAGTasks(entries)
		m = m.SetDAGProgress(completed, len(tasks))
	}

	p := tea.NewProgram(m, tea.WithAltScreen())

	go func() {
		_ = orc.Run(ctx, project)
		close(events)
	}()

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}

func dryRun(tasksPath, project string) error {
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return fmt.Errorf("parsing tasks: %w", err)
	}

	d := dag.NewDAG(tasks)
	if err := d.Validate(); err != nil {
		return fmt.Errorf("validating DAG: %w", err)
	}

	order, err := d.Resolve()
	if err != nil {
		return fmt.Errorf("resolving DAG: %w", err)
	}

	taskMap := make(map[string]*types.Task, len(tasks))
	for i := range tasks {
		taskMap[tasks[i].ID] = &tasks[i]
	}

	fmt.Printf("Dry run for project: %s\n\n", project)
	fmt.Println("Execution order:")

	pending := 0
	for _, id := range order {
		t := taskMap[id]
		emoji := statusEmoji(t.Status)
		marker := " "
		if t.Status == types.StatusPending {
			marker = "→"
			pending++
		}
		fmt.Printf("  %s %s %s — %s [%s]\n", marker, emoji, t.ID, t.Title, t.Status)
	}

	fmt.Printf("\n%d task(s) would be executed\n", pending)
	return nil
}

func drainEvents(events <-chan orchestrator.Event) {
	for ev := range events {
		switch ev.Type {
		case orchestrator.EventTaskStart:
			log.Info("task started", "task", ev.TaskID, "attempt", ev.Attempt)
		case orchestrator.EventTaskComplete:
			if ev.Status == types.StatusPassed {
				log.Info("task passed", "task", ev.TaskID, "cost", fmt.Sprintf("$%.2f", ev.CostUSD))
			} else {
				log.Warn("task failed", "task", ev.TaskID, "message", ev.Message)
			}
		case orchestrator.EventReviewStart:
			log.Info("reviewing", "task", ev.TaskID)
		case orchestrator.EventReviewResult:
			log.Info("review result", "task", ev.TaskID, "verdict", ev.Message)
		case orchestrator.EventCheckpoint:
			log.Info("checkpoint", "task", ev.TaskID)
		case orchestrator.EventRetry:
			log.Warn("retrying", "task", ev.TaskID, "attempt", ev.Attempt)
		case orchestrator.EventPlanStart:
			log.Info("planning started")
		case orchestrator.EventPlanComplete:
			log.Info("planning completed")
		case orchestrator.EventDAGResolved:
			log.Info("DAG resolved", "tasks", ev.Total)
		case orchestrator.EventDone:
			log.Info("all tasks completed")
		case orchestrator.EventSandboxPrepare:
			log.Info("sandbox preparing")
		case orchestrator.EventSandboxCleanup:
			log.Info("sandbox cleanup")
		case orchestrator.EventInsight:
			if ev.Insight != nil {
				fmt.Printf("\n✨ Insight: %d tasks of type %q completed without a dedicated agent.\n", ev.Insight.Count, ev.Insight.TaskType)
				fmt.Printf("   A suggested agent prompt was saved to: .corvex/insights/%s-agent-suggestion.md\n", ev.Insight.TaskType)
				fmt.Printf("   To activate it: mv .corvex/insights/%s-agent-suggestion.md %s\n\n", ev.Insight.TaskType, ev.Insight.SuggestedPath)
			}
		case orchestrator.EventError:
			log.Error("error", "message", ev.Message)
		}
	}
}

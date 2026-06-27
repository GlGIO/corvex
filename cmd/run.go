package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/giovannialves/corvex/internal/activity"
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
	runTask      string
	runSingle    bool
	runDryRun    bool
	runPlain     bool
	flagValidate bool
	runAB        string
	runNoReplan  bool
	runHere       bool
	runForce      bool
	runYes        bool
	runSkipDoctor bool
)

var runCmd = &cobra.Command{
	Use:               "run <project>",
	Short:             "Execute pending tasks for a project",
	Long:              "Run the orchestration loop: DAG resolve → Worker → Reviewer → checkpoint → next.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeProjectArg,
	RunE:              runRun,
}

func init() {
	runCmd.Flags().StringVar(&runTask, "task", "", "run only a specific task (e.g. S03)")
	runCmd.Flags().BoolVar(&runSingle, "single", false, "run only the next pending task")
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "show execution plan without running")
	runCmd.Flags().BoolVar(&runPlain, "plain", false, "disable TUI, use plain log output")
	runCmd.Flags().BoolVar(&flagValidate, "validate", false, "run integration validation after all tasks complete")
	runCmd.Flags().StringVar(&runAB, "ab", "", "A/B run two models against one task (e.g. --ab sonnet,opus); requires --task")
	runCmd.Flags().BoolVar(&runNoReplan, "no-replan", false, "fail if spec.md drifted instead of auto-regenerating tasks.md (protects manual edits)")
	runCmd.Flags().BoolVar(&runHere, "here", false, "run from the current directory even when a worktree exists for this project (rarely correct — usually you want to cd into the worktree)")
	runCmd.Flags().BoolVar(&runForce, "force", false, "run even if the working tree is dirty, discarding uncommitted changes first (DESTRUCTIVE)")
	runCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "skip the cost-preview confirmation prompt (for CI/scripts)")
	runCmd.Flags().BoolVar(&runSkipDoctor, "skip-doctor", false, "skip the pre-run config checks (corvex doctor)")
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

	// Fail early with a helpful message when the project does not exist.
	pDir := projectDir(workDir, project)
	if _, err1 := os.Stat(filepath.Join(pDir, "spec.md")); os.IsNotExist(err1) {
		if _, err2 := os.Stat(filepath.Join(pDir, "tasks.md")); os.IsNotExist(err2) {
			var sb strings.Builder
			fmt.Fprintf(&sb, "project %q not found", project)
			if names := projectNames(workDir); len(names) > 0 {
				fmt.Fprintf(&sb, "\n\navailable projects: %s", strings.Join(names, ", "))
			}
			if suggestion := suggestProject(workDir, project); suggestion != "" {
				fmt.Fprintf(&sb, "\n\ndid you mean %q?", suggestion)
			}
			return fmt.Errorf("%s", sb.String())
		}
	}

	// `corvex start <proj>` creates a sibling worktree at <repo>-<proj>.
	// If that worktree exists but the user is invoking `run` from somewhere
	// else (typically the main repo where they ran `start`), the run would
	// write all generated code to the wrong branch and orphan the worktree.
	// Refuse with an actionable message; `--here` is the escape hatch when
	// the user really means to run from the current directory (e.g. the
	// worktree is a leftover from an abandoned experiment).
	if !runHere {
		if wt := findProjectWorktree(workDir, project); wt != "" {
			absWork, _ := filepath.Abs(workDir)
			absWT, _ := filepath.Abs(wt)
			if absWork != absWT {
				return fmt.Errorf("worktree for project %q exists at %s, but you are running from %s.\nThis would write code to the wrong branch and bypass the worktree.\n\n→ cd %s && corvex run %s\n\nOr pass --here to run from the current directory anyway", project, absWT, absWork, absWT, project)
			}
		}
	}

	if runDryRun {
		pDir := projectDir(workDir, project)
		tasksPath := filepath.Join(pDir, "tasks.md")
		return dryRun(tasksPath, project)
	}

	// Pre-run config gate: fail fast on misconfiguration the way `corvex doctor`
	// would, before spending any tokens. Warnings don't block; --skip-doctor
	// bypasses entirely.
	if !runSkipDoctor {
		if gateErr := doctorGate(cfg, workDir); gateErr != nil {
			return gateErr
		}
	}

	// Cost preview + confirmation: corvex run spends real money. Show what will
	// run and the ceilings, then confirm on an interactive TTY (auto-proceed for
	// --yes or non-TTY/CI so pipes don't hang).
	fmt.Fprintln(os.Stderr, runPreview(workDir, project, cfg))
	if !confirmRun() {
		return fmt.Errorf("aborted by user")
	}

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	sb := sandboxpkg.NewSandbox(cfg.Sandbox)

	events := make(chan orchestrator.Event, 256)
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
		NoReplan:   runNoReplan,
		Force:      runForce,
	})

	if !runPlain && isInteractive() {
		return runWithTUI(ctx, orc, events, commands, cancel, project, workDir)
	}

	noColor, _ := cmd.Flags().GetBool("no-color")
	noColor = noColor || os.Getenv("NO_COLOR") != "" || !isInteractive()
	quiet, _ := cmd.Flags().GetBool("quiet")
	renderer := NewPlainRenderer(os.Stdout, noColor, quiet)
	go renderer.Drain(events)

	if err := orc.Run(ctx, project); err != nil {
		return fmt.Errorf("run failed: %w", err)
	}

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
		// Look up historical per-task metrics from activity.jsonl so already-
		// PASSED tasks render with their real duration (not "0s") and the
		// header shows the accumulated cost (not "$0.00"). On a fresh project
		// the ledger is empty and PerTask is nil — entries below short-circuit
		// safely.
		summary, _ := activity.Summarize(workDir, project)

		entries := make([]tui.TaskEntry, 0, len(tasks))
		completed := 0
		for _, t := range tasks {
			entry := tui.TaskEntry{
				ID:     t.ID,
				Title:  t.Title,
				Status: t.Status,
			}
			if metric, ok := summary.PerTask[t.ID]; ok && t.Status == types.StatusPassed {
				entry.Duration = time.Duration(metric.DurationMs) * time.Millisecond
			}
			entries = append(entries, entry)
			if t.Status == types.StatusPassed || t.Status == types.StatusSkipped {
				completed++
			}
		}
		m = m.AddDAGTasks(entries)
		m = m.SetDAGProgress(completed, len(tasks))
		// Cumulative cost/tokens from previous runs. New EventTaskComplete
		// events accumulate on top during this run.
		m = m.SeedStatusTotals(summary.TotalTokensIn, summary.TotalTokensOut, summary.TotalCostUSD)
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


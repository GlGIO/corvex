package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

func loadConfig() (*config.Config, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("getting working directory: %w", err)
	}

	configPath := filepath.Join(wd, ".corvex", "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return config.Default(), wd, nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("loading config: %w", err)
	}

	return cfg, wd, nil
}

func projectDir(workDir, project string) string {
	return filepath.Join(workDir, ".corvex", "tasks", project)
}

// findGitRoot walks up from start until it finds a directory containing .git.
func findGitRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no .git found above %s", start)
		}
		abs = parent
	}
}

// worktreePath returns the sibling path convention used by `corvex start`:
// <parent-of-repo>/<repo-name>-<feature>.
func worktreePath(gitRoot, feature string) string {
	return filepath.Join(filepath.Dir(gitRoot), filepath.Base(gitRoot)+"-"+feature)
}

// findProjectWorktree returns the conventional worktree path for a project if
// the directory already exists, or empty string otherwise. workDir can be any
// path inside (or equal to) the git working tree; findGitRoot walks up to the
// real root before computing the sibling.
func findProjectWorktree(workDir, project string) string {
	gitRoot, err := findGitRoot(workDir)
	if err != nil {
		return ""
	}
	wt := worktreePath(gitRoot, project)
	info, err := os.Stat(wt)
	if err != nil || !info.IsDir() {
		return ""
	}
	return wt
}

// checkWorktreeMismatch refuses to operate from the main repo when a worktree
// for the project exists elsewhere — running there would write to the wrong
// branch and bypass the worktree. `here` (the command's --here flag) overrides.
// cmdName tailors the hint (e.g. "run", "plan").
func checkWorktreeMismatch(workDir, project, cmdName string, here bool) error {
	if here {
		return nil
	}
	wt := findProjectWorktree(workDir, project)
	if wt == "" {
		return nil
	}
	absWork, _ := filepath.Abs(workDir)
	absWT, _ := filepath.Abs(wt)
	if absWork == absWT {
		return nil
	}
	return fmt.Errorf("worktree for project %q exists at %s, but you are running from %s.\nThis would use the wrong branch/state and bypass the worktree.\n\n→ cd %s && corvex %s %s\n\nOr pass --here to use the current directory anyway", project, absWT, absWork, absWT, cmdName, project)
}

// linkWorktreePaths symlinks the configured repo-relative paths from the main
// repo (gitRoot) into the worktree (wtPath). It is idempotent: missing sources
// are skipped, and an existing destination is never overwritten. Used by
// `corvex start` to bring gitignored state (deps, dotenv) the checkout omits.
func linkWorktreePaths(gitRoot, wtPath string, paths []string) {
	for _, rel := range paths {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		src := filepath.Join(gitRoot, rel)
		dst := filepath.Join(wtPath, rel)
		if _, err := os.Lstat(dst); err == nil {
			continue // already present — don't clobber
		}
		if _, err := os.Stat(src); err != nil {
			continue // nothing to link from
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			log.Warn("worktree link: mkdir", "path", rel, "err", err)
			continue
		}
		if err := os.Symlink(src, dst); err != nil {
			log.Warn("worktree link", "path", rel, "err", err)
			continue
		}
		log.Info("linked into worktree", "path", rel)
	}
}

// completeProjectArg is the ValidArgsFunction for commands whose first positional
// argument is <project>. It returns project names only when completing the first
// arg; subsequent args (e.g. <task> in reset/logs) receive no completion.
func completeProjectArg(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	_, workDir, err := loadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return projectNames(workDir), cobra.ShellCompDirectiveNoFileComp
}

func requireCorvexDir(workDir string) error {
	corvexDir := filepath.Join(workDir, ".corvex")

	// Lstat doesn't follow symlinks — distinguish "not there at all" from
	// "broken symlink pointing somewhere that no longer exists" so users in
	// a worktree get a more actionable hint than "run corvex init".
	info, lstatErr := os.Lstat(corvexDir)
	if os.IsNotExist(lstatErr) {
		return fmt.Errorf(".corvex directory not found in %s — run 'corvex init' first (or if this is a worktree, recreate the symlink: ln -s <main-repo>/.corvex .corvex)", workDir)
	}
	if lstatErr != nil {
		return fmt.Errorf("checking .corvex: %w", lstatErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if _, err := os.Stat(corvexDir); err != nil {
			return fmt.Errorf(".corvex is a symlink but points to a missing target (%w) — recreate it: rm %s && ln -s <main-repo>/.corvex %s", err, corvexDir, corvexDir)
		}
	}
	return nil
}

// projectNames returns names of directories under .corvex/tasks/ that contain
// spec.md or tasks.md.
func projectNames(workDir string) []string {
	tasksDir := filepath.Join(workDir, ".corvex", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, err := os.Stat(filepath.Join(tasksDir, name, "spec.md")); err == nil {
			names = append(names, name)
			continue
		}
		if _, err := os.Stat(filepath.Join(tasksDir, name, "tasks.md")); err == nil {
			names = append(names, name)
		}
	}
	return names
}

// suggestProject returns the closest project name to name via case-insensitive
// prefix/substring match or Levenshtein distance <= 2, or "" if none is close.
func suggestProject(workDir, name string) string {
	names := projectNames(workDir)
	lower := strings.ToLower(name)
	for _, n := range names {
		nl := strings.ToLower(n)
		if strings.HasPrefix(nl, lower) || strings.Contains(nl, lower) {
			return n
		}
	}
	best, bestDist := "", 3
	for _, n := range names {
		if d := levenshtein(strings.ToLower(n), lower); d <= 2 && d < bestDist {
			bestDist = d
			best = n
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := range dp[0] {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			if ra[i-1] == rb[j-1] {
				dp[i][j] = dp[i-1][j-1]
			} else {
				dp[i][j] = 1 + min(dp[i-1][j], min(dp[i][j-1], dp[i-1][j-1]))
			}
		}
	}
	return dp[la][lb]
}

func statusEmoji(s types.TaskStatus) string {
	switch s {
	case types.StatusPending:
		return "⬜"
	case types.StatusRunning:
		return "🔄"
	case types.StatusPassed:
		return "✅"
	case types.StatusFailed:
		return "❌"
	case types.StatusSkipped:
		return "⏭️"
	default:
		return "?"
	}
}

// addJSONFlag registers a --json boolean flag on cmd and returns a pointer to
// the flag value. Commands check *jsonFlag before printing human output and
// take an early-return JSON branch instead.
func addJSONFlag(cmd *cobra.Command) *bool {
	var b bool
	cmd.Flags().BoolVar(&b, "json", false, "print machine-readable JSON instead of formatted output")
	return &b
}

// printJSON marshals v as indented JSON and writes it to w followed by a
// newline. Human/log output must be suppressed before calling this.
func printJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JSON: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

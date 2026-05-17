package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:   "review [project]",
	Short: "List pending escalations awaiting human review",
	Long: `Show open escalations written by the Reviewer when a task has repeatedly
failed with the same category. Each entry is a markdown file in
.corvex/escalations/. Resolve the underlying issue, delete the file, and
re-run "corvex run" to retry the task.

If a project name is given, only escalations for that project are shown.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runReview,
}

func init() {
	rootCmd.AddCommand(reviewCmd)
}

func runReview(_ *cobra.Command, args []string) error {
	_, workDir, err := loadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	dir := filepath.Join(workDir, ".corvex", "escalations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No pending escalations.")
			return nil
		}
		return fmt.Errorf("reading escalations directory: %w", err)
	}

	filter := ""
	if len(args) == 1 {
		filter = args[0]
	}

	var matched []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if filter != "" && !strings.HasPrefix(name, filter+"-") {
			continue
		}
		matched = append(matched, name)
	}
	sort.Strings(matched)

	if len(matched) == 0 {
		if filter != "" {
			fmt.Printf("No pending escalations for project %q.\n", filter)
		} else {
			fmt.Println("No pending escalations.")
		}
		return nil
	}

	fmt.Printf("Pending escalations (%d):\n\n", len(matched))
	for _, name := range matched {
		path := filepath.Join(dir, name)
		fmt.Printf("• %s\n", path)

		// Surface the first two lines of context so users can triage without
		// opening each file.
		if data, readErr := os.ReadFile(path); readErr == nil {
			lines := strings.SplitN(string(data), "\n", 6)
			for i, line := range lines {
				if i >= 4 || strings.TrimSpace(line) == "" {
					continue
				}
				fmt.Printf("    %s\n", line)
			}
		}
		fmt.Println()
	}

	return nil
}

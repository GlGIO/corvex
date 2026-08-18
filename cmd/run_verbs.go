package cmd

import (
	"os"
	"strings"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// The read verbs of the `run` noun. Every one of them is a second process
// looking at somebody else's work: they own no run, write nothing, and take
// their identity from the global index (F1) rather than from the current
// directory — which is what makes `corvex run show run_8f21` answer from any
// repository on the machine.

func runRunList(_ *cobra.Command, _ []string) error {
	if runListProjects {
		return runProjectRows()
	}

	repo, err := resolveRepoFilter()
	if err != nil {
		return err
	}
	rows, err := ops.RunLister{}.ListRuns(ops.RunListOptions{Since: runListSince, Repo: repo})
	if err != nil {
		return err
	}
	if *runListJSON {
		return printJSON(os.Stdout, rows)
	}
	renderRunList(rows, runListSince, repo != "")
	return nil
}

func runProjectRows() error {
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}
	rows, err := ops.RunLister{}.ProjectRows(workDir)
	if err != nil {
		return err
	}
	if *runListJSON {
		return printJSON(os.Stdout, rows)
	}
	renderProjectRows(rows)
	return nil
}

func runRunShow(_ *cobra.Command, args []string) error {
	workDir, err := showWorkDir(args[0])
	if err != nil {
		return err
	}
	report, err := ops.RunLister{}.LoadRunReport(workDir, args[0], strings.ToUpper(runShowStep))
	if err != nil {
		return err
	}
	if *runShowJSON {
		return printJSON(os.Stdout, report)
	}
	renderRunReport(report)
	return nil
}

// showWorkDir resolves where to read from. A run id needs no local workspace —
// the index knows which repository the run belongs to, and refusing to answer
// because the user happens to stand in another directory would make ids useless
// exactly where they matter. A project name is local by definition.
func showWorkDir(arg string) (string, error) {
	if ops.IsRunID(arg) {
		return "", nil
	}
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return "", err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return "", err
	}
	return workDir, nil
}

// resolveRepoFilter turns `--repo .` into this repository's path and leaves an
// explicit path alone.
func resolveRepoFilter() (string, error) {
	if runListRepo == "" {
		return "", nil
	}
	if runListRepo != "." {
		return runListRepo, nil
	}
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return "", err
	}
	return workDir, nil
}

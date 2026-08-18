package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/giovannialves/corvex/internal/ops"
)

// The escalation half of the gate verbs (F3, D6).
//
// An escalation is addressed by project and step because that is what its file
// name has always encoded, and it is answered in the repository that holds it:
// project names are not unique across repositories, and resolving one from the
// wrong tree would delete a file somebody else's run wrote.

func showEscalation(project string) error {
	workDir, err := workspaceDir()
	if err != nil {
		return err
	}
	item, err := ops.FindEscalation(workDir, project, requireStep())
	if err != nil {
		return err
	}
	if *gateShowJSON {
		return printJSON(os.Stdout, item)
	}
	body, rerr := os.ReadFile(item.Path)
	if rerr != nil {
		return rerr
	}
	fmt.Printf("escalation  ·  %s  ·  step %s\n%s\n\n%s\n\n", item.Project, item.Step, item.Path, strings.TrimRight(string(body), "\n"))
	fmt.Printf("  → corvex gate approve %s --step %s   (fixed; run it again)\n", item.Project, item.Step)
	fmt.Printf("  → corvex gate reject  %s --step %s   (not fixing it; leave the step failed)\n", item.Project, item.Step)
	return nil
}

func closeEscalation(project string, retry bool) error {
	workDir, err := workspaceDir()
	if err != nil {
		return err
	}
	item, err := ops.ResolveEscalation(workDir, project, requireStep(), retry)
	if err != nil {
		return err
	}
	if retry {
		fmt.Printf("Closed the escalation for %s step %s — the step is PENDING again.\n", item.Project, item.Step)
		fmt.Printf("  → corvex run retry %s --step %s\n", item.Project, item.Step)
		return nil
	}
	fmt.Printf("Closed the escalation for %s step %s — the step stays FAILED.\n", item.Project, item.Step)
	return nil
}

// requireStep exists because --step is optional for a gate (one open gate needs
// no naming) and mandatory for an escalation (the file name IS project+step).
// Returning the empty string produces the "no escalation for ..." error, which
// already prints the path it looked for.
func requireStep() string { return strings.TrimSpace(gateStep) }

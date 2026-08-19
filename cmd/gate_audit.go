package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/giovannialves/corvex/internal/ops"
)

// `gate audit` — the sensor over the sensors (roadmap risk 1).
//
// # Why a verb on `gate` and not something new
//
// F3's growth rule, argued in full at the top of gate_ack.go: no fourth noun, no
// free-standing verb. This is a read of the gates, so it belongs on the noun that
// already owns them, next to `list`, `show`, `ack`, `approve`, `reject`.
//
// # Why not a column on `inspect --json`
//
// That was the cheap option and it is refused on measured grounds — the full
// argument, with the three ledger defects that make it wrong rather than merely
// awkward, is at the top of internal/ops/gate_audit.go. Short version: the ledger
// flattens `rejected` and `expired` into one FAILED, the `--approve-gates` path
// writes a decision with no duration at all, and there is no reading mark
// anywhere in it — so the two numbers risk 1 asks for cannot be computed from it,
// and the third (the read→decide gap) cannot even be expressed.
//
// # Where the policy lives
//
// In argv, not in the data. `--suspect-under` is the "approved in four seconds"
// line, and it only ever changes what the human render SAYS. `--json` carries
// counts and milliseconds and no verdict about them, because the threshold is an
// opinion this month and would be a fact next month if it were a field.
var gateAuditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Measure the gates themselves: approval latency, rejection rate, and the read→decide gap",
	Long: "Audit the gates instead of what they guard.\n\n" +
		"Three numbers per gate: how long it waited for an answer, how often the answer was no, " +
		"and — the sharp one — how long passed between acknowledging the required reading and " +
		"deciding. A gap of exactly zero means both happened in one command, which is a keystroke " +
		"rather than a review.\n\n" +
		"It reads the gate files under .corvex/runs/gates/, not the activity ledger: the ledger " +
		"cannot tell a rejection from an expiry, records no duration for --approve-gates, and has " +
		"no reading marks at all. Gates auto-approved by --approve-gates never appear here, because " +
		"no gate file is written for them — policy approval is a declared choice, not a fast human.\n\n" +
		"Expiry is reported beside the rejection rate and never inside it: nobody showed up is not " +
		"the same answer as no.",
	Args: cobra.NoArgs,
	RunE: runGateAudit,
}

func runGateAudit(cmd *cobra.Command, _ []string) error {
	since, err := ops.ParseWindow(gateAuditSince)
	if err != nil {
		return err
	}
	if gateAuditAll {
		// Refused rather than silently preferring one: with a population this
		// small `--all` is the flag that makes the audit meaningful at all, and a
		// script that passes both deserves to be told which window it lost.
		if cmd.Flags().Changed("since") {
			return fmt.Errorf("--all and --since %s disagree about the window: "+
				"--all is every gate ever recorded, --since narrows it — pass one", gateAuditSince)
		}
		since = 0
	}
	repo, err := gateAuditRepoFilter()
	if err != nil {
		return err
	}
	audit, err := (ops.GateLister{}).LoadGateAudit(optionalWorkspaceDir(), ops.GateAuditOptions{
		Since: since,
		Repo:  repo,
	})
	if err != nil {
		return err
	}
	if *gateAuditJSON {
		return printJSON(os.Stdout, audit)
	}
	renderGateAudit(audit, gateAuditSuspect)
	return nil
}

// gateAuditRepoFilter turns `--repo .` into this repository's path, exactly as
// `run list --repo .` does.
//
// It repeats three lines of resolveRepoFilter rather than calling it, because
// that one closes over `run list`'s own flag variable — sharing a flag between
// two commands is how a filter ends up narrowing the listing nobody asked to
// narrow.
func gateAuditRepoFilter() (string, error) {
	if gateAuditRepo != "." {
		return gateAuditRepo, nil
	}
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return "", err
	}
	return workDir, nil
}

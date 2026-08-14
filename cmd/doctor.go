package cmd

import (
	"fmt"
	"os"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

var doctorJSON *bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the config and local environment for common misconfigurations",
	Args:  cobra.NoArgs,
	RunE:  runDoctor,
}

func init() {
	doctorJSON = addJSONFlag(doctorCmd)
	rootCmd.AddCommand(doctorCmd)
}

// statusIcon and statusWord are the two renderings of a verdict: the glyph for
// the human report and the word for --json. The verdict itself is ops.CheckStatus
// — deciding it is ops' job, spelling it for a terminal is this package's.
func statusIcon(s ops.CheckStatus) string {
	switch s {
	case ops.CheckPass:
		return "✓"
	case ops.CheckWarn:
		return "⚠"
	default:
		return "✗"
	}
}

func statusWord(s ops.CheckStatus) string {
	switch s {
	case ops.CheckPass:
		return "pass"
	case ops.CheckWarn:
		return "warn"
	default:
		return "fail"
	}
}

type checkJSON struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type doctorOutput struct {
	Checks   []checkJSON `json:"checks"`
	Passed   int         `json:"passed"`
	Warnings int         `json:"warnings"`
	Failed   int         `json:"failed"`
}

func runDoctor(_ *cobra.Command, _ []string) error {
	cfg, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}

	report := ops.Doctor(cfg, workDir)

	if doctorJSON != nil && *doctorJSON {
		if err := printDoctorJSON(report); err != nil {
			return err
		}
		return doctorExitErr(report)
	}

	for _, r := range report.Checks {
		fmt.Printf("%s %s: %s\n", statusIcon(r.Status), r.Name, r.Message)
	}

	total := report.Passed + report.Warnings + report.Failed
	fmt.Printf("doctor: %d checks, %d passed, %d warnings, %d failed\n", total, report.Passed, report.Warnings, report.Failed)

	return doctorExitErr(report)
}

func printDoctorJSON(report ops.DoctorReport) error {
	checks := make([]checkJSON, len(report.Checks))
	for i, r := range report.Checks {
		checks[i] = checkJSON{
			Name:    r.Name,
			Status:  statusWord(r.Status),
			Message: r.Message,
		}
	}
	return printJSON(os.Stdout, doctorOutput{
		Checks:   checks,
		Passed:   report.Passed,
		Warnings: report.Warnings,
		Failed:   report.Failed,
	})
}

func doctorExitErr(report ops.DoctorReport) error {
	if report.Failed > 0 {
		return fmt.Errorf("doctor: %d check(s) failed", report.Failed)
	}
	return nil
}

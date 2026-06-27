package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the config and local environment for common misconfigurations",
	Args:  cobra.NoArgs,
	RunE:  runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

type checkStatus int

const (
	checkPass checkStatus = iota
	checkWarn
	checkFail
)

type checkResult struct {
	name   string
	status checkStatus
	msg    string
}

func (r checkResult) prefix() string {
	switch r.status {
	case checkPass:
		return "✓"
	case checkWarn:
		return "⚠"
	default:
		return "✗"
	}
}

func runDoctor(_ *cobra.Command, _ []string) error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}

	results := runChecks(cfg)

	var passed, warned, failed int
	for _, r := range results {
		fmt.Printf("%s %s: %s\n", r.prefix(), r.name, r.msg)
		switch r.status {
		case checkPass:
			passed++
		case checkWarn:
			warned++
		case checkFail:
			failed++
		}
	}

	total := passed + warned + failed
	fmt.Printf("doctor: %d checks, %d passed, %d warnings, %d failed\n", total, passed, warned, failed)

	if failed > 0 {
		return fmt.Errorf("doctor: %d check(s) failed", failed)
	}
	return nil
}

func runChecks(cfg *config.Config) []checkResult {
	return []checkResult{
		checkProvider(cfg),
		checkModels(cfg),
		checkSandbox(cfg),
		checkEscalation(cfg),
		checkCostCeilings(cfg),
	}
}

func checkProvider(cfg *config.Config) checkResult {
	known := map[string]bool{"claude-cli": true}
	if !known[cfg.Provider.Default] {
		return checkResult{"provider", checkFail, fmt.Sprintf("unknown provider %q (known: claude-cli)", cfg.Provider.Default)}
	}

	bin := os.Getenv("CORVEX_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return checkResult{"provider", checkFail, fmt.Sprintf("binary %q not found on PATH", bin)}
	}

	return checkResult{"provider", checkPass, fmt.Sprintf("provider=%s binary=%s", cfg.Provider.Default, bin)}
}

func checkModels(cfg *config.Config) checkResult {
	var missing []string
	if cfg.Provider.Models.Planner == "" {
		missing = append(missing, "planner")
	}
	if cfg.Provider.Models.Worker == "" {
		missing = append(missing, "worker")
	}
	if cfg.Provider.Models.Reviewer == "" {
		missing = append(missing, "reviewer")
	}
	if len(missing) > 0 {
		return checkResult{"models", checkFail, fmt.Sprintf("missing: %s", strings.Join(missing, ", "))}
	}
	return checkResult{"models", checkPass, fmt.Sprintf("planner=%s worker=%s reviewer=%s",
		cfg.Provider.Models.Planner, cfg.Provider.Models.Worker, cfg.Provider.Models.Reviewer)}
}

func checkSandbox(cfg *config.Config) checkResult {
	validTypes := map[string]bool{"local": true, "docker": true}
	validProfiles := map[string]bool{"nix": true, "devcontainer": true}

	if cfg.Sandbox.Profile != "" {
		if !validProfiles[cfg.Sandbox.Profile] {
			return checkResult{"sandbox", checkFail, fmt.Sprintf("unknown profile %q (known: nix, devcontainer)", cfg.Sandbox.Profile)}
		}
	} else if !validTypes[cfg.Sandbox.Type] {
		return checkResult{"sandbox", checkFail, fmt.Sprintf("unknown type %q (known: local, docker)", cfg.Sandbox.Type)}
	}

	if cfg.Sandbox.Type == "docker" {
		if _, err := exec.LookPath("docker"); err != nil {
			return checkResult{"sandbox", checkWarn, "type=docker but docker binary not found on PATH"}
		}
	}

	if cfg.Sandbox.Mount != "" {
		parts := strings.SplitN(cfg.Sandbox.Mount, ":", 2)
		hostPath := parts[0]
		if !filepath.IsAbs(hostPath) {
			if _, err := filepath.Abs(hostPath); err != nil {
				return checkResult{"sandbox", checkFail, fmt.Sprintf("sandbox.mount %q is not parseable: %v", cfg.Sandbox.Mount, err)}
			}
		}
	}

	return checkResult{"sandbox", checkPass, fmt.Sprintf("type=%s", cfg.Sandbox.Type)}
}

func checkEscalation(cfg *config.Config) checkResult {
	known := map[string]bool{
		"upgrade-model":       true,
		"spawn-investigation": true,
		"human-prompt":        true,
	}

	for name, policy := range cfg.Review.Escalation {
		if !known[policy.Action] {
			return checkResult{"escalation", checkFail, fmt.Sprintf("policy %q has unknown action %q", name, policy.Action)}
		}
		if policy.After < 1 {
			return checkResult{"escalation", checkFail, fmt.Sprintf("policy %q: after=%d must be >= 1", name, policy.After)}
		}
		if policy.Action == "upgrade-model" && policy.To == "" {
			return checkResult{"escalation", checkFail, fmt.Sprintf("policy %q: upgrade-model requires 'to' field", name)}
		}
	}

	return checkResult{"escalation", checkPass, fmt.Sprintf("%d policies", len(cfg.Review.Escalation))}
}

func checkCostCeilings(cfg *config.Config) checkResult {
	var warns []string
	if cfg.Execution.MaxCostUSD == 0 {
		warns = append(warns, "max_cost_usd=0 (no cap)")
	}
	if cfg.Execution.MaxCostPerTaskUSD == 0 {
		warns = append(warns, "max_cost_per_task_usd=0 (no cap)")
	}
	if len(warns) > 0 {
		return checkResult{"cost", checkWarn, strings.Join(warns, "; ")}
	}
	return checkResult{"cost", checkPass, fmt.Sprintf("max_cost_usd=%.2f max_cost_per_task_usd=%.2f",
		cfg.Execution.MaxCostUSD, cfg.Execution.MaxCostPerTaskUSD)}
}

package ops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
)

// CheckProvider verifies the configured provider is one corvex knows how to
// drive and that its CLI binary is reachable on PATH.
func CheckProvider(cfg *config.Config) CheckResult {
	known := map[string]bool{"claude-cli": true}
	if !known[cfg.Provider.Default] {
		return CheckResult{"provider", CheckFail, fmt.Sprintf("unknown provider %q (known: claude-cli)", cfg.Provider.Default)}
	}

	bin := os.Getenv("CORVEX_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return CheckResult{"provider", CheckFail, fmt.Sprintf("binary %q not found on PATH", bin)}
	}

	return CheckResult{"provider", CheckPass, fmt.Sprintf("provider=%s binary=%s", cfg.Provider.Default, bin)}
}

// CheckModels verifies a model is configured for each role.
func CheckModels(cfg *config.Config) CheckResult {
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
		return CheckResult{"models", CheckFail, fmt.Sprintf("missing: %s", strings.Join(missing, ", "))}
	}
	return CheckResult{"models", CheckPass, fmt.Sprintf("planner=%s worker=%s reviewer=%s",
		cfg.Provider.Models.Planner, cfg.Provider.Models.Worker, cfg.Provider.Models.Reviewer)}
}

// CheckSandbox verifies the sandbox type or profile is one corvex supports, that
// docker is present when required, and that a configured mount is parseable.
func CheckSandbox(cfg *config.Config) CheckResult {
	validTypes := map[string]bool{"local": true, "docker": true}
	validProfiles := map[string]bool{"nix": true, "devcontainer": true}

	if cfg.Sandbox.Profile != "" {
		if !validProfiles[cfg.Sandbox.Profile] {
			return CheckResult{"sandbox", CheckFail, fmt.Sprintf("unknown profile %q (known: nix, devcontainer)", cfg.Sandbox.Profile)}
		}
	} else if !validTypes[cfg.Sandbox.Type] {
		return CheckResult{"sandbox", CheckFail, fmt.Sprintf("unknown type %q (known: local, docker)", cfg.Sandbox.Type)}
	}

	if cfg.Sandbox.Type == "docker" {
		if _, err := exec.LookPath("docker"); err != nil {
			return CheckResult{"sandbox", CheckWarn, "type=docker but docker binary not found on PATH"}
		}
	}

	if cfg.Sandbox.Mount != "" {
		parts := strings.SplitN(cfg.Sandbox.Mount, ":", 2)
		hostPath := parts[0]
		if !filepath.IsAbs(hostPath) {
			if _, err := filepath.Abs(hostPath); err != nil {
				return CheckResult{"sandbox", CheckFail, fmt.Sprintf("sandbox.mount %q is not parseable: %v", cfg.Sandbox.Mount, err)}
			}
		}
	}

	return CheckResult{"sandbox", CheckPass, fmt.Sprintf("type=%s", cfg.Sandbox.Type)}
}

// CheckEscalation verifies every escalation policy names a known action, waits
// at least one attempt, and carries the field its action needs.
func CheckEscalation(cfg *config.Config) CheckResult {
	known := map[string]bool{
		"upgrade-model":       true,
		"spawn-investigation": true,
		"human-prompt":        true,
	}

	for name, policy := range cfg.Review.Escalation {
		if !known[policy.Action] {
			return CheckResult{"escalation", CheckFail, fmt.Sprintf("policy %q has unknown action %q", name, policy.Action)}
		}
		if policy.After < 1 {
			return CheckResult{"escalation", CheckFail, fmt.Sprintf("policy %q: after=%d must be >= 1", name, policy.After)}
		}
		if policy.Action == "upgrade-model" && policy.To == "" {
			return CheckResult{"escalation", CheckFail, fmt.Sprintf("policy %q: upgrade-model requires 'to' field", name)}
		}
	}

	return CheckResult{"escalation", CheckPass, fmt.Sprintf("%d policies", len(cfg.Review.Escalation))}
}

// CheckCostCeilings warns when spend is uncapped.
func CheckCostCeilings(cfg *config.Config) CheckResult {
	var warns []string
	if cfg.Execution.MaxCostUSD == 0 {
		warns = append(warns, "max_cost_usd=0 (no cap)")
	}
	if cfg.Execution.MaxCostPerTaskUSD == 0 {
		warns = append(warns, "max_cost_per_task_usd=0 (no cap)")
	}
	if len(warns) > 0 {
		return CheckResult{"cost", CheckWarn, strings.Join(warns, "; ")}
	}
	return CheckResult{"cost", CheckPass, fmt.Sprintf("max_cost_usd=%.2f max_cost_per_task_usd=%.2f",
		cfg.Execution.MaxCostUSD, cfg.Execution.MaxCostPerTaskUSD)}
}

package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/orchestrator"
)

// CheckMCPGitignore warns when MCP servers are configured but the materialised
// .corvex/mcp.json (which can contain secrets) is not gitignored under workDir.
func CheckMCPGitignore(cfg *config.Config, workDir string) CheckResult {
	if len(cfg.Sandbox.MCPServers) == 0 {
		return CheckResult{"mcp-secrets", CheckPass, "no MCP servers configured"}
	}
	candidates := []string{
		filepath.Join(workDir, ".corvex", ".gitignore"),
		filepath.Join(workDir, ".gitignore"),
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "mcp.json") {
			return CheckResult{"mcp-secrets", CheckPass, "mcp.json is gitignored"}
		}
	}
	return CheckResult{"mcp-secrets", CheckWarn, "mcp.json may contain secrets but is not gitignored — add 'mcp.json' to .corvex/.gitignore"}
}

// CheckSkills reports the repo-local skills under workDir that the Worker can
// invoke, and warns when a skill_routing entry points at a skill that isn't
// available.
func CheckSkills(cfg *config.Config, workDir string) CheckResult {
	names := orchestrator.RepoSkills(workDir)
	available := make(map[string]bool, len(names))
	for _, n := range names {
		available[n] = true
	}

	var missing []string
	for taskType, skill := range cfg.SkillRouting {
		if !available[skill] {
			missing = append(missing, fmt.Sprintf("%s→%s", taskType, skill))
		}
	}
	if len(missing) > 0 {
		return CheckResult{"skills", CheckWarn, fmt.Sprintf("skill_routing points at unavailable skill(s): %s (add them under .corvex/skills/)", strings.Join(missing, ", "))}
	}

	if len(names) == 0 {
		return CheckResult{"skills", CheckPass, "no repo skills (add .corvex/skills/<name>/SKILL.md to expose some to the worker)"}
	}
	return CheckResult{"skills", CheckPass, fmt.Sprintf("%d repo skill(s) available to the worker: %s", len(names), strings.Join(names, ", "))}
}

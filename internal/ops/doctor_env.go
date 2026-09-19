package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// CheckFanoutWorktrees reports the checkouts a fan-out left behind.
//
// An isolated fan-out gives each item its own worktree and removes it when the
// item's merge node lands. An item that FAILED keeps its worktree on purpose —
// the work in it is the only copy — and so does a run that was killed or died
// mid-wave. That is the right behaviour and the wrong silence: measured after
// killing a run with three items in flight, `.corvex/worktrees/` held three
// checkouts, `git worktree list` knew about all three, and `corvex doctor`
// reported `7 checks, 7 passed`.
//
// A warning, never a failure: a leftover worktree is frequently the thing
// somebody is about to open. What is wrong is not knowing it is there — after a
// few killed runs, the directory is full of branches holding work nobody
// remembers, and the disk is the only place that knows.
func CheckFanoutWorktrees(_ *config.Config, workDir string) CheckResult {
	root, err := FindGitRoot(workDir)
	if err != nil {
		// Not a git repository: there is no worktree to leak. The other checks
		// already report what that means for a run.
		return CheckResult{"worktrees", CheckPass, "no git repository here"}
	}
	dir := filepath.Join(root, ".corvex", "worktrees")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return CheckResult{"worktrees", CheckPass, "no fan-out worktrees left behind"}
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return CheckResult{"worktrees", CheckPass, "no fan-out worktrees left behind"}
	}
	sort.Strings(names)
	return CheckResult{"worktrees", CheckWarn, fmt.Sprintf(
		"%d fan-out worktree(s) still on disk: %s — they hold the work of items that failed or were interrupted, and nothing else does. Inspect them, then `git worktree remove .corvex/worktrees/<name>`",
		len(names), strings.Join(names, ", "))}
}

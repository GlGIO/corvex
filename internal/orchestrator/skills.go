package orchestrator

import (
	"os"
	"path/filepath"

	charmbraceletlog "github.com/charmbracelet/log"
)

// materializeSkills exposes repo-local skills under `.corvex/skills/<name>/`
// to the Worker by symlinking each into `.claude/skills/<name>` — the location
// the Claude CLI auto-discovers (verified to load in headless `-p` mode). This
// mirrors how the MCP config is materialised before a Worker run.
//
// It never clobbers a pre-existing `.claude/skills/<name>` (a user's own skill
// wins), and returns a cleanup that removes only the links it created.
// `.claude/` is gitignored, so the links don't dirty the tree or survive in
// commits.
func (o *Orchestrator) materializeSkills() func() {
	noop := func() {}

	srcDir := filepath.Join(o.workDir, ".corvex", "skills")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return noop // no .corvex/skills → nothing to expose
	}

	destDir := filepath.Join(o.workDir, ".claude", "skills")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		charmbraceletlog.Warn("creating .claude/skills", "err", err)
		return noop
	}

	var created []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// A valid skill directory must contain SKILL.md.
		if _, serr := os.Stat(filepath.Join(srcDir, name, "SKILL.md")); serr != nil {
			continue
		}
		dest := filepath.Join(destDir, name)
		if _, lerr := os.Lstat(dest); lerr == nil {
			// Already present (user skill or a stale link) — don't clobber.
			continue
		}
		// Relative target so it also resolves inside a bind-mounted container
		// (both .claude and .corvex live under the repo root).
		target := filepath.Join("..", "..", ".corvex", "skills", name)
		if err := os.Symlink(target, dest); err != nil {
			charmbraceletlog.Warn("linking skill", "name", name, "err", err)
			continue
		}
		created = append(created, dest)
	}

	if len(created) > 0 {
		charmbraceletlog.Info("exposed repo skills to the worker", "count", len(created))
	}

	return func() {
		for _, p := range created {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				charmbraceletlog.Warn("removing skill link", "path", p, "err", err)
			}
		}
	}
}

// RepoSkills returns the names of skills the Worker can invoke: directories
// containing a SKILL.md under either `.corvex/skills/` (corvex-managed, linked
// in before a run) or `.claude/skills/` (already discoverable by the Claude
// CLI). De-duplicated. Used by `corvex doctor`.
func RepoSkills(workDir string) []string {
	seen := make(map[string]bool)
	var names []string
	for _, base := range []string{
		filepath.Join(workDir, ".corvex", "skills"),
		filepath.Join(workDir, ".claude", "skills"),
	} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			if _, serr := os.Stat(filepath.Join(base, e.Name(), "SKILL.md")); serr == nil {
				seen[e.Name()] = true
				names = append(names, e.Name())
			}
		}
	}
	return names
}

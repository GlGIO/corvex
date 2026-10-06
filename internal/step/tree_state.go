package step

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// treeState fingerprints the working tree of dir — tracked edits, staged
// changes and untracked files alike — as the id of the tree `git add -A` would
// write. It goes through a throwaway index, so the repository's real index is
// never touched, and it honours .gitignore, so a judge that runs the tests and
// leaves a coverage file or a build cache behind is not mistaken for one that
// edited the work.
//
// The run's own bookkeeping under .corvex/ is left out. The ledger and
// tasks.md are tracked, and the runner writes them while the judge works —
// measured: every characterised run discarded a clean PASS for exactly that.
//
// It reports "" when dir is not a git work tree: no fingerprint means no claim,
// and the caller skips the comparison rather than inventing one.
func treeState(ctx context.Context, dir string) string {
	tmp, err := os.MkdirTemp("", "corvex-tree-*")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(tmp)
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"))
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	// An unborn HEAD has nothing to read; the add below still describes the tree.
	_, _ = run("read-tree", "HEAD")
	if _, err := run("add", "-A", "--", ".", ":(exclude).corvex"); err != nil {
		return ""
	}
	id, err := run("write-tree")
	if err != nil {
		return ""
	}
	return id
}

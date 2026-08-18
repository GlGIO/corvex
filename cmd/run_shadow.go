package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// noticeVerbShadow tells a user whose project is named after a verb why their
// command did something else.
//
// F3's D1 chose precedence — the verb always wins — over "whichever exists",
// because behaviour that depends on the contents of `.corvex/tasks/` means
// `corvex run list` does different things in two repositories. The cost of that
// choice is that the shadowing is INVISIBLE: `corvex run list` lists runs, the
// project called `list` is never mentioned, and nothing says why.
//
// The comment on runCmd claimed a function did this. It did not exist — an audit
// found the claim before a user found the trap. This is that function, and it
// follows D7's rule for the same reason every other notice does: stderr, and
// only on a terminal, so the golden network keeps its bytes.
func noticeVerbShadow(cmd *cobra.Command, _ []string) {
	if !isInteractive() {
		return
	}
	name := cmd.Name()
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return
	}
	if _, statErr := os.Stat(filepath.Join(ops.ProjectDir(workDir, name), "tasks.md")); statErr != nil {
		if _, specErr := os.Stat(filepath.Join(ops.ProjectDir(workDir, name), "spec.md")); specErr != nil {
			return
		}
	}
	fmt.Fprintf(os.Stderr,
		"note: this repository also has a project called %q — the verb wins.\n  → corvex run start %s   (to run the project)\n",
		name, name)
}

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Deprecation, F3's D7: a legacy command keeps working and keeps its bytes, but
// stops advertising itself and tells a human at a terminal where its replacement
// lives.
//
// The notice goes to stderr AND only when stdout is a terminal, for a reason
// that is mechanical rather than aesthetic: the characterization network built
// in F-1 records stdout and stderr in one transcript
// (`characterize_test.go`, `transcript`) precisely so that output cannot migrate
// between the two unnoticed. A notice printed unconditionally would therefore
// rewrite every golden of every command this phase renames — destroying the one
// oracle that proves the renaming changed nothing, in the phase that needs it
// most. The harness runs under a pipe; a human does not. So the human is told,
// and the oracle keeps its bytes.
//
// The consequence is written down rather than hidden: a script never sees the
// notice, and will learn about the removal when it happens (v3). That is why the
// deadline is in the F3 document and in the notice itself.
func deprecate(cmd *cobra.Command, replacement string) {
	cmd.Hidden = true
	previous := cmd.PreRun
	cmd.PreRun = func(c *cobra.Command, args []string) {
		if previous != nil {
			previous(c, args)
		}
		if !isInteractive() {
			return
		}
		fmt.Fprintf(os.Stderr, "deprecated: `corvex %s` will be removed in v3 — use `%s`\n", c.Name(), replacement)
	}
}

func init() {
	deprecate(statusCmd, "corvex run show <project>")
	deprecate(inspectCmd, "corvex run show <project>")
	deprecate(logsCmd, "corvex run show <project> --step <id>")
	deprecate(listCmd, "corvex run list --projects")
	deprecate(resetCmd, "corvex run retry <project> --step <id>")
	deprecate(reviewCmd, "corvex gate list")
	// `recipe` is NOT deprecated: it becomes a noun with verbs. What is
	// deprecated is its bare form (`recipe <name>` compiling), which cannot be
	// hidden without hiding the noun — so it warns from inside its own RunE.
}

// noticeDeprecatedForm is the same notice for a deprecated *form* of a command
// that survives under a new shape.
func noticeDeprecatedForm(form, replacement string) {
	if !isInteractive() {
		return
	}
	fmt.Fprintf(os.Stderr, "deprecated: `corvex %s` will be removed in v3 — use `%s`\n", form, replacement)
}

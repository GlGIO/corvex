package cmd

import (
	"errors"
	"fmt"
	"os"

	charmlog "github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/charmbracelet/lipgloss"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "corvex",
	Short:         "AI-powered development orchestrator",
	Long:          "Corvex orchestrates AI agents to execute complex software development tasks autonomously.",
	Version:       types.Version,
	SilenceUsage:  true,
	SilenceErrors: true, // Execute() prints the error itself; avoid cobra's duplicate
	// Bare `corvex` answers "what is going on" (see root_state.go). Making the
	// root runnable means cobra stops rejecting unknown commands on its own —
	// it hands them here as arguments — so unknownCommand reproduces both the
	// message AND the "Did you mean this?" suggestions, which are the reason a
	// typo does not become a silent no-op.
	Args: unknownCommand,
	RunE: runRootState,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		noColor, _ := cmd.Flags().GetBool("no-color")
		if noColor || os.Getenv("NO_COLOR") != "" {
			lipgloss.SetColorProfile(termenv.Ascii)
			charmlog.SetColorProfile(termenv.Ascii)
		}
	},
}

// unknownCommand keeps `corvex stauts` an error with a suggestion instead of
// silently printing the state screen.
func unknownCommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if suggestion := suggestCommand(cmd, args[0]); suggestion != "" {
		msg += "\n\nDid you mean this?\n\t" + suggestion
	}
	return errors.New(msg)
}

// suggestCommand answers a typo, including a typo of a deprecated command —
// which cobra's SuggestionsFor cannot do, because it only considers commands
// that are still listed.
func suggestCommand(root *cobra.Command, typed string) string {
	names := make([]string, 0, len(root.Commands()))
	for _, c := range root.Commands() {
		names = append(names, c.Name())
	}
	match := ops.SuggestFrom(names, typed)
	if match == "" {
		return ""
	}
	if replacement, ok := deprecatedReplacement[match]; ok {
		return fmt.Sprintf("%s   (deprecated — use `%s`)", match, replacement)
	}
	return match
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.SetVersionTemplate("corvex {{.Version}}\n")
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable color output")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "Suppress per-task progress; print only failures/errors and the final summary")
}

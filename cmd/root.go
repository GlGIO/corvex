package cmd

import (
	"fmt"
	"os"

	charmlog "github.com/charmbracelet/log"
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
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		noColor, _ := cmd.Flags().GetBool("no-color")
		if noColor || os.Getenv("NO_COLOR") != "" {
			lipgloss.SetColorProfile(termenv.Ascii)
			charmlog.SetColorProfile(termenv.Ascii)
		}
	},
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

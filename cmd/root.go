package cmd

import (
	"fmt"
	"os"

	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "corvex",
	Short:        "AI-powered development orchestrator",
	Long:         "Corvex orchestrates AI agents to execute complex software development tasks autonomously.",
	Version:      types.Version,
	SilenceUsage: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.SetVersionTemplate("corvex {{.Version}}\n")
}

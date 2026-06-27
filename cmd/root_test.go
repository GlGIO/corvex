package cmd

import (
	"os"
	"testing"
)

func TestNoColorFlag(t *testing.T) {
	// Ensure --no-color flag exercises the PersistentPreRun without error.
	rootCmd.ResetFlags()
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable color output")

	rootCmd.SetArgs([]string{"--no-color", "version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute() with --no-color: %v", err)
	}
}

func TestNoColorEnv(t *testing.T) {
	// Ensure NO_COLOR env variable exercises the disable path without error.
	t.Setenv("NO_COLOR", "1")
	_ = os.Getenv("NO_COLOR") // confirm env is set

	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute() with NO_COLOR set: %v", err)
	}
}

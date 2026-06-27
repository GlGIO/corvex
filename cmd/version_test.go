package cmd

import (
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestVersionCmd(t *testing.T) {
	output, err := captureStdout(t, func() error {
		versionCmd.Run(versionCmd, nil)
		return nil
	})
	if err != nil {
		t.Fatalf("version command returned error: %v", err)
	}
	if !strings.Contains(output, types.Version) {
		t.Errorf("version output %q does not contain version string %q", output, types.Version)
	}
	expected := "corvex " + types.Version
	if !strings.Contains(output, expected) {
		t.Errorf("version output %q does not contain %q", output, expected)
	}
}

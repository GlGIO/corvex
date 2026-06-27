package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestVersionCmd(t *testing.T) {
	tests := []struct {
		name     string
		jsonFlag bool
		check    func(t *testing.T, output string)
	}{
		{
			name:     "human output unchanged",
			jsonFlag: false,
			check: func(t *testing.T, output string) {
				expected := "corvex " + types.Version
				if !strings.Contains(output, expected) {
					t.Errorf("version output %q does not contain %q", output, expected)
				}
			},
		},
		{
			name:     "json output contains version",
			jsonFlag: true,
			check: func(t *testing.T, output string) {
				var got struct {
					Version string `json:"version"`
				}
				if err := json.Unmarshal([]byte(output), &got); err != nil {
					t.Fatalf("json output is not valid JSON: %v\n%s", err, output)
				}
				if got.Version != types.Version {
					t.Errorf("json version = %q, want %q", got.Version, types.Version)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*versionJSON = tt.jsonFlag
			t.Cleanup(func() { *versionJSON = false })

			output, err := captureStdout(t, func() error {
				return versionCmd.RunE(versionCmd, nil)
			})
			if err != nil {
				t.Fatalf("version command returned error: %v", err)
			}
			tt.check(t, output)
		})
	}
}

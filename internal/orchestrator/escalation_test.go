package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

func TestResolveEscalation(t *testing.T) {
	t.Parallel()

	cfg := config.ReviewConfig{
		Escalation: map[string]config.EscalationPolicy{
			"wrong-approach":    {After: 2, Action: ActionUpgradeModel, To: "opus"},
			"missing-edge-case": {After: 2, Action: ActionSpawnInvestigation},
			"flaky-test":        {After: 3, Action: ActionHumanPrompt},
			"disabled":          {After: 0, Action: ActionUpgradeModel, To: "opus"},
		},
	}

	tests := []struct {
		name      string
		category  string
		count     int
		wantAct   string
		wantUp    string
	}{
		{"empty category never fires", "", 99, "", ""},
		{"unknown category never fires", "stylistic", 5, "", ""},
		{"below threshold", "wrong-approach", 1, "", ""},
		{"at threshold upgrades model", "wrong-approach", 2, ActionUpgradeModel, "opus"},
		{"above threshold still upgrades", "wrong-approach", 3, ActionUpgradeModel, "opus"},
		{"spawn-investigation at threshold", "missing-edge-case", 2, ActionSpawnInvestigation, ""},
		{"human-prompt at threshold", "flaky-test", 3, ActionHumanPrompt, ""},
		{"disabled policy never fires", "disabled", 100, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveEscalation(cfg, tt.category, tt.count)
			if got.Action != tt.wantAct {
				t.Errorf("Action = %q, want %q", got.Action, tt.wantAct)
			}
			if got.UpgradeTo != tt.wantUp {
				t.Errorf("UpgradeTo = %q, want %q", got.UpgradeTo, tt.wantUp)
			}
		})
	}
}

func TestWriteHumanEscalation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := writeHumanEscalation(dir, "my-feature", "S03", "flaky-test", "Tests failed intermittently on CI.")
	if err != nil {
		t.Fatalf("writeHumanEscalation error = %v", err)
	}

	want := filepath.Join(dir, ".corvex", "escalations", "my-feature-S03.md")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read escalation file: %v", err)
	}
	content := string(data)

	for _, expect := range []string{
		"my-feature / S03",
		"flaky-test",
		"Tests failed intermittently on CI.",
		"corvex run",
	} {
		if !strings.Contains(content, expect) {
			t.Errorf("escalation file missing %q\n---\n%s", expect, content)
		}
	}
}

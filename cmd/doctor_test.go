package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

// setupDoctorProject creates a tempdir with .corvex/config.yaml, chdir's into
// it, and returns cleanup. Uses os.Chdir so must not run in parallel.
func setupDoctorProject(t *testing.T, configYAML string) (string, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	corvexDir := filepath.Join(tmpDir, ".corvex")
	if err := os.MkdirAll(corvexDir, 0o755); err != nil {
		t.Fatalf("creating .corvex dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(corvexDir, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatalf("writing config.yaml: %v", err)
	}
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting cwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return tmpDir, func() { os.Chdir(origDir) }
}

func TestRunDoctor(t *testing.T) {
	// os.Chdir is not goroutine-safe; do not call t.Parallel() here.
	validConfig := `
provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`
	tests := []struct {
		name       string
		configYAML string
		claudeBin  string // set CORVEX_CLAUDE_BIN when non-empty
		wantErr    bool
	}{
		{
			// Point at the test binary itself — always executable and on disk.
			name:       "valid config passes",
			configYAML: validConfig,
			claudeBin:  os.Args[0],
			wantErr:    false,
		},
		{
			name: "unknown provider fails",
			configYAML: `
provider:
  default: bad-provider
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`,
			wantErr: true,
		},
		{
			name: "upgrade-model missing to fails",
			configYAML: `
provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
review:
  escalation:
    retry:
      action: upgrade-model
      after: 2
`,
			claudeBin: os.Args[0],
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cleanup := setupDoctorProject(t, tt.configYAML)
			defer cleanup()

			if tt.claudeBin != "" {
				t.Setenv("CORVEX_CLAUDE_BIN", tt.claudeBin)
			}

			_, err := captureStdout(t, func() error {
				return runDoctor(nil, nil)
			})

			if tt.wantErr && err == nil {
				t.Error("runDoctor() expected error but got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("runDoctor() unexpected error: %v", err)
			}
		})
	}
}

func defaultDoctorConfig() *config.Config {
	return &config.Config{
		Provider: config.ProviderConfig{
			Default: "claude-cli",
			Models: config.ModelsConfig{
				Planner:  "opus",
				Worker:   "sonnet",
				Reviewer: "sonnet",
			},
		},
		Sandbox: config.SandboxConfig{Type: "local"},
		Execution: config.ExecutionConfig{
			MaxCostUSD:        25,
			MaxCostPerTaskUSD: 5,
		},
	}
}

func TestCheckProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		provider   string
		wantStatus checkStatus
	}{
		{"unknown provider fails", "not-a-provider", checkFail},
		{"another unknown provider fails", "openai", checkFail},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Provider.Default = tt.provider
			r := checkProvider(cfg)
			if r.status != tt.wantStatus {
				t.Errorf("checkProvider() status = %v, want %v; msg = %q", r.status, tt.wantStatus, r.msg)
			}
		})
	}
}

func TestCheckModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		models     config.ModelsConfig
		wantStatus checkStatus
	}{
		{"all set passes", config.ModelsConfig{Planner: "opus", Worker: "sonnet", Reviewer: "sonnet"}, checkPass},
		{"missing planner fails", config.ModelsConfig{Worker: "sonnet", Reviewer: "sonnet"}, checkFail},
		{"missing worker fails", config.ModelsConfig{Planner: "opus", Reviewer: "sonnet"}, checkFail},
		{"missing reviewer fails", config.ModelsConfig{Planner: "opus", Worker: "sonnet"}, checkFail},
		{"all empty fails", config.ModelsConfig{}, checkFail},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Provider.Models = tt.models
			r := checkModels(cfg)
			if r.status != tt.wantStatus {
				t.Errorf("checkModels() status = %v, want %v; msg = %q", r.status, tt.wantStatus, r.msg)
			}
		})
	}
}

func TestCheckSandbox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sandbox    config.SandboxConfig
		wantStatus checkStatus
	}{
		{"local type passes", config.SandboxConfig{Type: "local"}, checkPass},
		{"unknown type fails", config.SandboxConfig{Type: "lxc"}, checkFail},
		{"nix profile passes", config.SandboxConfig{Type: "", Profile: "nix"}, checkPass},
		{"devcontainer profile passes", config.SandboxConfig{Type: "", Profile: "devcontainer"}, checkPass},
		{"unknown profile fails", config.SandboxConfig{Type: "", Profile: "podman"}, checkFail},
		{"valid mount passes", config.SandboxConfig{Type: "local", Mount: "/host:/container"}, checkPass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Sandbox = tt.sandbox
			r := checkSandbox(cfg)
			if r.status != tt.wantStatus {
				t.Errorf("checkSandbox() status = %v, want %v; msg = %q", r.status, tt.wantStatus, r.msg)
			}
		})
	}
}

func TestCheckEscalation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		escalation map[string]config.EscalationPolicy
		wantStatus checkStatus
	}{
		{"no policies passes", nil, checkPass},
		{"valid upgrade-model passes", map[string]config.EscalationPolicy{
			"retry": {Action: "upgrade-model", After: 2, To: "opus"},
		}, checkPass},
		{"valid spawn-investigation passes", map[string]config.EscalationPolicy{
			"retry": {Action: "spawn-investigation", After: 1},
		}, checkPass},
		{"valid human-prompt passes", map[string]config.EscalationPolicy{
			"retry": {Action: "human-prompt", After: 3},
		}, checkPass},
		{"upgrade-model missing to fails", map[string]config.EscalationPolicy{
			"retry": {Action: "upgrade-model", After: 2},
		}, checkFail},
		{"unknown action fails", map[string]config.EscalationPolicy{
			"retry": {Action: "restart-task", After: 1},
		}, checkFail},
		{"after zero fails", map[string]config.EscalationPolicy{
			"retry": {Action: "human-prompt", After: 0},
		}, checkFail},
		{"after negative fails", map[string]config.EscalationPolicy{
			"retry": {Action: "human-prompt", After: -1},
		}, checkFail},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Review.Escalation = tt.escalation
			r := checkEscalation(cfg)
			if r.status != tt.wantStatus {
				t.Errorf("checkEscalation() status = %v, want %v; msg = %q", r.status, tt.wantStatus, r.msg)
			}
		})
	}
}

func TestCheckCostCeilings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		exec       config.ExecutionConfig
		wantStatus checkStatus
	}{
		{"both set passes", config.ExecutionConfig{MaxCostUSD: 25, MaxCostPerTaskUSD: 5}, checkPass},
		{"max_cost_usd zero warns", config.ExecutionConfig{MaxCostUSD: 0, MaxCostPerTaskUSD: 5}, checkWarn},
		{"max_cost_per_task_usd zero warns", config.ExecutionConfig{MaxCostUSD: 25, MaxCostPerTaskUSD: 0}, checkWarn},
		{"both zero warns", config.ExecutionConfig{MaxCostUSD: 0, MaxCostPerTaskUSD: 0}, checkWarn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Execution = tt.exec
			r := checkCostCeilings(cfg)
			if r.status != tt.wantStatus {
				t.Errorf("checkCostCeilings() status = %v, want %v; msg = %q", r.status, tt.wantStatus, r.msg)
			}
		})
	}
}

func validDoctorConfigYAML() string {
	return `
provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`
}

func TestDoctorJSONShape(t *testing.T) {
	_, cleanup := setupDoctorProject(t, validDoctorConfigYAML())
	defer cleanup()
	t.Setenv("CORVEX_CLAUDE_BIN", os.Args[0])

	b := true
	doctorJSON = &b
	defer func() { f := false; doctorJSON = &f }()

	output, _ := captureStdout(t, func() error {
		return runDoctor(nil, nil)
	})

	var out doctorOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}
	if len(out.Checks) == 0 {
		t.Error("checks array must not be empty")
	}
	for _, c := range out.Checks {
		if c.Name == "" {
			t.Error("check name must not be empty")
		}
		if c.Status != "pass" && c.Status != "warn" && c.Status != "fail" {
			t.Errorf("check %q has unexpected status %q", c.Name, c.Status)
		}
	}
	if out.Passed+out.Warnings+out.Failed != len(out.Checks) {
		t.Errorf("passed+warnings+failed (%d) != len(checks) (%d)", out.Passed+out.Warnings+out.Failed, len(out.Checks))
	}
}

func TestDoctorJSONFailExitCode(t *testing.T) {
	badConfig := `
provider:
  default: bad-provider
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`
	_, cleanup := setupDoctorProject(t, badConfig)
	defer cleanup()

	b := true
	doctorJSON = &b
	defer func() { f := false; doctorJSON = &f }()

	output, err := captureStdout(t, func() error {
		return runDoctor(nil, nil)
	})
	if err == nil {
		t.Error("runDoctor --json with failed check should return non-nil error")
	}

	var out doctorOutput
	if jsonErr := json.Unmarshal([]byte(output), &out); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", jsonErr, output)
	}
	if out.Failed == 0 {
		t.Error("failed count should be > 0 for bad provider config")
	}
}

func TestDoctorJSONHumanUnchanged(t *testing.T) {
	_, cleanup := setupDoctorProject(t, validDoctorConfigYAML())
	defer cleanup()
	t.Setenv("CORVEX_CLAUDE_BIN", os.Args[0])

	f := false
	doctorJSON = &f

	output, err := captureStdout(t, func() error {
		return runDoctor(nil, nil)
	})
	if err != nil {
		t.Fatalf("runDoctor human failed: %v", err)
	}
	if len(output) > 0 && output[0] == '{' {
		t.Error("human output should not start with '{' (looks like JSON)")
	}
	if !strings.Contains(output, "doctor:") {
		t.Error("human output should contain 'doctor:' summary line")
	}
}

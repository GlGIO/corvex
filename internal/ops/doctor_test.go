package ops

// Unit tests for the doctor checks. They lived in cmd/doctor_test.go while the
// checks were still reached through a shim of aliases in cmd/doctor.go; they
// moved down with the code they exercise, assertions unchanged. What stayed in
// cmd/doctor_test.go is the rendering and exit-code half (TestRunDoctor,
// TestDoctorJSONShape, TestDoctorJSONFailExitCode, TestDoctorJSONHumanUnchanged).

import (
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

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
		wantStatus CheckStatus
	}{
		{"unknown provider fails", "not-a-provider", CheckFail},
		{"another unknown provider fails", "openai", CheckFail},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Provider.Default = tt.provider
			r := CheckProvider(cfg)
			if r.Status != tt.wantStatus {
				t.Errorf("CheckProvider() status = %v, want %v; msg = %q", r.Status, tt.wantStatus, r.Message)
			}
		})
	}
}

func TestCheckModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		models     config.ModelsConfig
		wantStatus CheckStatus
	}{
		{"all set passes", config.ModelsConfig{Planner: "opus", Worker: "sonnet", Reviewer: "sonnet"}, CheckPass},
		{"missing planner fails", config.ModelsConfig{Worker: "sonnet", Reviewer: "sonnet"}, CheckFail},
		{"missing worker fails", config.ModelsConfig{Planner: "opus", Reviewer: "sonnet"}, CheckFail},
		{"missing reviewer fails", config.ModelsConfig{Planner: "opus", Worker: "sonnet"}, CheckFail},
		{"all empty fails", config.ModelsConfig{}, CheckFail},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Provider.Models = tt.models
			r := CheckModels(cfg)
			if r.Status != tt.wantStatus {
				t.Errorf("CheckModels() status = %v, want %v; msg = %q", r.Status, tt.wantStatus, r.Message)
			}
		})
	}
}

func TestCheckSandbox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sandbox    config.SandboxConfig
		wantStatus CheckStatus
	}{
		{"local type passes", config.SandboxConfig{Type: "local"}, CheckPass},
		{"unknown type fails", config.SandboxConfig{Type: "lxc"}, CheckFail},
		{"nix profile passes", config.SandboxConfig{Type: "", Profile: "nix"}, CheckPass},
		{"devcontainer profile passes", config.SandboxConfig{Type: "", Profile: "devcontainer"}, CheckPass},
		{"unknown profile fails", config.SandboxConfig{Type: "", Profile: "podman"}, CheckFail},
		{"valid mount passes", config.SandboxConfig{Type: "local", Mount: "/host:/container"}, CheckPass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Sandbox = tt.sandbox
			r := CheckSandbox(cfg)
			if r.Status != tt.wantStatus {
				t.Errorf("CheckSandbox() status = %v, want %v; msg = %q", r.Status, tt.wantStatus, r.Message)
			}
		})
	}
}

func TestCheckEscalation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		escalation map[string]config.EscalationPolicy
		wantStatus CheckStatus
	}{
		{"no policies passes", nil, CheckPass},
		{"valid upgrade-model passes", map[string]config.EscalationPolicy{
			"retry": {Action: "upgrade-model", After: 2, To: "opus"},
		}, CheckPass},
		{"valid spawn-investigation passes", map[string]config.EscalationPolicy{
			"retry": {Action: "spawn-investigation", After: 1},
		}, CheckPass},
		{"valid human-prompt passes", map[string]config.EscalationPolicy{
			"retry": {Action: "human-prompt", After: 3},
		}, CheckPass},
		{"upgrade-model missing to fails", map[string]config.EscalationPolicy{
			"retry": {Action: "upgrade-model", After: 2},
		}, CheckFail},
		{"unknown action fails", map[string]config.EscalationPolicy{
			"retry": {Action: "restart-task", After: 1},
		}, CheckFail},
		{"after zero fails", map[string]config.EscalationPolicy{
			"retry": {Action: "human-prompt", After: 0},
		}, CheckFail},
		{"after negative fails", map[string]config.EscalationPolicy{
			"retry": {Action: "human-prompt", After: -1},
		}, CheckFail},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Review.Escalation = tt.escalation
			r := CheckEscalation(cfg)
			if r.Status != tt.wantStatus {
				t.Errorf("CheckEscalation() status = %v, want %v; msg = %q", r.Status, tt.wantStatus, r.Message)
			}
		})
	}
}

func TestCheckCostCeilings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		exec       config.ExecutionConfig
		wantStatus CheckStatus
	}{
		{"both set passes", config.ExecutionConfig{MaxCostUSD: 25, MaxCostPerTaskUSD: 5}, CheckPass},
		{"max_cost_usd zero warns", config.ExecutionConfig{MaxCostUSD: 0, MaxCostPerTaskUSD: 5}, CheckWarn},
		{"max_cost_per_task_usd zero warns", config.ExecutionConfig{MaxCostUSD: 25, MaxCostPerTaskUSD: 0}, CheckWarn},
		{"both zero warns", config.ExecutionConfig{MaxCostUSD: 0, MaxCostPerTaskUSD: 0}, CheckWarn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultDoctorConfig()
			cfg.Execution = tt.exec
			r := CheckCostCeilings(cfg)
			if r.Status != tt.wantStatus {
				t.Errorf("CheckCostCeilings() status = %v, want %v; msg = %q", r.Status, tt.wantStatus, r.Message)
			}
		})
	}
}

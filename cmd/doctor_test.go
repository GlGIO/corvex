package cmd

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

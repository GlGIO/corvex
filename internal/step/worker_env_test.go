package step

import (
	"context"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

func TestCollectAuthEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA-test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret-test")
	t.Setenv("AWS_SESSION_TOKEN", "token-test")
	t.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("AWS_PROFILE", "dev")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("CORVEX_TASK", "S01")
	t.Setenv("VENDOR_CLI_TOKEN", "should-not-appear")
	t.Setenv("UNRELATED_VAR", "should-not-appear")

	env := collectAuthEnv(config.DefaultEnvAllowlist(), nil)

	expected := map[string]string{
		"ANTHROPIC_API_KEY":       "test-key",
		"CLAUDE_CODE_USE_BEDROCK": "1",
		"AWS_ACCESS_KEY_ID":       "AKIA-test",
		"AWS_SECRET_ACCESS_KEY":   "secret-test",
		"AWS_SESSION_TOKEN":       "token-test",
		"AWS_DEFAULT_REGION":      "us-east-1",
		"AWS_REGION":              "us-west-2",
		"AWS_PROFILE":             "dev",
		"OPENAI_API_KEY":          "sk-test",
		"CORVEX_TASK":             "S01",
	}
	for k, v := range expected {
		if env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, env[k], v)
		}
	}

	if _, ok := env["UNRELATED_VAR"]; ok {
		t.Error("UNRELATED_VAR should not be collected")
	}
	// Vendor-specific credentials are not built in: they are inherited only
	// when the user declares the prefix in sandbox.env_allowlist.
	if _, ok := env["VENDOR_CLI_TOKEN"]; ok {
		t.Error("VENDOR_CLI_TOKEN should not be collected by default")
	}
}

// A prefix the defaults do not know about is inherited once the config that
// this run loaded declares it — no recompile needed to grant a new credential.
func TestCollectAuthEnv_ConfiguredPrefix(t *testing.T) {
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "pat-test")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	undeclared := &config.Config{}
	if got := collectAuthEnv(undeclared.EnvAllowlist(), nil); got["AZURE_DEVOPS_EXT_PAT"] != "" {
		t.Fatalf("AZURE_DEVOPS_EXT_PAT leaked without config: %q", got["AZURE_DEVOPS_EXT_PAT"])
	}

	declared := &config.Config{}
	declared.Sandbox.EnvAllowlist = []string{"AZURE_"}

	env := collectAuthEnv(declared.EnvAllowlist(), nil)
	if env["AZURE_DEVOPS_EXT_PAT"] != "pat-test" {
		t.Errorf("AZURE_DEVOPS_EXT_PAT = %q, want %q", env["AZURE_DEVOPS_EXT_PAT"], "pat-test")
	}
	if env["ANTHROPIC_API_KEY"] != "test-key" {
		t.Error("config allowlist must not displace the built-in defaults")
	}
}

func TestCollectAuthEnv_NoMatch(t *testing.T) {
	t.Setenv("TOTALLY_UNRELATED", "value1")
	t.Setenv("ANOTHER_RANDOM", "value2")

	env := collectAuthEnv(config.DefaultEnvAllowlist(), nil)

	if _, ok := env["TOTALLY_UNRELATED"]; ok {
		t.Error("TOTALLY_UNRELATED should not be in auth env")
	}
	if _, ok := env["ANOTHER_RANDOM"]; ok {
		t.Error("ANOTHER_RANDOM should not be in auth env")
	}
}

// The allowlist is a per-Worker field, not process state: what reaches the
// sandbox is exactly what the config of THIS Worker declared. A second Worker
// built from a config that says nothing sees none of it — the property the old
// process-wide "active allowlist" could not offer.
func TestWorkerForwardsItsOwnAllowlistToSandbox(t *testing.T) {
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "pat-test")

	declared := &config.Config{}
	declared.Sandbox.EnvAllowlist = []string{"AZURE_"}

	envFor := func(allowlist []string) map[string]string {
		sb := &mockSandbox{}
		w := NewWorker(&mockCommandProvider{}, "sonnet", "/tmp", sb, nil, allowlist, config.SecurityConfig{})
		task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}
		if _, err := w.Execute(context.Background(), task, "", nil, "", ""); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		sb.mu.Lock()
		defer sb.mu.Unlock()
		if len(sb.runCalls) != 1 {
			t.Fatalf("sandbox.Run called %d times, want 1", len(sb.runCalls))
		}
		return sb.runCalls[0].Env
	}

	if got := envFor(declared.EnvAllowlist())["AZURE_DEVOPS_EXT_PAT"]; got != "pat-test" {
		t.Errorf("declaring worker: AZURE_DEVOPS_EXT_PAT = %q, want %q", got, "pat-test")
	}
	if got := envFor(config.DefaultEnvAllowlist())["AZURE_DEVOPS_EXT_PAT"]; got != "" {
		t.Errorf("plain worker inherited another config's credential: %q", got)
	}
}

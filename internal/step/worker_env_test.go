package step

import (
	"testing"

	"github.com/giovannialves/corvex/internal/config"
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

	env := collectAuthEnv(config.DefaultEnvAllowlist())

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

// A prefix the defaults do not know about is inherited once the loaded config
// declares it — no recompile needed to grant a new credential.
func TestCollectAuthEnv_ConfiguredPrefix(t *testing.T) {
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "pat-test")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	if got := collectAuthEnv(config.ActiveEnvAllowlist()); got["AZURE_DEVOPS_EXT_PAT"] != "" {
		t.Fatalf("AZURE_DEVOPS_EXT_PAT leaked without config: %q", got["AZURE_DEVOPS_EXT_PAT"])
	}

	t.Cleanup(func() { config.SetActiveEnvAllowlist(nil) })
	config.SetActiveEnvAllowlist([]string{"AZURE_"})

	env := collectAuthEnv(config.ActiveEnvAllowlist())
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

	env := collectAuthEnv(config.ActiveEnvAllowlist())

	if _, ok := env["TOTALLY_UNRELATED"]; ok {
		t.Error("TOTALLY_UNRELATED should not be in auth env")
	}
	if _, ok := env["ANOTHER_RANDOM"]; ok {
		t.Error("ANOTHER_RANDOM should not be in auth env")
	}
}

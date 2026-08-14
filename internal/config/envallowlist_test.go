package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"gopkg.in/yaml.v3"
)

// (a) With no config at all, only the generic provider credentials are
// allowed, and nothing vendor-specific is baked into the binary.
func TestDefaultEnvAllowlist(t *testing.T) {
	got := config.DefaultEnvAllowlist()

	for _, want := range []string{"ANTHROPIC_", "CLAUDE_", "AWS_ACCESS_KEY", "OPENAI_", "CORVEX_"} {
		if !slices.Contains(got, want) {
			t.Errorf("default allowlist missing %q: %v", want, got)
		}
	}
	for _, p := range got {
		if strings.HasPrefix(p, "AZURE") {
			t.Errorf("vendor-specific prefix %q must live in sandbox.env_allowlist, not in the binary", p)
		}
	}
}

func TestDefaultEnvAllowlist_CallerCannotMutateIt(t *testing.T) {
	first := config.DefaultEnvAllowlist()
	first[0] = "MUTATED_"

	if second := config.DefaultEnvAllowlist(); second[0] == "MUTATED_" {
		t.Error("DefaultEnvAllowlist leaks the package slice to callers")
	}
}

// (b) Config adds to the defaults, and (c) it cannot remove one.
func TestResolveEnvAllowlist_ConfigAddsAndCannotRemove(t *testing.T) {
	defaults := config.DefaultEnvAllowlist()
	got := config.ResolveEnvAllowlist([]string{"AZURE_", "GH_TOKEN"})

	// Defaults come first, in their original order, untouched.
	if len(got) < len(defaults) || !slices.Equal(got[:len(defaults)], defaults) {
		t.Fatalf("defaults not preserved as prefix of result:\n got %v\nwant %v...", got, defaults)
	}
	// Extras follow in declaration order.
	if want := append(slices.Clone(defaults), "AZURE_", "GH_TOKEN"); !slices.Equal(got, want) {
		t.Errorf("resolved = %v, want %v", got, want)
	}
}

func TestResolveEnvAllowlist_EmptyExtraIsDefaults(t *testing.T) {
	if got := config.ResolveEnvAllowlist(nil); !slices.Equal(got, config.DefaultEnvAllowlist()) {
		t.Errorf("nil extra changed the allowlist: %v", got)
	}
	if got := config.ResolveEnvAllowlist([]string{}); !slices.Equal(got, config.DefaultEnvAllowlist()) {
		t.Errorf("empty extra changed the allowlist: %v", got)
	}
}

// (c) There is no config value that drops a default — not even naming it.
func TestResolveEnvAllowlist_CannotRemoveDefaultByRedeclaring(t *testing.T) {
	got := config.ResolveEnvAllowlist([]string{"ANTHROPIC_"})
	if !slices.Equal(got, config.DefaultEnvAllowlist()) {
		t.Errorf("redeclaring a default changed the list: %v", got)
	}
}

// (d) A prefix declared twice (or already a default) appears once.
func TestResolveEnvAllowlist_NoDuplicates(t *testing.T) {
	got := config.ResolveEnvAllowlist([]string{"AZURE_", "AZURE_", "CORVEX_", " AZURE_ "})

	count := 0
	for _, p := range got {
		if p == "AZURE_" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("AZURE_ appears %d times in %v, want 1", count, got)
	}
	if len(got) != len(config.DefaultEnvAllowlist())+1 {
		t.Errorf("resolved has %d entries, want %d: %v", len(got), len(config.DefaultEnvAllowlist())+1, got)
	}
}

// An empty prefix would match every variable in the host environment, so it is
// dropped rather than silently widening the sandbox.
func TestResolveEnvAllowlist_DropsEmptyPrefix(t *testing.T) {
	got := config.ResolveEnvAllowlist([]string{"", "   ", "\t"})
	if !slices.Equal(got, config.DefaultEnvAllowlist()) {
		t.Errorf("blank entries widened the allowlist: %v", got)
	}
	if slices.Contains(got, "") {
		t.Error("empty prefix survived — it would match the whole environment")
	}
}

func TestConfigEnvAllowlist(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.EnvAllowlist = []string{"AZURE_"}
	if got := cfg.EnvAllowlist(); !slices.Contains(got, "AZURE_") || !slices.Contains(got, "ANTHROPIC_") {
		t.Errorf("Config.EnvAllowlist() = %v", got)
	}

	var nilCfg *config.Config
	if got := nilCfg.EnvAllowlist(); !slices.Equal(got, config.DefaultEnvAllowlist()) {
		t.Errorf("nil config allowlist = %v, want defaults", got)
	}
}

// (e) End to end: AZURE_ reaches the sandbox only because config.yaml says so.
// The allowlist belongs to the Config that was loaded — there is no process
// state to consult, so a Config nobody configured still allows nothing extra.
func TestLoad_ConfigDeclaredPrefixReachesEnvAllowlist(t *testing.T) {
	if slices.Contains((&config.Config{}).EnvAllowlist(), "AZURE_") {
		t.Fatal("AZURE_ allowed by a config that never declared it")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "project:\n  name: p\nsandbox:\n  type: local\n  env_allowlist:\n    - AZURE_\n    - GH_TOKEN\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := []string{"AZURE_", "GH_TOKEN"}; !slices.Equal(cfg.Sandbox.EnvAllowlist, want) {
		t.Errorf("cfg.Sandbox.EnvAllowlist = %v, want %v", cfg.Sandbox.EnvAllowlist, want)
	}

	effective := cfg.EnvAllowlist()
	for _, want := range []string{"AZURE_", "GH_TOKEN", "ANTHROPIC_"} {
		if !slices.Contains(effective, want) {
			t.Errorf("effective allowlist missing %q: %v", want, effective)
		}
	}
}

// `corvex validate` rewrites config.yaml by marshaling the whole Config, so an
// unset allowlist must stay invisible: it may not add a line to a config the
// user never touched. Declared values must round-trip.
func TestEnvAllowlist_MarshalOmitsEmpty(t *testing.T) {
	out, err := yaml.Marshal(config.Default())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "env_allowlist") {
		t.Errorf("unset allowlist leaked into config.yaml:\n%s", out)
	}

	cfg := config.Default()
	cfg.Sandbox.EnvAllowlist = []string{"AZURE_"}
	out, err = yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "env_allowlist") || !strings.Contains(string(out), "AZURE_") {
		t.Errorf("declared allowlist did not round-trip:\n%s", out)
	}
}

// A config nobody configured — the zero value, or none at all — allows exactly
// the built-in defaults. This is what used to be "before any config is loaded",
// now expressed without process state.
func TestEnvAllowlist_UnconfiguredIsDefaults(t *testing.T) {
	if got := (&config.Config{}).EnvAllowlist(); !slices.Equal(got, config.DefaultEnvAllowlist()) {
		t.Errorf("zero-value config allowlist = %v, want defaults", got)
	}

	var absent *config.Config
	if got := absent.EnvAllowlist(); !slices.Equal(got, config.DefaultEnvAllowlist()) {
		t.Errorf("nil config allowlist = %v, want defaults", got)
	}
}

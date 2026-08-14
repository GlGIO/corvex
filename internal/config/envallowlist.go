package config

import "strings"

// defaultEnvAllowlist holds the environment-variable prefixes a sandboxed
// child process inherits from the host, with no configuration at all.
//
// Only PROVIDER-GENERIC credentials belong here — the ones any Corvex install
// needs to talk to a model. Anything vendor-, cloud- or company-specific
// belongs in `sandbox.env_allowlist` in config.yaml instead: teaching Corvex
// about one more credential must never require recompiling the binary.
var defaultEnvAllowlist = []string{
	"ANTHROPIC_",
	"CLAUDE_",
	"AWS_ACCESS_KEY",
	"AWS_SECRET_ACCESS",
	"AWS_SESSION_TOKEN",
	"AWS_DEFAULT_REGION",
	"AWS_REGION",
	"AWS_PROFILE",
	"OPENAI_",
	"CORVEX_",
}

// DefaultEnvAllowlist returns a copy of the built-in prefixes.
func DefaultEnvAllowlist() []string {
	return ResolveEnvAllowlist(nil)
}

// ResolveEnvAllowlist unions the built-in defaults with extra prefixes coming
// from `sandbox.env_allowlist`.
//
// Config can only ADD. There is no config value that removes a default — a
// user cannot lock the Worker out of its own model credentials by mistake.
// Order is stable: defaults first (in built-in order), then extra in the order
// declared. A prefix already present collapses into its first occurrence, and
// empty / whitespace-only entries are dropped because an empty prefix would
// match every variable in the host environment.
func ResolveEnvAllowlist(extra []string) []string {
	out := make([]string, 0, len(defaultEnvAllowlist)+len(extra))
	seen := make(map[string]struct{}, len(defaultEnvAllowlist)+len(extra))

	add := func(prefix string) {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			return
		}
		if _, dup := seen[prefix]; dup {
			return
		}
		seen[prefix] = struct{}{}
		out = append(out, prefix)
	}

	for _, prefix := range defaultEnvAllowlist {
		add(prefix)
	}
	for _, prefix := range extra {
		add(prefix)
	}
	return out
}

// EnvAllowlist returns the prefixes in effect for this config: the defaults
// plus whatever `sandbox.env_allowlist` declares.
//
// This is the ONLY way to obtain an effective allowlist. There is deliberately
// no process-wide "active" allowlist: the list decides which host CREDENTIALS
// a sandboxed child inherits, so it belongs to the config of the run that is
// asking, and it travels to whoever needs it as an argument. Two runs with
// different configs in one process (the HTTP surface) must not be able to see
// each other's credentials.
func (c *Config) EnvAllowlist() []string {
	if c == nil {
		return DefaultEnvAllowlist()
	}
	return ResolveEnvAllowlist(c.Sandbox.EnvAllowlist)
}

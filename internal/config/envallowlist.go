package config

import (
	"strings"
	"sync"
)

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
func (c *Config) EnvAllowlist() []string {
	if c == nil {
		return DefaultEnvAllowlist()
	}
	return ResolveEnvAllowlist(c.Sandbox.EnvAllowlist)
}

var (
	activeEnvMu        sync.RWMutex
	activeEnvAllowlist []string
)

// SetActiveEnvAllowlist publishes the extra prefixes declared by the loaded
// config as the ones in effect for this process. Load calls it, so a component
// that hands the host environment to a sandboxed child reads the user's
// configuration without every constructor in between having to carry the list.
//
// extra is unioned with the defaults, so this can never narrow the allowlist.
// Passing nil restores the plain defaults.
func SetActiveEnvAllowlist(extra []string) {
	resolved := ResolveEnvAllowlist(extra)
	activeEnvMu.Lock()
	activeEnvAllowlist = resolved
	activeEnvMu.Unlock()
}

// ActiveEnvAllowlist returns the prefixes in effect for this process. Before
// any config is loaded it returns the built-in defaults.
func ActiveEnvAllowlist() []string {
	activeEnvMu.RLock()
	list := activeEnvAllowlist
	activeEnvMu.RUnlock()

	if list == nil {
		return DefaultEnvAllowlist()
	}
	out := make([]string, len(list))
	copy(out, list)
	return out
}

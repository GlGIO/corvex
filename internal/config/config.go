package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Project      ProjectConfig     `yaml:"project"`
	Provider     ProviderConfig    `yaml:"provider"`
	Sandbox      SandboxConfig     `yaml:"sandbox"`
	Execution    ExecutionConfig   `yaml:"execution"`
	Review       ReviewConfig      `yaml:"review"`
	Context      ContextConfig     `yaml:"context"`
	AgentRouting map[string]string `yaml:"agent_routing"`
	// SkillRouting maps a task type to a repo skill name (under .corvex/skills/).
	// When set, the Worker prompt for that type is instructed to use the skill,
	// making skill use intentional rather than opportunistic.
	SkillRouting map[string]string `yaml:"skill_routing"`
	Validate     ValidateConfig    `yaml:"validate"`
	Plan         PlanConfig        `yaml:"plan"`
	Worktree     WorktreeConfig    `yaml:"worktree"`
}

// WorktreeConfig configures worktree setup done by `corvex start`.
type WorktreeConfig struct {
	// Link is a list of repo-relative paths symlinked from the main repo into a
	// new worktree (e.g. node_modules, backend/.env-stg). Use it for gitignored
	// state the checkout doesn't bring — deps, secrets/dotenv — so a fresh
	// worktree can build/run without a manual copy. Missing sources are skipped;
	// existing destinations are never overwritten.
	Link []string `yaml:"link"`
}

// PlanConfig configures the planning step.
type PlanConfig struct {
	// ContextCommand, when set, is a shell command run before the Planner; its
	// stdout is injected into the Planner prompt as external context. Use it to
	// pull a source of truth the read-only Planner can't reach itself — e.g. an
	// Azure DevOps / issue-tracker query, or a `claude -p` that uses a skill.
	ContextCommand string `yaml:"context_command"`
}

type ProjectConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type ProviderConfig struct {
	Default string       `yaml:"default"`
	Models  ModelsConfig `yaml:"models"`
}

type ModelsConfig struct {
	Planner  string `yaml:"planner"`
	Worker   string `yaml:"worker"`
	Reviewer string `yaml:"reviewer"`
}

type SandboxConfig struct {
	Type            string            `yaml:"type"`
	Profile         string            `yaml:"profile"` // "" | "nix" | "devcontainer" — overrides Type when set
	Image           string            `yaml:"image"`
	Mount           string            `yaml:"mount"`
	WorkDir         string            `yaml:"workdir"`
	WorkerExtraArgs []string          `yaml:"worker_extra_args"`
	MCPServers      []MCPServerConfig `yaml:"mcp_servers"`

	// EnvAllowlist declares EXTRA environment-variable name prefixes forwarded
	// from the host process into the sandbox, on top of the built-in ones
	// (ANTHROPIC_, CLAUDE_, AWS_*, OPENAI_, CORVEX_). Use it for credentials
	// Corvex knows nothing about — a cloud CLI, an issue tracker token, a
	// company VPN var (e.g. AZURE_ for `az`, GH_TOKEN, VAULT_).
	//
	// It is a union, never a replacement: the defaults are always present, so
	// nothing here can lock the Worker out of its own model credentials. Values
	// are never read from or written to config.yaml — only names are matched,
	// the value stays in the host environment.
	//
	// omitempty on purpose: `corvex validate` rewrites config.yaml by marshaling
	// the whole struct, and an unset allowlist must not add a line to a config
	// the user never asked about.
	EnvAllowlist []string `yaml:"env_allowlist,omitempty"`
}

// MCPServerConfig declares an MCP server exposed to the Worker. Servers are
// materialised into a JSON file passed via the provider CLI (e.g.
// `claude --mcp-config`). Only the Worker receives MCP servers; the Planner
// (read-only) and Reviewer (read+test) run without them.
type MCPServerConfig struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
}

type ExecutionConfig struct {
	MaxRetries       int  `yaml:"max_retries"`
	AutoCommit       bool `yaml:"auto_commit"`
	Parallel         bool `yaml:"parallel"`
	// MaxParallel bounds how many tasks in one ready DAG level run
	// concurrently when Parallel is true. 0 → default of 4.
	MaxParallel int `yaml:"max_parallel"`
	InsightThreshold int  `yaml:"insight_threshold"` // min repeated tasks of same unconfigured type to trigger agent suggestion; 0 = disabled

	// MaxCostUSD caps the cumulative LLM spend across all tasks in a single
	// `corvex run`. When exceeded, Run returns an actionable error pointing
	// the user to raise the ceiling. 0 = no cap (use with caution). Default 25.
	MaxCostUSD float64 `yaml:"max_cost_usd"`

	// MaxCostPerTaskUSD caps the LLM spend on any single task (worker +
	// reviewer combined). Prevents a runaway loop from burning the run's
	// entire budget on one task. 0 = no cap. Default 5.
	MaxCostPerTaskUSD float64 `yaml:"max_cost_per_task_usd"`

	// TaskWarnMinutes emits an EventTaskWarn (surfaced in TUI as a chip)
	// when a task is still running after this many minutes. 0 = no warn.
	// Default 5 — Vercel cron's free-tier limit is 10min so warning at 5
	// gives time to abort before deploy-time tasks would fail in prod.
	TaskWarnMinutes int `yaml:"task_warn_minutes"`

	// TaskTimeoutMinutes is a hard wall-clock ceiling per task attempt. When a
	// worker attempt runs longer than this, the orchestrator CANCELS it (not
	// just warns) so a stuck provider can't hang the whole run forever. The
	// cancelled attempt counts as a failure and feeds the retry loop. 0 = no
	// limit. Default 20.
	TaskTimeoutMinutes int `yaml:"task_timeout_minutes"`

	// StreamIdleTimeoutSeconds cancels a worker attempt when no stream event
	// (text, tool call, tool result) has arrived for this many seconds — the
	// signature of a hung provider that opened a connection but stopped
	// producing output. 0 = no idle detection. Default 180. Only effective on
	// the streaming path (local/nil sandbox); buffered sandboxes rely on the
	// wall-clock ceiling instead.
	StreamIdleTimeoutSeconds int `yaml:"stream_idle_timeout_seconds"`
}

type ContextConfig struct {
	AlwaysInclude []string `yaml:"always_include"`
}

// ReviewConfig configures Reviewer behaviour beyond the binary PASS/FAIL
// verdict, in particular how repeated rejections of the same category
// escalate.
type ReviewConfig struct {
	Escalation map[string]EscalationPolicy `yaml:"escalation"`
}

// EscalationPolicy describes what to do after N consecutive rejections share
// the same category. Categories are free-form strings emitted by the
// Reviewer (e.g. "wrong-approach", "flaky-test", "missing-edge-case").
type EscalationPolicy struct {
	// After is the number of rejections of this category that triggers the
	// action. A value of 0 disables the policy.
	After int `yaml:"after"`
	// Action is one of "upgrade-model", "spawn-investigation",
	// "human-prompt". Unknown values are ignored at runtime.
	Action string `yaml:"action"`
	// To is the model to upgrade to when Action == "upgrade-model".
	To string `yaml:"to"`
}

type ValidateConfig struct {
	Stack    ValidateStackConfig `yaml:"stack"`
	Database ValidateDBConfig    `yaml:"database"`
	UI       ValidateUIConfig    `yaml:"ui"`
}

type ValidateStackConfig struct {
	Runtime      string `yaml:"runtime"`
	Framework    string `yaml:"framework"`
	StartCommand string `yaml:"start_command"`
	Port         int    `yaml:"port"`
	ReadyTimeout int    `yaml:"ready_timeout"`
	HealthPath   string `yaml:"health_path"`
	// EnvFile (optional, path relative to the repo root) is a dotenv file
	// sourced into the app AND migration processes during validation — e.g.
	// `backend/.env-stg` to run the dev server against the STG stack. Keep it
	// gitignored; it holds secrets.
	EnvFile string `yaml:"env_file"`
}

type ValidateDBConfig struct {
	Type           string            `yaml:"type"`
	Image          string            `yaml:"image"`
	MigrateCommand string            `yaml:"migrate_command"`
	Env            map[string]string `yaml:"env"`
}

type ValidateUIConfig struct {
	Enabled bool `yaml:"enabled"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	cfg := Default()
	if len(data) == 0 {
		return cfg, nil
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	applyDefaults(cfg)

	// Auto-source dotenv files into the process environment so `${VAR}`
	// placeholders in this config — most notably `mcp_servers[].env` — can
	// be expanded at runtime without committing secrets to the YAML.
	//
	// Sources, in precedence order (first one to set a var wins; the host
	// env always wins over any file):
	//
	//   1. `.corvex/*.env`             — Corvex-specific overrides (symlink-friendly)
	//   2. `<repo>/.env`               — the project's own dotenv (Nuxt/Next/Vite convention)
	//   3. `<repo>/.env.local`         — gitignored real values, when present
	//
	// "repo" here is the directory that contains `.corvex/`, so the lookup
	// works transparently for projects that already maintain a root-level
	// `.env`.
	corvexDir := filepath.Dir(path)
	loadDotEnvDir(corvexDir)

	repoRoot := filepath.Dir(corvexDir)
	for _, name := range []string{".env", ".env.local"} {
		loadOneEnvFile(filepath.Join(repoRoot, name))
	}

	return cfg, nil
}

// loadDotEnvDir parses every `*.env` file in dir (following symlinks). Best-
// effort: per-file failures are logged as warnings and do not abort startup.
func loadDotEnvDir(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.env"))
	if err != nil {
		log.Warn("globbing .env files", "dir", dir, "err", err)
		return
	}
	for _, path := range matches {
		loadOneEnvFile(path)
	}
}

// loadOneEnvFile merges KEY=VAL pairs from a single dotenv file into the
// process env. Variables already present in the host env are preserved
// (host wins). Missing files are silently skipped — only real read errors
// produce a warning. Symlinks are followed by os.Open.
func loadOneEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("skipping unreadable .env file", "path", path, "err", err)
		}
		return
	}
	defer f.Close()

	loaded := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			log.Warn("setting env var", "key", key, "err", err)
			continue
		}
		loaded++
	}
	if err := scanner.Err(); err != nil {
		log.Warn("reading .env file", "path", path, "err", err)
	}
	log.Debug("loaded .env", "path", path, "keys", loaded)
}

func Default() *Config {
	return &Config{
		Provider: ProviderConfig{
			Default: "claude-cli",
			Models: ModelsConfig{
				Planner:  "opus",
				Worker:   "sonnet",
				Reviewer: "sonnet",
			},
		},
		Sandbox: SandboxConfig{
			Type: "local",
		},
		Execution: ExecutionConfig{
			MaxRetries:               2,
			AutoCommit:               true,
			InsightThreshold:         3,
			MaxCostUSD:               25,
			MaxCostPerTaskUSD:        5,
			TaskWarnMinutes:          5,
			TaskTimeoutMinutes:       20,
			StreamIdleTimeoutSeconds: 180,
		},
	}
}

func applyDefaults(cfg *Config) {
	d := Default()
	if cfg.Provider.Default == "" {
		cfg.Provider.Default = d.Provider.Default
	}
	if cfg.Provider.Models.Planner == "" {
		cfg.Provider.Models.Planner = d.Provider.Models.Planner
	}
	if cfg.Provider.Models.Worker == "" {
		cfg.Provider.Models.Worker = d.Provider.Models.Worker
	}
	if cfg.Provider.Models.Reviewer == "" {
		cfg.Provider.Models.Reviewer = d.Provider.Models.Reviewer
	}
	if cfg.Sandbox.Type == "" {
		cfg.Sandbox.Type = d.Sandbox.Type
	}
	if cfg.Execution.MaxRetries == 0 {
		cfg.Execution.MaxRetries = d.Execution.MaxRetries
	}
}

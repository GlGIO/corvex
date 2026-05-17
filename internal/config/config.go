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
	Validate     ValidateConfig    `yaml:"validate"`
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
	InsightThreshold int  `yaml:"insight_threshold"` // min repeated tasks of same unconfigured type to trigger agent suggestion; 0 = disabled
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

	// Auto-source `.corvex/*.env` files (symlinks supported) into the process
	// environment so `${VAR}` placeholders in this config — most notably
	// `mcp_servers[].env` — can be expanded at runtime without committing
	// secrets to the YAML. Host env wins, so an explicit `export` in the
	// shell still overrides the file.
	loadDotEnvFiles(filepath.Dir(path))

	return cfg, nil
}

// loadDotEnvFiles parses every `*.env` file in dir (following symlinks) and
// merges KEY=VAL pairs into the process env. Best-effort: per-file failures
// are logged as warnings and do not abort startup. Variables already present
// in the host env are preserved (host wins).
func loadDotEnvFiles(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.env"))
	if err != nil {
		log.Warn("globbing .env files", "dir", dir, "err", err)
		return
	}
	for _, path := range matches {
		f, err := os.Open(path) // follows symlinks
		if err != nil {
			log.Warn("skipping unreadable .env file", "path", path, "err", err)
			continue
		}
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
		_ = f.Close()
		log.Debug("loaded .env", "path", path, "keys", loaded)
	}
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
			MaxRetries:       2,
			AutoCommit:       true,
			InsightThreshold: 3,
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

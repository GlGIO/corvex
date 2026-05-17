package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Project      ProjectConfig     `yaml:"project"`
	Provider     ProviderConfig    `yaml:"provider"`
	Sandbox      SandboxConfig     `yaml:"sandbox"`
	Execution    ExecutionConfig   `yaml:"execution"`
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
	return cfg, nil
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

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
	Type            string   `yaml:"type"`
	Image           string   `yaml:"image"`
	Mount           string   `yaml:"mount"`
	WorkDir         string   `yaml:"workdir"`
	WorkerExtraArgs []string `yaml:"worker_extra_args"`
}

type ExecutionConfig struct {
	MaxRetries int  `yaml:"max_retries"`
	AutoCommit bool `yaml:"auto_commit"`
	Parallel   bool `yaml:"parallel"`
}

type ContextConfig struct {
	AlwaysInclude []string `yaml:"always_include"`
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
			MaxRetries: 2,
			AutoCommit: true,
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

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

const fullConfig = `
project:
  name: smartcare
  description: "CRM multi-tenant da Yandeh"

provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet

sandbox:
  type: docker
  image: node:20-slim
  mount: ./:/app
  workdir: /app

execution:
  max_retries: 2
  auto_commit: true
  parallel: true

context:
  always_include:
    - .corvex/context/*.md

agent_routing:
  database: .corvex/agents/dba.md
  backend: .corvex/agents/backend.md
  frontend: .corvex/agents/frontend.md
  review: .corvex/agents/reviewer.md
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTemp(t, fullConfig)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Project.Name != "smartcare" {
		t.Errorf("Project.Name = %q, want %q", cfg.Project.Name, "smartcare")
	}
	if cfg.Provider.Models.Planner != "opus" {
		t.Errorf("Provider.Models.Planner = %q, want %q", cfg.Provider.Models.Planner, "opus")
	}
	if cfg.Sandbox.Type != "docker" {
		t.Errorf("Sandbox.Type = %q, want %q", cfg.Sandbox.Type, "docker")
	}
	if !cfg.Execution.Parallel {
		t.Error("Execution.Parallel = false, want true")
	}
	if len(cfg.Context.AlwaysInclude) != 1 || cfg.Context.AlwaysInclude[0] != ".corvex/context/*.md" {
		t.Errorf("Context.AlwaysInclude = %v, want [.corvex/context/*.md]", cfg.Context.AlwaysInclude)
	}
	if cfg.AgentRouting["database"] != ".corvex/agents/dba.md" {
		t.Errorf("AgentRouting[database] = %q, want %q", cfg.AgentRouting["database"], ".corvex/agents/dba.md")
	}
}

func TestLoad_MinimalConfig(t *testing.T) {
	path := writeTemp(t, "project:\n  name: myproject\n")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Project.Name != "myproject" {
		t.Errorf("Project.Name = %q, want %q", cfg.Project.Name, "myproject")
	}
	if cfg.Provider.Default != "claude-cli" {
		t.Errorf("Provider.Default = %q, want default %q", cfg.Provider.Default, "claude-cli")
	}
	if cfg.Provider.Models.Planner != "opus" {
		t.Errorf("Provider.Models.Planner = %q, want default %q", cfg.Provider.Models.Planner, "opus")
	}
	if cfg.Execution.MaxRetries != 2 {
		t.Errorf("Execution.MaxRetries = %d, want default %d", cfg.Execution.MaxRetries, 2)
	}
	if cfg.Sandbox.Type != "local" {
		t.Errorf("Sandbox.Type = %q, want default %q", cfg.Sandbox.Type, "local")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTemp(t, "project:\n  name: test\n  invalid:\n\t- mixed tabs")
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid YAML, got nil")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("Load() expected error for missing file, got nil")
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	path := writeTemp(t, "")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider.Default != "claude-cli" {
		t.Errorf("Provider.Default = %q, want default %q", cfg.Provider.Default, "claude-cli")
	}
}

func TestDefault(t *testing.T) {
	cfg := config.Default()

	if cfg.Provider.Default != "claude-cli" {
		t.Errorf("Provider.Default = %q, want %q", cfg.Provider.Default, "claude-cli")
	}
	if cfg.Provider.Models.Planner != "opus" {
		t.Errorf("Provider.Models.Planner = %q, want %q", cfg.Provider.Models.Planner, "opus")
	}
	if cfg.Provider.Models.Worker != "sonnet" {
		t.Errorf("Provider.Models.Worker = %q, want %q", cfg.Provider.Models.Worker, "sonnet")
	}
	if cfg.Provider.Models.Reviewer != "sonnet" {
		t.Errorf("Provider.Models.Reviewer = %q, want %q", cfg.Provider.Models.Reviewer, "sonnet")
	}
	if cfg.Sandbox.Type != "local" {
		t.Errorf("Sandbox.Type = %q, want %q", cfg.Sandbox.Type, "local")
	}
	if cfg.Execution.MaxRetries != 2 {
		t.Errorf("Execution.MaxRetries = %d, want %d", cfg.Execution.MaxRetries, 2)
	}
	if !cfg.Execution.AutoCommit {
		t.Error("Execution.AutoCommit = false, want true")
	}
}

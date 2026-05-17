package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/types"
)

// ConfigDraft is the AI's best guess at the validate config, plus the fields it wasn't sure about.
type ConfigDraft struct {
	Validate   config.ValidateConfig
	Detected   []DetectedField
	Uncertain  []UncertainField
	CostUSD    float64
	TokensIn   int
	TokensOut  int
	DurationMs int64
}

// DetectedField records something the AI found in the codebase with high confidence.
type DetectedField struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// UncertainField records the AI's best guess for something it couldn't determine with confidence.
type UncertainField struct {
	Field  string `json:"field"`
	Guess  string `json:"guess"`
	Reason string `json:"reason"`
}

// Configurer inspects the codebase and produces a draft validate config.
type Configurer struct {
	provider provider.Provider
	model    string
	workDir  string
}

// NewConfigurer creates a Configurer bound to the given provider and model.
func NewConfigurer(p provider.Provider, model, workDir string) *Configurer {
	return &Configurer{provider: p, model: model, workDir: workDir}
}

// InferValidate analyses the codebase and returns a draft validate config along with
// detected fields (high confidence) and uncertain fields (best guesses).
func (c *Configurer) InferValidate(ctx context.Context) (*ConfigDraft, error) {
	prompt := buildConfigurerPrompt()

	result, err := c.provider.Execute(ctx, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        c.model,
		WorkDir:      c.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep"},
	})
	if err != nil {
		return nil, fmt.Errorf("configurer execution: %w", err)
	}

	draft, err := parseConfigurerOutput(result.Output)
	if err != nil {
		return nil, fmt.Errorf("parsing configurer output: %w", err)
	}
	draft.CostUSD = result.CostUSD
	draft.TokensIn = result.TokensIn
	draft.TokensOut = result.TokensOut
	draft.DurationMs = result.DurationMs
	return draft, nil
}

func buildConfigurerPrompt() string {
	return `You are configuring an automated validation environment for a software project.

## Goal

Inspect this codebase (Read/Glob/Grep) and produce a draft configuration for ` + "`corvex validate`" + `.
Detect the runtime, framework, start command, port, database, and migration command from real
project files (package.json, pyproject.toml, go.mod, Dockerfile, docker-compose.yml, .env*, README, etc.).

For each field, mark it as either DETECTED (high confidence — you found it explicitly) or
UNCERTAIN (your best guess based on convention).

## Output format

End your response with exactly one fenced code block tagged ` + "`config`" + `, containing a single JSON
object. No other text after the block.

` + "```config\n" + `{
  "stack": {
    "runtime": "node|python|go|java|ruby|other",
    "framework": "nestjs|express|fastapi|django|gin|...",
    "start_command": "npm run start:test",
    "port": 3000,
    "health_path": "/health",
    "ready_timeout": 30
  },
  "database": {
    "type": "postgres|mysql|sqlite|mongodb|none",
    "image": "postgres:16",
    "migrate_command": "npm run migrate",
    "env": {"POSTGRES_DB": "testdb", "POSTGRES_USER": "test", "POSTGRES_PASSWORD": "test"}
  },
  "ui": {
    "enabled": false
  },
  "detected": [
    {"field": "stack.runtime", "value": "node", "source": "package.json"},
    {"field": "stack.framework", "value": "nestjs", "source": "package.json dependencies"}
  ],
  "uncertain": [
    {"field": "stack.health_path", "guess": "/health", "reason": "no explicit health endpoint found"}
  ]
}
` + "```" + `

Rules:
- Use null or "" for fields you cannot determine at all (don't invent stack/database configs from nothing).
- The "detected" array MUST cite a source file or pattern.
- The "uncertain" array is for guesses you're not sure about — the user will be asked to confirm these.
- For "ui.enabled", default to false unless you find a frontend (React/Vue/Svelte/etc. in dependencies or src/).
- For "database.type", only set non-"none" if you find concrete evidence (driver in deps, docker-compose service, connection string).
`
}

var configBlockRe = regexp.MustCompile("(?s)```config\\s*\\n(.*?)\\n```")

type configPayload struct {
	Stack     stackPayload     `json:"stack"`
	Database  dbPayload        `json:"database"`
	UI        uiPayload        `json:"ui"`
	Detected  []DetectedField  `json:"detected"`
	Uncertain []UncertainField `json:"uncertain"`
}

type stackPayload struct {
	Runtime      string `json:"runtime"`
	Framework    string `json:"framework"`
	StartCommand string `json:"start_command"`
	Port         int    `json:"port"`
	HealthPath   string `json:"health_path"`
	ReadyTimeout int    `json:"ready_timeout"`
}

type dbPayload struct {
	Type           string            `json:"type"`
	Image          string            `json:"image"`
	MigrateCommand string            `json:"migrate_command"`
	Env            map[string]string `json:"env"`
}

type uiPayload struct {
	Enabled bool `json:"enabled"`
}

func parseConfigurerOutput(output string) (*ConfigDraft, error) {
	matches := configBlockRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no ```config block found in output")
	}
	raw := matches[len(matches)-1][1]

	var p configPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("invalid json in config block: %w", err)
	}

	timeout := p.Stack.ReadyTimeout
	if timeout == 0 {
		timeout = 30
	}

	return &ConfigDraft{
		Validate: config.ValidateConfig{
			Stack: config.ValidateStackConfig{
				Runtime:      strings.TrimSpace(p.Stack.Runtime),
				Framework:    strings.TrimSpace(p.Stack.Framework),
				StartCommand: strings.TrimSpace(p.Stack.StartCommand),
				Port:         p.Stack.Port,
				HealthPath:   strings.TrimSpace(p.Stack.HealthPath),
				ReadyTimeout: timeout,
			},
			Database: config.ValidateDBConfig{
				Type:           strings.TrimSpace(p.Database.Type),
				Image:          strings.TrimSpace(p.Database.Image),
				MigrateCommand: strings.TrimSpace(p.Database.MigrateCommand),
				Env:            p.Database.Env,
			},
			UI: config.ValidateUIConfig{
				Enabled: p.UI.Enabled,
			},
		},
		Detected:  p.Detected,
		Uncertain: p.Uncertain,
	}, nil
}

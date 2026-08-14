package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/provider"
	sandboxpkg "github.com/giovannialves/corvex/internal/sandbox"
)

// MissingProject reports a project name that has neither spec.md nor tasks.md,
// together with the facts a caller needs to help: the names that do exist and
// the closest match to what was asked for ("" when nothing is close).
type MissingProject struct {
	Name       string
	Available  []string
	Suggestion string
}

// CheckProject returns nil when the project has state on disk (spec.md or
// tasks.md), or the facts about the miss so the caller can explain it. Running
// this before any work is what keeps a typo from being answered with a planner
// call.
func CheckProject(workDir, project string) *MissingProject {
	// Only a definite "not there" counts as missing: a stat that fails for any
	// other reason (permissions, a file where a directory belongs) is left for
	// the code that actually opens the file to report.
	pDir := ProjectDir(workDir, project)
	if _, err := os.Stat(filepath.Join(pDir, "spec.md")); !os.IsNotExist(err) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(pDir, "tasks.md")); !os.IsNotExist(err) {
		return nil
	}
	return &MissingProject{
		Name:       project,
		Available:  ProjectNames(workDir),
		Suggestion: SuggestProject(workDir, project),
	}
}

// RunRequest is one `corvex run` invocation described without a terminal: which
// project, which task scope, which safety overrides, and the channels the caller
// wants progress and control to flow through.
type RunRequest struct {
	Config       *config.Config
	WorkDir      string
	TargetTask   string
	SingleTask   bool
	NoReplan     bool
	Force        bool
	ApproveGates bool
	// ABSpec is the comma-separated model pair for an A/B comparison
	// ("sonnet,opus"), or "" for a normal run. It is parsed here so every
	// surface rejects the same malformed input.
	ABSpec   string
	Events   chan orchestrator.Event
	Commands chan orchestrator.Command
}

// NewRunner builds the orchestrator for a run request: provider, sandbox and
// the scheduler options. It only assembles and validates — nothing executes
// until the caller calls Run, and nothing is printed here.
func NewRunner(req RunRequest) (*orchestrator.Orchestrator, error) {
	p, err := provider.NewProvider(req.Config.Provider.Default, req.Config)
	if err != nil {
		return nil, fmt.Errorf("creating provider: %w", err)
	}

	// Built before the A/B checks below on purpose: selecting a sandbox probes
	// the host and may warn about falling back, and that warning has always
	// come out before any flag complaint.
	sb := sandboxpkg.NewSandbox(req.Config.Sandbox)

	abModels, err := ParseABModels(req.ABSpec)
	if err != nil {
		return nil, err
	}
	if len(abModels) > 0 && req.TargetTask == "" && !req.SingleTask {
		return nil, fmt.Errorf("--ab requires --task <id> or --single to scope the comparison")
	}

	return orchestrator.New(orchestrator.Options{
		Config:       req.Config,
		Provider:     p,
		WorkDir:      req.WorkDir,
		Events:       req.Events,
		TargetTask:   req.TargetTask,
		SingleTask:   req.SingleTask,
		Sandbox:      sb,
		ABModels:     abModels,
		Commands:     req.Commands,
		NoReplan:     req.NoReplan,
		Force:        req.Force,
		ApproveGates: req.ApproveGates,
	}), nil
}

// ParseABModels splits a comma-separated spec like "sonnet,opus" into a
// 2-element slice, trimming whitespace. Returns nil when the spec is empty.
// Errors when the result is not exactly 2 distinct models.
func ParseABModels(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	models := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			models = append(models, p)
		}
	}
	if len(models) != 2 {
		return nil, fmt.Errorf("--ab needs exactly 2 models separated by a comma (got %d: %q)", len(models), raw)
	}
	if models[0] == models[1] {
		return nil, fmt.Errorf("--ab models must differ (got %q twice)", models[0])
	}
	return models, nil
}

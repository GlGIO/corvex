package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/run"
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
	Config *config.Config
	// Project is the project this run executes. It belongs in the request, not
	// only in the argument to Orchestrator.Run, because run identity is minted
	// here and the record has to say which project it identifies.
	Project      string
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

	// Repo is the absolute git root the run is recorded against. Empty means
	// "resolve it from WorkDir".
	Repo string
	// Registry overrides how run identity is registered. The zero value is
	// production: the real corvex home, a crypto/rand id, the real clock and
	// this process's pid. Tests set NewID/Home/Now here so a run id never
	// reaches golden-compared output by accident.
	Registry *run.Registry
	// HeartbeatInterval overrides the heartbeat period; 0 uses the package
	// default (10s).
	HeartbeatInterval time.Duration
	// GatePoll overrides how often a run parked on a human gate re-reads its
	// gate file; 0 uses step.DefaultGatePoll. Tests set it so a cross-process
	// approval does not cost two seconds of wall clock.
	GatePoll time.Duration

	// Environment is the run environment by name ("", "simple" or "stack").
	// Empty is `simple`; anything unknown is refused (see ParseEnvironment).
	Environment string
	// StackUp overrides how `stack` is brought up. Nil wires the production
	// implementation; tests substitute so the lifecycle is assertable without a
	// docker daemon.
	StackUp StackUpFn
}

// NewRunner assembles one run: provider, sandbox, scheduler options — and the
// run's identity, which is minted here, once, and handed down by value.
//
// Nothing executes until Execute is called. Identity is registered *after* every
// validation that can reject the invocation, so a run refused for a bad --ab
// flag leaves no `running` record behind that nothing will ever close — and a run
// whose identity cannot be registered is itself rejected, with no runner returned.
func NewRunner(req RunRequest) (*Runner, error) {
	p, err := provider.NewProvider(req.Config.Provider.Default, req.Config)
	if err != nil {
		return nil, fmt.Errorf("creating provider: %w", err)
	}

	// Built before the A/B checks below on purpose: selecting a sandbox probes
	// the host and may warn about falling back, and that warning has always
	// come out before any flag complaint.
	sb := sandboxpkg.NewSandbox(req.Config.Sandbox)

	// Parsed before identity is registered, with every other flag check: a run
	// refused for a misspelled environment must not leave a `running` record
	// that nothing will ever close.
	env, err := ParseEnvironment(req.Environment)
	if err != nil {
		return nil, err
	}

	abModels, err := ParseABModels(req.ABSpec)
	if err != nil {
		return nil, err
	}
	if len(abModels) > 0 && req.TargetTask == "" && !req.SingleTask {
		return nil, fmt.Errorf("--ab requires --task <id> or --single to scope the comparison")
	}

	// Identity is a precondition, not a nicety. A run that cannot be identified
	// writes a ledger nobody can attribute — the pre-F1 shape, arriving silently
	// and precisely when the machine is under stress — so the invocation is
	// refused instead. See Runner for the trade that was reversed here.
	handle, err := startIdentity(req)
	if err != nil {
		return nil, fmt.Errorf("registering run identity: %w", err)
	}
	r := &Runner{
		Project:     req.Project,
		interval:    req.HeartbeatInterval,
		handle:      handle,
		RunID:       handle.RunID(),
		Repo:        handle.Record().Repo,
		Recipe:      handle.Record().Recipe,
		Environment: env,
		env:         &runEnvironment{kind: env},
		stackUp:     req.StackUp,
	}
	if r.stackUp == nil {
		r.stackUp = DefaultStackUp(req.WorkDir, req.Project, req.Config, os.Stdout, os.Stderr)
	}

	r.Orchestrator = orchestrator.New(orchestrator.Options{
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
		Identity:     r.Identity(),
		Repo:         r.Repo,
		// The run reports `parked` while a human gate holds it. This is a
		// method value on the handle rather than the handle itself: the step
		// executor gets the one verb it needs and no access to identity.
		SetRunStatus: handle.SetStatus,
		GatePoll:     req.GatePoll,
	})
	return r, nil
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

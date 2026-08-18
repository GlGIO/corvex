package step

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/types"
)

// Worker executes a single task using the AI provider with full tool access.
type Worker struct {
	provider     provider.Provider
	model        string
	workDir      string
	sandbox      sandbox.Sandbox
	onStream     func(types.StreamEvent)
	skillRouting map[string]string // task type → repo skill name
	envAllowlist []string          // host env prefixes forwarded into the sandbox
	envDenylist  []string          // names the runner keeps to itself (F9 custody)
	extraBlocked []string          // tool patterns the config refuses on top of the built-in block
}

// NewWorker creates a Worker bound to the given provider and model.
// skillRouting (task type → skill name) may be nil.
//
// security carries credential custody (F9): the names this Worker must not
// forward even when the allowlist matches them, and the tools it must refuse on
// top of the built-in block. Like the allowlist it is a parameter rather than
// process state, and for the same reason.
//
// envAllowlist is the resolved list of host environment prefixes this Worker
// may forward into its sandbox — normally cfg.EnvAllowlist(). It is a
// parameter, not process state, because it decides which credentials leave the
// host: the Worker of one run must never inherit the allowlist of another.
// A nil list forwards nothing.
func NewWorker(
	p provider.Provider,
	model, workDir string,
	sb sandbox.Sandbox,
	skillRouting map[string]string,
	envAllowlist []string,
	security config.SecurityConfig,
) *Worker {
	return &Worker{
		provider:     p,
		model:        model,
		workDir:      workDir,
		sandbox:      sb,
		skillRouting: skillRouting,
		envAllowlist: envAllowlist,
		envDenylist:  security.RunnerOnlyEnv,
		extraBlocked: security.DisallowedTools,
	}
}

// clone returns a copy of the Worker with its own mutable fields (model,
// onStream). Parallel execution gives each task its own clone so escalation's
// model upgrade and the per-task stream callback never race across goroutines.
func (w *Worker) clone() *Worker {
	return &Worker{
		provider:     w.provider,
		model:        w.model,
		workDir:      w.workDir,
		sandbox:      w.sandbox,
		skillRouting: w.skillRouting,
		envAllowlist: w.envAllowlist,
		envDenylist:  w.envDenylist,
		extraBlocked: w.extraBlocked,
	}
}

// SetOnStream installs a callback that receives streaming events from the
// provider while a task runs. Pass nil to clear. The callback is invoked
// synchronously on the provider's goroutine, so it should be cheap (e.g.
// forwarding to a buffered channel).
func (w *Worker) SetOnStream(cb func(types.StreamEvent)) {
	w.onStream = cb
}

// buildRequest assembles what the provider is asked to do, including the tool
// policy. It is a method rather than inline code so the policy is assertable:
// "the raw path is closed" is a security claim, and a security claim that only
// exists inside a 40-line function is a claim nobody tests.
func (w *Worker) buildRequest(t *types.Task, anchorCtx string, contextDocs []string, agentPrompt, diagnosis string) types.ExecuteRequest {
	routedSkill := ""
	if w.skillRouting != nil {
		routedSkill = w.skillRouting[string(t.Type)]
	}
	return types.ExecuteRequest{
		Prompt:  buildWorkerPrompt(t, anchorCtx, contextDocs, agentPrompt, diagnosis, routedSkill),
		Model:   w.model,
		WorkDir: w.workDir,
		// Hard block: the worker LLM cannot touch corvex state files
		// (tasks.md, anchor.yaml, decisions.md, spec.md). Status transitions
		// happen through the orchestrator, not through Edit/Write tool calls.
		// Without this guard, the worker has been observed to write
		// "✅ DONE" instead of canonical "✅ PASSED" (silently corrupting
		// the parser) and to delete the .corvex symlink in worktrees.
		//
		// Config adds to this list and can never shorten it (F9): a user closing
		// `Bash` is closing a door, and no config value opens one corvex decided
		// to keep shut.
		// Custody travels WITH the request, because the request is what becomes
		// a process. Computing the forwarded set was never enough: every sandbox
		// and the direct exec path start the child from os.Environ(), so a
		// variable that was merely "not forwarded" was inherited anyway.
		DenyEnv: w.envDenylist,
		DisallowedTools: append([]string{
			"Edit(.corvex/**)",
			"Write(.corvex/**)",
			"Bash(rm:.corvex/**)",
			"Bash(rm:-rf .corvex*)",
			"Bash(mv:.corvex/**)",
		}, w.extraBlocked...),
	}
}

// Execute runs the AI provider for the given task and returns the execution result.
func (w *Worker) Execute(
	ctx context.Context,
	t *types.Task,
	anchorCtx string,
	contextDocs []string,
	agentPrompt string,
	diagnosis string,
) (*types.ExecuteResult, error) {
	req := w.buildRequest(t, anchorCtx, contextDocs, agentPrompt, diagnosis)

	// When a streaming callback is set AND we're running on a LocalSandbox,
	// bypass the sandbox abstraction (which buffers stdout) and stream
	// directly via the provider's ProgressStreamer. LocalSandbox is just a
	// thin os/exec wrapper, so running the same command through
	// ExecuteWithProgress produces identical results plus per-chunk events.
	if w.onStream != nil && isLocalOrNilSandbox(w.sandbox) {
		if ps, ok := w.provider.(provider.ProgressStreamer); ok {
			result, err := ps.ExecuteWithProgress(ctx, req, w.onStream)
			if err != nil {
				return result, fmt.Errorf("worker execution for task %s: %w", t.ID, err)
			}
			return result, nil
		}
	}

	if cb, ok := w.provider.(provider.CommandBuilder); ok && w.sandbox != nil {
		return w.executeViaSandbox(ctx, cb, req, t.ID)
	}

	result, err := w.provider.Execute(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("worker execution for task %s: %w", t.ID, err)
	}

	return result, nil
}

// isLocalOrNilSandbox reports whether the sandbox can be safely bypassed for
// streaming. Docker/devcontainer/nix sandboxes need their own runtime so we
// can't shortcut to a local exec for them.
func isLocalOrNilSandbox(sb sandbox.Sandbox) bool {
	if sb == nil {
		return true
	}
	_, ok := sb.(*sandbox.LocalSandbox)
	return ok
}

func (w *Worker) executeViaSandbox(
	ctx context.Context,
	cb provider.CommandBuilder,
	req types.ExecuteRequest,
	taskID string,
) (*types.ExecuteResult, error) {
	bin, args, env := cb.BuildCommand(req)

	authEnv := collectAuthEnv(w.envAllowlist, w.envDenylist)
	for k, v := range env {
		authEnv[k] = v
	}

	cmd := make([]string, 0, 1+len(args))
	cmd = append(cmd, bin)
	cmd = append(cmd, args...)

	start := time.Now()
	sandboxResult, err := w.sandbox.Run(ctx, sandbox.RunRequest{
		Command: cmd,
		Env:     authEnv,
		DenyEnv: w.envDenylist,
	})
	elapsed := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("sandbox execution for task %s: %w", taskID, err)
	}

	result, parseErr := cb.ParseFullOutput(sandboxResult.Stdout, sandboxResult.ExitCode, elapsed)
	if parseErr != nil {
		return nil, fmt.Errorf("parsing sandbox output for task %s: %w", taskID, parseErr)
	}

	if sandboxResult.ExitCode != 0 {
		return result, fmt.Errorf("worker execution for task %s: exit code %d (stderr: %s)",
			taskID, sandboxResult.ExitCode, sandboxResult.Stderr)
	}

	return result, nil
}

// collectAuthEnv picks the host environment variables whose name starts with
// one of prefixes. Values never reach a log or the ledger — they are read here
// and handed straight to the sandbox.
//
// The prefix list is configuration, not code: the caller passes
// cfg.EnvAllowlist(), which is the built-in credentials plus whatever
// `sandbox.env_allowlist` declares, so a user can grant a new credential (a
// cloud CLI, an issue tracker token) without a new binary.
func collectAuthEnv(prefixes, denied []string) map[string]string {
	deny := make(map[string]struct{}, len(denied))
	for _, name := range denied {
		if name = strings.TrimSpace(name); name != "" {
			deny[name] = struct{}{}
		}
	}

	env := make(map[string]string)
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		// Custody (F9): the denylist wins over any prefix that would have let
		// this through. Order matters — a credential the runner is supposed to
		// keep must not depend on which prefix happened to be checked first.
		if _, held := deny[key]; held {
			continue
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				env[key] = parts[1]
				break
			}
		}
	}
	return env
}

func buildWorkerPrompt(t *types.Task, anchorCtx string, contextDocs []string, agentPrompt, diagnosis, routedSkill string) string {
	var b strings.Builder

	if agentPrompt != "" {
		b.WriteString("## Agent Instructions\n\n")
		b.WriteString(agentPrompt)
		b.WriteString("\n\n")
	}

	if routedSkill != "" {
		fmt.Fprintf(&b, "## Required Skill\n\nThis %q task is routed to the `%s` skill. Invoke the `%s` skill (via the Skill tool) and follow it while completing the task.\n\n", t.Type, routedSkill, routedSkill)
	}

	if len(contextDocs) > 0 {
		b.WriteString("## Project Context\n\n")
		b.WriteString(strings.Join(contextDocs, "\n\n---\n\n"))
		b.WriteString("\n\n")
	}

	if anchorCtx != "" {
		b.WriteString("## Previous Work\n\n")
		b.WriteString(anchorCtx)
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "## Current Task: %s — %s\n\n", t.ID, t.Title)

	b.WriteString("### Description\n\n")
	b.WriteString(t.Description)
	b.WriteString("\n\n")

	if len(t.Criteria) > 0 {
		b.WriteString("### Success Criteria\n\n")
		for _, c := range t.Criteria {
			fmt.Fprintf(&b, "- [ ] %s\n", c)
		}
		b.WriteString("\n")
	}

	if len(t.Files.Create) > 0 || len(t.Files.Modify) > 0 {
		b.WriteString("### Files\n\n")
		for _, f := range t.Files.Create {
			fmt.Fprintf(&b, "- Create: %s\n", f)
		}
		for _, f := range t.Files.Modify {
			fmt.Fprintf(&b, "- Modify: %s\n", f)
		}
		b.WriteString("\n")
	}

	if diagnosis != "" {
		b.WriteString("## Previous Attempt Failed\n\n")
		b.WriteString("The previous attempt to complete this task failed with the following diagnosis:\n\n")
		b.WriteString(diagnosis)
		b.WriteString("\n\nPlease address these issues in your implementation.\n\n")
	}

	b.WriteString(`## Instructions

Complete the task described above. Make sure all success criteria are met.

### Before you write code — sanity check the task

The task description above was produced by an upstream Planner that
compacted spec.md. If the task touches **timing, ordering, units, numeric
thresholds, or external API parameters**, take 30 seconds to:

1. Glob ` + "`.corvex/tasks/*/spec.md`" + ` (and ` + "`decisions.md`" + ` if it exists)
2. Read the section relevant to this task
3. Check whether the task description matches the spec on those details

If they match, proceed normally.

If they disagree, **spec.md wins**. Implement what spec.md says, and at the
top of your final response add an ` + "`INTERPRETATION:`" + ` note explaining which
reading you took and why. Example:

    INTERPRETATION: Task said "subtract 15min before scheduling" but
    spec.md §2.8 says the scheduled timestamp itself is the campaign
    send time and an internal cron triggers 15min earlier. Implemented
    the spec.md reading — scheduleCampaign() receives agendado_para
    unchanged.

The reviewer cross-checks spec.md and accepts implementations that match
it even when the task description points elsewhere. Documenting your
interpretation up front prevents a wasted retry loop.

### Required: end with a TASK-REPORT block

After your work is complete, end your response with a structured report block
in EXACTLY this shape (the orchestrator parses it to carry context to the next
task):

    TASK-REPORT:
    SUMMARY: <1-3 sentences describing what you implemented>
    DECISIONS:
    - <each key decision or tradeoff you made>
    HANDOFF: <what the next task's engineer needs to know — new interfaces,
    invariants, gotchas. One short paragraph. If this is the final task with no
    follow-up, write "none">

The HANDOFF is mandatory unless this is the last task. Omitting the TASK-REPORT
block will cause your attempt to be rejected and retried.
`)

	return b.String()
}

// loadContextDocs reads files matching glob patterns relative to workDir.
func loadContextDocs(workDir string, patterns []string) []string {
	var docs []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(workDir, pattern))
		if err != nil {
			continue
		}
		for _, match := range matches {
			data, err := os.ReadFile(match)
			if err != nil {
				continue
			}
			content := strings.TrimSpace(string(data))
			if content != "" {
				docs = append(docs, content)
			}
		}
	}
	return docs
}

// loadAgentPrompt reads the agent prompt file for the given task type via routing config.
func loadAgentPrompt(workDir string, routing map[string]string, taskType types.TaskType) string {
	if routing == nil {
		return ""
	}
	path, ok := routing[string(taskType)]
	if !ok {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(workDir, path))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

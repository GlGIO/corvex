package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/types"
)

// Worker executes a single task using the AI provider with full tool access.
type Worker struct {
	provider provider.Provider
	model    string
	workDir  string
	sandbox  sandbox.Sandbox
	onStream func(types.StreamEvent)
}

// NewWorker creates a Worker bound to the given provider and model.
func NewWorker(p provider.Provider, model, workDir string, sb sandbox.Sandbox) *Worker {
	return &Worker{provider: p, model: model, workDir: workDir, sandbox: sb}
}

// SetOnStream installs a callback that receives streaming events from the
// provider while a task runs. Pass nil to clear. The callback is invoked
// synchronously on the provider's goroutine, so it should be cheap (e.g.
// forwarding to a buffered channel).
func (w *Worker) SetOnStream(cb func(types.StreamEvent)) {
	w.onStream = cb
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
	prompt := buildWorkerPrompt(t, anchorCtx, contextDocs, agentPrompt, diagnosis)

	req := types.ExecuteRequest{
		Prompt:  prompt,
		Model:   w.model,
		WorkDir: w.workDir,
	}

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

	authEnv := collectAuthEnv()
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

var authEnvPrefixes = []string{
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

func collectAuthEnv() map[string]string {
	env := make(map[string]string)
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		for _, prefix := range authEnvPrefixes {
			if strings.HasPrefix(key, prefix) {
				env[key] = parts[1]
				break
			}
		}
	}
	return env
}

func buildWorkerPrompt(t *types.Task, anchorCtx string, contextDocs []string, agentPrompt, diagnosis string) string {
	var b strings.Builder

	if agentPrompt != "" {
		b.WriteString("## Agent Instructions\n\n")
		b.WriteString(agentPrompt)
		b.WriteString("\n\n")
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

	b.WriteString("## Instructions\n\nComplete the task described above. Make sure all success criteria are met.\n")

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

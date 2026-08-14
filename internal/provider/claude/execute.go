package claude

// Execution paths for the Claude CLI: the blocking Execute /
// ExecuteWithProgress pair and the channel-based Stream. Both drive the same
// stream-json parser (protocol.go) over the child process's stdout.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

func (c *ClaudeCLI) Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
	return c.ExecuteWithProgress(ctx, req, nil)
}

// ExecuteWithProgress is identical to Execute but invokes onEvent for every
// stream event (tool calls, text, errors) as it arrives. The final
// ExecuteResult still carries cost and token totals — callers get
// observability and accounting in one pass.
func (c *ClaudeCLI) ExecuteWithProgress(ctx context.Context, req types.ExecuteRequest, onEvent func(types.StreamEvent)) (*types.ExecuteResult, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}

	args := buildArgs(req)
	cmd := c.cmdRunner(ctx, c.binaryCmd, args...)
	configureCmd(cmd, req)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	start := time.Now()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude cli stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude cli start: %w", err)
	}

	result := &types.ExecuteResult{}
	var outputParts []string
	var sawResult bool

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		events, parseErr := parseNDJSONLine(line)
		if parseErr != nil {
			continue
		}

		for _, ev := range events {
			if onEvent != nil {
				onEvent(ev)
			}
			switch ev.Type {
			case types.EventText:
				outputParts = append(outputParts, ev.Content)
			case types.EventDone:
				// final content already captured
			}
		}

		var raw rawLine
		if json.Unmarshal(line, &raw) == nil && raw.Type == "result" {
			sawResult = true
			var res resultLine
			if json.Unmarshal(line, &res) == nil {
				result.TokensIn = res.TotalInputTokens
				result.TokensOut = res.TotalOutputTokens
				result.CostUSD = res.TotalCostUSD
				if res.DurationMs > 0 {
					result.DurationMs = res.DurationMs
				}
			}
		}
	}

	waitErr := cmd.Wait()
	elapsed := time.Since(start)
	if result.DurationMs == 0 {
		result.DurationMs = elapsed.Milliseconds()
	}

	result.Output = strings.Join(outputParts, "")

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		return result, fmt.Errorf("claude cli exited with error: %w (stderr: %s)", waitErr, strings.TrimSpace(stderr.String()))
	}

	if !sawResult {
		return result, fmt.Errorf("claude cli produced no result line (truncated output?)")
	}

	return result, nil
}

func (c *ClaudeCLI) Stream(ctx context.Context, req types.ExecuteRequest) (<-chan types.StreamEvent, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}

	args := buildArgs(req)
	cmd := c.cmdRunner(ctx, c.binaryCmd, args...)
	configureCmd(cmd, req)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude cli stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude cli start: %w", err)
	}

	ch := make(chan types.StreamEvent, 32)

	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			events, parseErr := parseNDJSONLine(line)
			if parseErr != nil {
				select {
				case ch <- types.StreamEvent{Type: types.EventError, Content: parseErr.Error()}:
				case <-ctx.Done():
					return
				}
				continue
			}

			for _, ev := range events {
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		}

		if err := cmd.Wait(); err != nil {
			select {
			case ch <- types.StreamEvent{Type: types.EventError, Content: err.Error()}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}

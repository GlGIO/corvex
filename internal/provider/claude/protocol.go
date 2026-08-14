package claude

// The Claude CLI stream-json protocol: the wire structs, the line-by-line
// decoder that turns them into types.StreamEvent, and ParseFullOutput for
// callers that only get the finished stdout buffer.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

type rawLine struct {
	Type string `json:"type"`
}

type messageContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type messageBody struct {
	Role    string           `json:"role"`
	Content []messageContent `json:"content"`
}

type assistantLine struct {
	Type    string      `json:"type"`
	Message messageBody `json:"message"`
}

type toolResultLine struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type resultLine struct {
	Type              string  `json:"type"`
	Subtype           string  `json:"subtype"`
	Result            string  `json:"result"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
	DurationMs        int64   `json:"duration_ms"`
}

// toolInput captures the most useful fields the assistant places in a
// `tool_use` event's `input` payload, so we can render a meaningful summary
// for each tool. Different tools use different keys:
//
//	Read/Write/Edit  → file_path
//	Glob             → pattern (and optionally path to scope the search)
//	Grep             → pattern + path
//	Bash             → command
//
// Unknown / less-common fields silently fall through — the progress writer
// degrades to the bare tool name.
type toolInput struct {
	FilePath string `json:"file_path,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
	Path     string `json:"path,omitempty"`
	Command  string `json:"command,omitempty"`
}

// summary returns a single, terse description of the tool target suitable
// for live progress output. Empty when no recognised field was set.
func (t toolInput) summary() string {
	switch {
	case t.FilePath != "":
		return t.FilePath
	case t.Command != "":
		return "$ " + t.Command
	case t.Pattern != "" && t.Path != "":
		return t.Pattern + " in " + t.Path
	case t.Pattern != "":
		return t.Pattern
	case t.Path != "":
		return t.Path
	default:
		return ""
	}
}

func parseNDJSONLine(line []byte) ([]types.StreamEvent, error) {
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, fmt.Errorf("invalid json line: %w", err)
	}

	switch raw.Type {
	case "assistant":
		return parseAssistant(line)
	case "tool_result":
		return parseToolResult(line)
	case "result":
		return parseResult(line)
	case "system":
		return nil, nil
	default:
		return nil, nil
	}
}

func parseAssistant(line []byte) ([]types.StreamEvent, error) {
	var al assistantLine
	if err := json.Unmarshal(line, &al); err != nil {
		return nil, fmt.Errorf("parsing assistant line: %w", err)
	}

	var events []types.StreamEvent
	for _, c := range al.Message.Content {
		switch c.Type {
		case "text":
			events = append(events, types.StreamEvent{
				Type:    types.EventText,
				Content: c.Text,
			})
		case "tool_use":
			ev := types.StreamEvent{
				Type: types.EventToolUse,
				Tool: c.Name,
			}
			var ti toolInput
			if json.Unmarshal(c.Input, &ti) == nil {
				ev.File = ti.FilePath
				if summary := ti.summary(); summary != "" {
					ev.Content = summary
				}
			}
			events = append(events, ev)
		}
	}

	return events, nil
}

func parseToolResult(line []byte) ([]types.StreamEvent, error) {
	var tr toolResultLine
	if err := json.Unmarshal(line, &tr); err != nil {
		return nil, fmt.Errorf("parsing tool_result line: %w", err)
	}

	return []types.StreamEvent{{
		Type:    types.EventToolResult,
		Content: tr.Content,
	}}, nil
}

func parseResult(line []byte) ([]types.StreamEvent, error) {
	var rl resultLine
	if err := json.Unmarshal(line, &rl); err != nil {
		return nil, fmt.Errorf("parsing result line: %w", err)
	}

	return []types.StreamEvent{{
		Type:    types.EventDone,
		Content: rl.Result,
	}}, nil
}

// ParseFullOutput implements provider.CommandBuilder.
func (c *ClaudeCLI) ParseFullOutput(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error) {
	result := &types.ExecuteResult{
		ExitCode:   exitCode,
		DurationMs: elapsed.Milliseconds(),
	}
	var outputParts []string
	var sawResult bool

	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		events, err := parseNDJSONLine([]byte(trimmed))
		if err != nil {
			continue
		}
		for _, ev := range events {
			if ev.Type == types.EventText {
				outputParts = append(outputParts, ev.Content)
			}
		}
		var raw rawLine
		if json.Unmarshal([]byte(trimmed), &raw) == nil && raw.Type == "result" {
			sawResult = true
			var res resultLine
			if json.Unmarshal([]byte(trimmed), &res) == nil {
				result.TokensIn = res.TotalInputTokens
				result.TokensOut = res.TotalOutputTokens
				result.CostUSD = res.TotalCostUSD
				if res.DurationMs > 0 {
					result.DurationMs = res.DurationMs
				}
			}
		}
	}

	result.Output = strings.Join(outputParts, "")
	if exitCode == 0 && !sawResult {
		return result, fmt.Errorf("claude cli produced no result line (truncated output?)")
	}
	return result, nil
}

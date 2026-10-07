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
	Type         string  `json:"type"`
	Subtype      string  `json:"subtype"`
	Result       string  `json:"result"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	DurationMs   int64   `json:"duration_ms"`
	// Usage is where the CLI actually reports tokens. Measured against
	// claude 2.1.292: the result line has NO total_input_tokens /
	// total_output_tokens — every fixture in this repo invented them, so
	// every real run reached the ledger with 0 tokens.
	Usage struct {
		InputTokens              int `json:"input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		OutputTokens             int `json:"output_tokens"`
	} `json:"usage"`
	// The legacy names stay readable, so a recorded stream in that shape
	// keeps meaning what it meant.
	TotalInputTokens  int             `json:"total_input_tokens"`
	TotalOutputTokens int             `json:"total_output_tokens"`
	StructuredOutput  json.RawMessage `json:"structured_output"`
	APIErrorStatus    *int            `json:"api_error_status"`
}

// applyResultLine copies what the final result line reports onto result. One
// function for the two readers (the streaming Execute and the sandbox's
// ParseFullOutput), which used to be two copies of the same field list.
func applyResultLine(result *types.ExecuteResult, res resultLine) {
	result.CostUSD = res.TotalCostUSD
	if res.DurationMs > 0 {
		result.DurationMs = res.DurationMs
	}
	in := res.Usage.InputTokens + res.Usage.CacheCreationInputTokens + res.Usage.CacheReadInputTokens
	out := res.Usage.OutputTokens
	if in == 0 && out == 0 {
		in, out = res.TotalInputTokens, res.TotalOutputTokens
	}
	result.TokensIn, result.TokensOut = in, out
	if len(res.StructuredOutput) > 0 && string(res.StructuredOutput) != "null" {
		result.Structured = []byte(res.StructuredOutput)
	}
	if res.APIErrorStatus != nil {
		result.APIErrorStatus = *res.APIErrorStatus
	}
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
	case "user":
		// Where the CLI actually reports a tool's result. Captured from the
		// wire (testdata/wire_tool_cycle.jsonl): the result arrives as a `user`
		// message whose content is a `tool_result` block carrying the
		// `tool_use_id` of the call it answers.
		//
		// This branch was missing, and nothing noticed for a whole phase: the
		// parser handled a TOP-LEVEL "tool_result" line, the tests fed it that
		// shape, and the two agreed with each other while the CLI emitted
		// something else. The first real run produced 11 tool_use lines and
		// zero tool_result — F5 shipped "start, end, duration" with only the
		// start alive in the field.
		return parseUser(line)
	case "tool_result":
		// The top-level shape. This CLI does not emit it; kept because it costs
		// one branch and removing it would be a guess about every other version
		// and provider.
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
				ID:   c.ID,
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

// userLine is the CLI's tool-result envelope.
type userLine struct {
	Message struct {
		Content []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
}

// parseUser turns the CLI's `user` envelope into tool-result events.
//
// The result's own CONTENT is deliberately dropped. It is the tool's output —
// a file's bytes, a command's stdout — and the only consumer downstream that
// persists anything is the ledger, which is committed. What the pairing needs
// is the id and the moment, not the payload.
func parseUser(line []byte) ([]types.StreamEvent, error) {
	var ul userLine
	if err := json.Unmarshal(line, &ul); err != nil {
		return nil, fmt.Errorf("parsing user line: %w", err)
	}
	var events []types.StreamEvent
	for _, c := range ul.Message.Content {
		if c.Type != "tool_result" {
			continue
		}
		events = append(events, types.StreamEvent{Type: types.EventToolResult, ID: c.ToolUseID})
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
				applyResultLine(result, res)
			}
		}
	}

	result.Output = strings.Join(outputParts, "")
	if exitCode == 0 && !sawResult {
		return result, fmt.Errorf("claude cli produced no result line (truncated output?)")
	}
	return result, nil
}

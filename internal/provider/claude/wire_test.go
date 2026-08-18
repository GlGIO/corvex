package claude

import (
	"bufio"
	"os"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

// The wire, as the CLI actually speaks it.
//
// testdata/wire_tool_cycle.jsonl is the SHAPE captured from a real
// `claude -p … --output-format stream-json --verbose` invocation (values
// scrubbed: the raw capture carries cwd, memory paths, a socket path and
// session ids, none of which belong in a repository). It is written out as
// bytes rather than produced by this package on purpose — that is the only way
// a test can disagree with the parser.
//
// It exists because the parser and its tests agreed with each other for a whole
// phase while the CLI emitted something else: a tool's result arrives as a
// `user` message carrying a `tool_result` block, and the parser was matching a
// TOP-LEVEL `"tool_result"` line that this CLI never sends. The first real run
// produced 11 tool_use lines and zero tool_result, which made F5's "start, end,
// duration" only a start in the field.
func TestParse_RealWireProducesAPairedToolCycle(t *testing.T) {
	f, err := os.Open("testdata/wire_tool_cycle.jsonl")
	if err != nil {
		t.Fatalf("opening the captured wire: %v", err)
	}
	defer f.Close()

	var events []types.StreamEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		got, perr := parseNDJSONLine(line)
		if perr != nil {
			t.Fatalf("parsing %s: %v", line, perr)
		}
		events = append(events, got...)
	}

	var use, result types.StreamEvent
	for _, e := range events {
		switch e.Type {
		case types.EventToolUse:
			use = e
		case types.EventToolResult:
			result = e
		}
	}

	if use.Tool != "Read" {
		t.Fatalf("no tool_use event was parsed from the real wire: %+v", events)
	}
	if result.Type != types.EventToolResult {
		t.Fatal("no tool_result event was parsed: the end of every tool call is invisible, and so is its duration")
	}
	if use.ID == "" || result.ID != use.ID {
		t.Errorf("call and result are not pairable: use.ID=%q result.ID=%q", use.ID, result.ID)
	}
	// The result's payload is a tool's output — a file's bytes, a command's
	// stdout. It has no consumer that needs it and one consumer that must never
	// have it (the committed ledger), so it stops here.
	if result.Content != "" {
		t.Errorf("the tool result carried its payload forward: %q", result.Content)
	}
}

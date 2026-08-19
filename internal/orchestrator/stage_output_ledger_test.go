package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// The tripwire for the fix that made a failed stage's output visible.
//
// internal/step now attaches what a failed command printed to its
// task_complete event, so `--plain` can print it next to the failure line. That
// content is exactly what activity.jsonl must never hold: the file is committed
// by corvex's own auto_commit, which is why activity.Entry carries a tool's NAME
// and never its arguments, and a command's output is the same class of content
// as a command's input.
//
// The property is structural — event.Event.Output has no counterpart in
// activity.Entry and ledgerEntryFromEvent copies field by field — and this test
// is what makes it stay structural. Asserted on the marshalled JSON, because the
// JSON is the file: a future Entry field plus a line in the copy would both be
// needed to break it, and both would be caught here.
func TestLedgerEntry_NeverCarriesAFailedStagesOutput(t *testing.T) {
	t.Parallel()
	const secret = "AWS_SESSION_TOKEN=FwoGZXIvYXdzEBYaDF"

	ev := Event{
		Type:    event.TaskComplete,
		TaskID:  "S03",
		Status:  types.StatusFailed,
		Phase:   event.PhaseValidate,
		Message: "command exit did not pass after 1 iteration(s)",
		Output:  "ERROR: --repository is required\n" + secret,
	}

	entry := ledgerEntryFromEvent(ev)
	buf, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	line := string(buf)
	if strings.Contains(line, secret) || strings.Contains(line, "--repository is required") {
		t.Fatalf("the ledger line is %s — a failed command's output reached the committed file", line)
	}
	// Positive control: the line is not empty of the fields it is supposed to
	// carry, so "the secret is absent" is a claim about a real line rather than
	// about a zero struct.
	if !strings.Contains(line, "command exit did not pass") || !strings.Contains(line, `"task_id":"S03"`) {
		t.Fatalf("the ledger line is %s — it lost the count and the step it belongs to", line)
	}
}

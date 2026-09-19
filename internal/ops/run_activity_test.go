package ops

// A running step says what it is doing.
//
// MEASURED on the first real incident run: seven minutes on S02 with the screen
// saying `RUNNING` and `$0.00` and nothing else, while the ledger beside it held
// 169 lines and was recording a production query every few seconds. The run was
// healthy; the screen was the only part that looked dead, and the owner's read
// of it was "parece que não tá logando nada da run".
//
// Cost cannot fill that gap by construction: it is written when a step
// COMPLETES. So the row needs the one fact the ledger has continuously — the
// last thing that happened, and when.

import (
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
)

func TestActivityLabel_NamesTheToolWhenThereIsOne(t *testing.T) {
	// The server name is part of the answer, not noise: `SmartCarePRD` says the
	// diagnosis is reading production rather than guessing, which is the whole
	// question a person has while watching one.
	got := activityLabel(activity.Entry{Type: "tool_use", Phase: "worker", Tool: "mcp__SmartCarePRD__query"})
	if got != "mcp__SmartCarePRD__query" {
		t.Errorf("label = %q, want the tool name", got)
	}
}

func TestActivityLabel_FallsBackToThePhaseThenTheType(t *testing.T) {
	if got := activityLabel(activity.Entry{Type: "task_start", Phase: "worker"}); got != "worker · task_start" {
		t.Errorf("label = %q, want the phase and the type", got)
	}
	if got := activityLabel(activity.Entry{Type: "sandbox_cleanup"}); got != "sandbox_cleanup" {
		t.Errorf("label = %q, want the bare type", got)
	}
}

// The clock is moved by EVERY line, not only the ones that cost money: the
// question is "is anything happening", and a step whose last line is four
// minutes old answers it either way.
func TestRunTaskRow_LastActivityIsTheNewestLineOfAnyType(t *testing.T) {
	base := time.Date(2026, 9, 19, 20, 46, 0, 0, time.UTC)
	rows := map[string]*RunTaskRow{"S02": {ID: "S02"}}
	entries := []activity.Entry{
		{TaskID: "S02", Type: "task_start", Phase: "worker", Timestamp: base},
		{TaskID: "S02", Type: "tool_use", Phase: "worker", Tool: "Read", Timestamp: base.Add(20 * time.Second)},
		{TaskID: "S02", Type: "tool_use", Phase: "worker", Tool: "mcp__SmartCarePRD__query", Timestamp: base.Add(90 * time.Second)},
		{TaskID: "S03", Type: "tool_use", Tool: "outro", Timestamp: base.Add(200 * time.Second)},
	}
	for _, e := range entries {
		row, ok := rows[e.TaskID]
		if !ok {
			continue
		}
		if !e.Timestamp.IsZero() && !e.Timestamp.Before(row.LastActivityAt) {
			row.LastActivityAt = e.Timestamp
			row.LastActivity = activityLabel(e)
		}
	}
	got := rows["S02"]
	if got.LastActivity != "mcp__SmartCarePRD__query" {
		t.Errorf("last activity = %q, want the newest line of this step", got.LastActivity)
	}
	if !got.LastActivityAt.Equal(base.Add(90 * time.Second)) {
		t.Errorf("last activity at = %v, want the newest timestamp", got.LastActivityAt)
	}
}

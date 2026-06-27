package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/types"
)

func TestPlainRenderer_Verbose(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlainRenderer(&buf, true /*noColor*/, false /*quiet*/)
	events := []orchestrator.Event{
		{Type: orchestrator.EventTaskStart, TaskID: "S01"},
		{Type: orchestrator.EventTaskComplete, TaskID: "S01", Status: types.StatusPassed, DurationMs: 72000, CostUSD: 0.41},
		{Type: orchestrator.EventTaskComplete, TaskID: "S02", Status: types.StatusFailed, Message: "boom"},
		{Type: orchestrator.EventDone},
	}
	for _, ev := range events {
		r.render(ev)
	}
	out := buf.String()
	for _, want := range []string{"S01", "passed", "S02", "failed", "boom", "done"} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q\n--- got ---\n%s", want, out)
		}
	}
	// ASCII fallback (noColor) must not emit ANSI escape codes.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("noColor output should have no ANSI escapes:\n%q", out)
	}
}

func TestPlainRenderer_Quiet(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlainRenderer(&buf, true, true /*quiet*/)
	events := []orchestrator.Event{
		{Type: orchestrator.EventTaskStart, TaskID: "S01"},
		{Type: orchestrator.EventTaskComplete, TaskID: "S01", Status: types.StatusPassed, DurationMs: 1000},
		{Type: orchestrator.EventRetry, TaskID: "S01", Attempt: 1},
		{Type: orchestrator.EventTaskComplete, TaskID: "S02", Status: types.StatusFailed, Message: "boom"},
		{Type: orchestrator.EventError, Message: "fatal thing"},
		{Type: orchestrator.EventDone},
	}
	for _, ev := range events {
		r.render(ev)
	}
	out := buf.String()
	// Progress (start/passed/retry) is suppressed in quiet mode.
	for _, hidden := range []string{"passed", "retry"} {
		if strings.Contains(out, hidden) {
			t.Errorf("quiet output should suppress %q\n--- got ---\n%s", hidden, out)
		}
	}
	// Failures, errors and the final summary still show.
	for _, want := range []string{"failed", "boom", "fatal thing", "done"} {
		if !strings.Contains(out, want) {
			t.Errorf("quiet output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestCompleteProjectArg(t *testing.T) {
	tmp := t.TempDir()
	// Two real projects (foo via spec.md, bar via tasks.md) and a junk dir.
	for _, p := range []struct{ name, file string }{
		{"foo", "spec.md"},
		{"bar", "tasks.md"},
	} {
		dir := filepath.Join(tmp, ".corvex", "tasks", p.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p.file), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with neither spec nor tasks must be excluded.
	if err := os.MkdirAll(filepath.Join(tmp, ".corvex", "tasks", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	got, _ := completeProjectArg(nil, nil, "")
	sort.Strings(got)
	want := []string{"bar", "foo"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("completeProjectArg() = %v, want %v", got, want)
	}

	// Completing a second positional arg returns no project names.
	got2, _ := completeProjectArg(nil, []string{"foo"}, "")
	if len(got2) != 0 {
		t.Errorf("second-arg completion = %v, want empty", got2)
	}
}

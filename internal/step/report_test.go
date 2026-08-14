package step

import "testing"

func TestParseTaskReport_WellFormed(t *testing.T) {
	out := `Did the work.

TASK-REPORT:
SUMMARY: Added the foo endpoint.
DECISIONS:
- used a map, not a slice
- returned 404 on miss
HANDOFF: The next task can call GetFoo(id) which returns (Foo, bool).`

	rep, ok := parseTaskReport(out)
	if !ok {
		t.Fatal("expected ok=true for a well-formed report")
	}
	if rep.Summary != "Added the foo endpoint." {
		t.Errorf("Summary = %q", rep.Summary)
	}
	if len(rep.Decisions) != 2 {
		t.Fatalf("Decisions = %v, want 2", rep.Decisions)
	}
	if rep.Decisions[0] != "used a map, not a slice" {
		t.Errorf("Decisions[0] = %q", rep.Decisions[0])
	}
	if rep.Handoff != "The next task can call GetFoo(id) which returns (Foo, bool)." {
		t.Errorf("Handoff = %q", rep.Handoff)
	}
}

func TestParseTaskReport_MarkdownTolerant(t *testing.T) {
	out := "## Result\n\n**TASK-REPORT:**\n**SUMMARY:** Wired the parser.\n**DECISIONS:**\n* tolerant matching\n**HANDOFF:** Parser lives in report.go."
	rep, ok := parseTaskReport(out)
	if !ok {
		t.Fatal("expected ok=true with markdown wrapping")
	}
	if rep.Summary != "Wired the parser." {
		t.Errorf("Summary = %q", rep.Summary)
	}
	if len(rep.Decisions) != 1 || rep.Decisions[0] != "tolerant matching" {
		t.Errorf("Decisions = %v", rep.Decisions)
	}
	if rep.Handoff != "Parser lives in report.go." {
		t.Errorf("Handoff = %q", rep.Handoff)
	}
}

func TestParseTaskReport_MultilineHandoff(t *testing.T) {
	out := `TASK-REPORT:
SUMMARY: did it.
HANDOFF: line one
line two continues the handoff.`
	rep, ok := parseTaskReport(out)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if rep.Handoff != "line one line two continues the handoff." {
		t.Errorf("Handoff = %q", rep.Handoff)
	}
}

func TestParseTaskReport_Missing(t *testing.T) {
	_, ok := parseTaskReport("I implemented the feature and all tests pass. Done.")
	if ok {
		t.Error("expected ok=false when no TASK-REPORT block is present")
	}
}

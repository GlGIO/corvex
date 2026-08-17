package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

// TestStepFieldsRoundTrip proves gates, evidence and a fan-out template survive
// tasks.md. This is the property the whole phase rests on: tasks.md is the state
// of record, so a gate that does not round-trip is a gate that disappears on
// resume.
func TestStepFieldsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	want := types.Task{
		ID: "S06", Title: "Aplicar migration em STG", Status: types.StatusPending, Kind: "tool", Command: "npm run migrate:stg",
		Gates: []types.Gate{
			{Nature: types.GateHuman, When: types.GateBefore, Prompt: "Aplicar esta migration em STG?", Label: "Aprovação de STG"},
			{Nature: types.GatePolicy, MaxAttempts: 2},
		},
		Evidence: []types.Evidence{
			{Kind: types.EvidenceSQL, Label: "Migration", RequiredReading: true, From: "cat migrations/*.sql"},
		},
		Fanout: &types.Fanout{
			Over: "S05", MaxItems: 20, OnItemFailure: types.OnItemFailureContinue,
			Template: []types.StepSpec{
				{ID: "impl", Kind: "code", Title: "Implementar"},
				{ID: "unit", Kind: "test", Command: "npm test", DependsOn: []string{"impl"}},
			},
		},
		Produces: "items",
		Item:     "migrations/003.sql",
	}
	if err := WriteTasksFile(path, []types.Task{want}, types.DAGSpec{}); err != nil {
		t.Fatalf("WriteTasksFile: %v", err)
	}
	got, _, err := ParseTasksFile(path)
	if err != nil {
		t.Fatalf("ParseTasksFile: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tasks, want 1", len(got))
	}
	g := got[0]
	if len(g.Gates) != 2 || g.Gates[0].Nature != types.GateHuman || g.Gates[0].Prompt != want.Gates[0].Prompt {
		t.Errorf("gates did not round trip: %+v", g.Gates)
	}
	if g.Gates[1].MaxAttempts != 2 {
		t.Errorf("policy knob lost: %+v", g.Gates[1])
	}
	if len(g.Evidence) != 1 || !g.Evidence[0].RequiredReading || g.Evidence[0].From != want.Evidence[0].From {
		t.Errorf("evidence did not round trip: %+v", g.Evidence)
	}
	if g.Fanout == nil || g.Fanout.Over != "S05" || len(g.Fanout.Template) != 2 {
		t.Fatalf("fanout did not round trip: %+v", g.Fanout)
	}
	if g.Fanout.Template[1].DependsOn[0] != "impl" || g.Fanout.OnItemFailure != types.OnItemFailureContinue {
		t.Errorf("fanout template lost detail: %+v", g.Fanout)
	}
	if g.Produces != "items" || g.Item != "migrations/003.sql" {
		t.Errorf("scalars lost: produces=%q item=%q", g.Produces, g.Item)
	}
}

// TestTaskWithoutStepFieldsIsByteIdentical is the compatibility guard: a task
// that declares nothing from F2 must serialise exactly as it did before, because
// the F-1 golden net byte-compares this output.
func TestTaskWithoutStepFieldsIsByteIdentical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	tasks := []types.Task{
		{ID: "S01", Title: "Bootstrap", Status: types.StatusPending, Type: types.TypeGeneral, Description: "Prepare the tree.", Criteria: []string{"Tree exists"}},
		{ID: "S02", Title: "Run tests", Status: types.StatusPending, Kind: "command", Command: "go test ./...", DependsOn: []string{"S01"}},
	}
	if err := WriteTasksFile(path, tasks, types.DAGSpec{GeneratedBy: "corvex-recipe:demo", Dependencies: map[string][]string{"S01": {}, "S02": {"S01"}}}); err != nil {
		t.Fatalf("WriteTasksFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	out := string(data)
	for _, forbidden := range []string{"gates:", "evidence:", "fanout:", "produces:", "fixed_by:", "item:"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a task with no F2 fields emitted %q:\n%s", forbidden, out)
		}
	}
	// The pre-F2 block shape, exactly.
	if !strings.Contains(out, "```yaml\nkind: command\ncommand: \"go test ./...\"\ndepends_on: [S01]\n```\n") {
		t.Errorf("pre-F2 inline block changed shape:\n%s", out)
	}
}

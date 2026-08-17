package task

import (
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

// TestValidTaskID_Shape pins the rule the recipe compiler enforces. The ids that
// matter here are the two F2 mints (`S06/003/apply`) or allows (`build-backend`)
// and the shapes that must stay refused because they cannot round-trip through a
// heading.
func TestValidTaskID_Shape(t *testing.T) {
	valid := []string{"S01", "S9999", "build-backend", "S06/003/apply", "a", "A_1", "step.one", "0start"}
	for _, id := range valid {
		if !ValidTaskID(id) {
			t.Errorf("ValidTaskID(%q) = false, want true", id)
		}
	}
	invalid := []string{"", " ", "has space", "-leading-dash", "/leading-slash", ".leading-dot", "tab\tid", "emoji✅"}
	for _, id := range invalid {
		if ValidTaskID(id) {
			t.Errorf("ValidTaskID(%q) = true, want false", id)
		}
	}
}

// TestHeadingRe_PreF2ShapesUnchanged is the regression guard on opening the id
// pattern. `S01-Title` is the case a greedy general id would have swallowed
// whole, and the alternation ordering is the only reason it still parses as it
// did before F2.
func TestHeadingRe_PreF2ShapesUnchanged(t *testing.T) {
	cases := []struct {
		line    string
		wantID  string
		wantTtl string
	}{
		{"## S01 — Bootstrap ⬜ PENDING", "S01", "Bootstrap"},
		{"## S01 - Bootstrap ⬜ PENDING", "S01", "Bootstrap"},
		{"## S01-Title ⬜ PENDING", "S01", "Title"},
		{"## S12 — Build backend ✅ PASSED", "S12", "Build backend"},
	}
	for _, c := range cases {
		m := headingRe.FindStringSubmatch(c.line)
		if m == nil {
			t.Fatalf("headingRe did not match %q", c.line)
		}
		if m[1] != c.wantID || m[2] != c.wantTtl {
			t.Errorf("%q → id %q title %q, want id %q title %q", c.line, m[1], m[2], c.wantID, c.wantTtl)
		}
	}
}

// TestHeadingRe_MintedIDs covers what the closed pattern could not express.
func TestHeadingRe_MintedIDs(t *testing.T) {
	cases := []struct{ line, wantID string }{
		{"## S06/003/apply — Implementar x ⬜ PENDING", "S06/003/apply"},
		{"## build-backend — Wire the API ⬜ PENDING", "build-backend"},
		{"## S02/before — Reproduz o bug ⬜ PENDING", "S02/before"},
	}
	for _, c := range cases {
		m := headingRe.FindStringSubmatch(c.line)
		if m == nil {
			t.Fatalf("headingRe did not match %q", c.line)
		}
		if m[1] != c.wantID {
			t.Errorf("%q → id %q, want %q", c.line, m[1], c.wantID)
		}
	}
}

// TestNonSSHapedIDNoLongerVanishes is the bug this change exists to close: before
// F2 a task whose id was not `S<digits>` was written to tasks.md and then
// silently dropped on the way back in — no error, no task, no clue.
func TestNonSSHapedIDNoLongerVanishes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	want := []types.Task{
		{ID: "build-backend", Title: "Wire the API", Status: types.StatusPending, Type: types.TypeBackend},
		{ID: "S06/003/apply", Title: "Apply 003", Status: types.StatusPending, Kind: "tool", Command: "true", DependsOn: []string{"build-backend"}},
	}
	if err := WriteTasksFile(path, want, types.DAGSpec{}); err != nil {
		t.Fatalf("WriteTasksFile: %v", err)
	}
	got, _, err := ParseTasksFile(path)
	if err != nil {
		t.Fatalf("ParseTasksFile: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("round trip kept %d task(s), want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Title != want[i].Title {
			t.Errorf("task %d = %q/%q, want %q/%q", i, got[i].ID, got[i].Title, want[i].ID, want[i].Title)
		}
	}
	if got[1].Command != "true" || len(got[1].DependsOn) != 1 {
		t.Errorf("minted-id task lost its body: %+v", got[1])
	}
}

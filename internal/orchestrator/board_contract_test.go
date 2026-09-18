package orchestrator

// The contract between two repositories, executed instead of assumed.
//
// A board tool in the SmartCare repo (`scripts/flow/az-feature-dag.sh`) reads an
// Azure Feature and prints the work list a fan-out consumes. Two programs, two
// repositories, one format — and nothing on either side would notice the day the
// format drifted: the tool's own suite proves it emits what it meant to, and the
// fan-out's tests use fixtures written by hand here. Each half would stay green
// while the pair stopped working.
//
// So the literal below is the tool's REAL stdout, copied from a run against its
// fixture, and what it pins is the only question that matters at the seam: does
// corvex read this as three stories in three dependency waves.
//
// When it breaks, read it as "the two sides disagree", not as "this test is
// stale" — the fix is a decision about the format, not about the string.

import (
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/step"
)

// Copied verbatim from:
//
//	cd ~/projects/yandeh/smartcare/scripts/flow
//	./az-feature-dag.sh --response-file fixtures/feature-dag-tres-stories.json
const boardToolOutput = `[{"id":"59821","type":"User Story","state":"New","title":"Tabela de associacao ticket_pedidos","depends_on":[],"wave":0},{"id":"59822","type":"User Story","state":"Active","title":"Endpoint de vinculo com tab no titulo","depends_on":["59821"],"wave":1},{"id":"59820","type":"User Story","state":"New","title":"Tela do vinculo","depends_on":["59822"],"wave":2}]`

func TestBoardTool_OutputExpandsIntoDependencyWaves(t *testing.T) {
	items := step.ParseItems(boardToolOutput)
	if len(items) != 3 {
		t.Fatalf("the board tool's output parsed into %d items, want 3: %v", len(items), items)
	}

	waves, err := groupIntoWaves(items, "item.wave")
	if err != nil {
		t.Fatalf("grouping by item.wave: %v", err)
	}
	if len(waves) != 3 {
		t.Fatalf("got %d waves, want 3 — the board's Predecessor links are the order", len(waves))
	}

	// One story per wave, in the order the board's dependency links imply. The
	// ids are what the story's steps are named after, so a wave holding the
	// wrong story is a run doing the work in the wrong order.
	wantOrder := []string{"59821", "59822", "59820"}
	for i, w := range waves {
		if len(w.items) != 1 {
			t.Fatalf("wave %d holds %d items, want 1: %v", i, len(w.items), w.items)
		}
		if !strings.Contains(w.items[0], `"id":"`+wantOrder[i]+`"`) {
			t.Errorf("wave %d is story %q, want %q", i, w.items[0], wantOrder[i])
		}
	}

	// The id survives into a step's text as a STRING. The tool quotes its ids for
	// exactly this reason: an id that goes through a float somewhere comes back
	// as 5.9337e+04, and the work item it names stops being findable.
	rendered := substituteItem("Investigar {{ item }}", waves[0].items[0])
	if !strings.Contains(rendered, `"id":"59821"`) {
		t.Errorf("the item did not reach the step's text intact: %q", rendered)
	}
	if strings.Contains(rendered, "e+0") {
		t.Errorf("an id went through a float on the way to the step: %q", rendered)
	}
}

// A board with no Predecessor links at all is the normal case in a team that
// does not model precedence — and it has to mean "one wave, everything ready",
// not an error. The tool emits wave 0 for everyone there; this is the corvex
// half of that agreement.
func TestBoardTool_NoDependenciesMeansOneWave(t *testing.T) {
	flat := `[{"id":"1","wave":0},{"id":"2","wave":0},{"id":"3","wave":0}]`
	waves, err := groupIntoWaves(step.ParseItems(flat), "item.wave")
	if err != nil {
		t.Fatalf("grouping: %v", err)
	}
	if len(waves) != 1 || len(waves[0].items) != 3 {
		t.Fatalf("got %d wave(s) with %v, want one wave holding all three", len(waves), waves)
	}
}

// `{{ item.field }}`: the difference between a queue somebody can scan and a
// wall of JSON.
//
// Found by running the real thing. A fan-out over a board emits objects, so
// every title written with `{{ item }}` reached the run screen, the gate inbox
// and the ledger as the whole item — the three places a person reads to decide
// something.
func TestSubstituteItem_ResolvesOneFieldOfAStructuredItem(t *testing.T) {
	item := `{"id":"59821","title":"Tabela ticket_pedidos","wave":0,"depends_on":["59820"]}`
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "the id, which is what a title and a branch name want",
			in:   "Investigar {{ item.id }}",
			want: "Investigar 59821",
		},
		{
			name: "the four spellings resolve the same",
			in:   "{{item.id}} {{ item.id }} {{.item.id}} {{ .item.id }}",
			want: "59821 59821 59821 59821",
		},
		{
			name: "a number does not come back in scientific notation",
			in:   "onda {{ item.wave }}",
			want: "onda 0",
		},
		{
			name: "the whole item still resolves, and is not eaten by the field form",
			in:   "{{ item.id }} veio de {{ item }}",
			want: `59821 veio de ` + item,
		},
		{
			name: "a list keeps the JSON it arrived as",
			in:   "depende de {{ item.depends_on }}",
			want: `depende de ["59820"]`,
		},
		{
			name: "a field that does not exist stays LITERAL instead of vanishing",
			in:   "Investigar {{ item.titel }}",
			want: "Investigar {{ item.titel }}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := substituteItem(tt.in, item); got != tt.want {
				t.Errorf("substituteItem(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The shape fan-outs had before boards existed — a path, a migration name — is
// not an object, and the field form must leave it alone rather than blank it.
func TestSubstituteItem_APlainItemIsUntouchedByTheFieldForm(t *testing.T) {
	got := substituteItem("aplicar {{ item }} ({{ item.id }})", "20260918-add-column.sql")
	want := "aplicar 20260918-add-column.sql ({{ item.id }})"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

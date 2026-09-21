package recipe

// A chain that points at nothing breaks at the one moment nobody is watching:
// the end of a run, when the person has stopped reading. Every check here is
// about refusing that before it can happen.

import (
	"strings"
	"testing"
)

func chainYAML(next string) string {
	return "name: incident\nstages:\n  - id: S01\n    kind: tool\n    command: \"true\"\n" + next
}

func TestValidate_RefusesAChainThatSaysNothingUseful(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"no recipe named": {
			chainYAML("next:\n  - why: \"publicar\"\n"), "names no recipe",
		},
		"pointing at itself": {
			chainYAML("next:\n  - recipe: incident\n    why: \"de novo\"\n"), "points at itself",
		},
		"an ending that does not exist": {
			chainYAML("next:\n  - recipe: ship\n    why: \"publicar\"\n    when: sometimes\n"), "use `done`",
		},
		// The `why` is not decoration: it is the line beside the button, and
		// the whole row exists to help somebody decide whether to take the leg
		// NOW. A button that says only a name asks them to remember why.
		"no reason to take it": {
			chainYAML("next:\n  - recipe: ship\n"), "no `why`",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := Parse([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = r.Validate()
			if err == nil {
				t.Fatalf("accepted: %s", tc.yaml)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidate_AcceptsTheThreeEndings(t *testing.T) {
	for _, when := range []string{"", "done", "partial", "any"} {
		y := chainYAML("next:\n  - recipe: ship\n    why: \"publicar\"\n")
		if when != "" {
			y += "    when: " + when + "\n"
		}
		r, err := Parse([]byte(y))
		if err != nil {
			t.Fatalf("parse %q: %v", when, err)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("when %q was refused: %v", when, err)
		}
	}
}

// The default is `done`, and it is a statement: a chain is what follows
// SUCCESS, so a recipe that says nothing about the ending means the clean one.
func TestNextStep_DefaultsToDone(t *testing.T) {
	n := NextStep{Recipe: "ship"}
	if n.EffectiveWhen() != "done" {
		t.Errorf("default when = %q, want done", n.EffectiveWhen())
	}
	if !n.AppliesTo("done") {
		t.Error("the default does not apply to a clean ending")
	}
	if n.AppliesTo("partial") {
		t.Error("the default applies to a partial ending — shipping a fix whose proof did not finish")
	}
	if !(NextStep{Recipe: "x", When: "any"}).AppliesTo("partial") {
		t.Error("`any` does not apply to partial")
	}
}

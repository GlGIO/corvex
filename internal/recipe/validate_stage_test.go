package recipe

import (
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func mustParse(t *testing.T, y string) *Recipe {
	t.Helper()
	r, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return r
}

// TestValidate_RejectsUnreadableStageID is the tripwire that replaces a silent
// data loss. Before F2 this recipe compiled, wrote a tasks.md, and the stage was
// gone on the way back in with no error anywhere.
func TestValidate_RejectsUnreadableStageID(t *testing.T) {
	for _, id := range []string{"has space", "-leading", "emoji✅"} {
		r := mustParse(t, "name: x\nstages:\n  - id: \""+id+"\"\n    kind: tool\n    command: \"true\"\n")
		err := r.Validate()
		if err == nil {
			t.Fatalf("id %q: Validate() = nil, want an error", id)
		}
		if !strings.Contains(err.Error(), "read back") {
			t.Errorf("id %q: error %q does not explain the round-trip failure", id, err)
		}
	}
}

// TestValidate_AcceptsMintedAndDashedIDs is the other half: the ids F2 needs must
// pass, or fan-out cannot express itself.
func TestValidate_AcceptsMintedAndDashedIDs(t *testing.T) {
	r := mustParse(t, "name: x\nstages:\n  - id: build-backend\n    kind: tool\n    command: \"true\"\n  - id: S06/003/apply\n    kind: test\n    command: \"true\"\n    depends_on: [build-backend]\n")
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidate_KindRules(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"tool needs command", "name: x\nstages:\n  - id: S01\n    kind: tool\n", "must set a non-empty `command`"},
		{"test needs command", "name: x\nstages:\n  - id: S01\n    kind: test\n", "must set a non-empty `command`"},
		{"code refuses command", "name: x\nstages:\n  - id: S01\n    kind: code\n    command: echo hi\n", "not a command stage"},
		{"human-gate refuses command", "name: x\nstages:\n  - id: S01\n    kind: human-gate\n    command: echo hi\n", "is a human-gate stage"},
		{"unknown kind", "name: x\nstages:\n  - id: S01\n    kind: magic\n", "unknown kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := mustParse(t, c.yaml).Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, c.want)
			}
		})
	}
}

// TestValidate_LegacyKindsStillCompile is the compatibility promise: no recipe
// written before F2 has to be rewritten.
func TestValidate_LegacyKindsStillCompile(t *testing.T) {
	r := mustParse(t, "name: x\nstages:\n  - id: S01\n    kind: task\n  - id: S02\n    kind: command\n    command: \"true\"\n  - id: S03\n    kind: human-gate\n")
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	tasks, _, err := r.Compile()
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Compile keeps the declared word; normalisation happens at dispatch, so
	// tasks.md still says what the user wrote.
	if tasks[1].Kind != "command" || tasks[2].Kind != "human-gate" {
		t.Errorf("Compile rewrote legacy kinds: %q, %q", tasks[1].Kind, tasks[2].Kind)
	}
	if types.NormalizeKind(tasks[2].Kind) != types.KindTool {
		t.Errorf("human-gate should normalise to tool, got %q", types.NormalizeKind(tasks[2].Kind))
	}
}

func TestValidate_GateRules(t *testing.T) {
	head := "name: x\nstages:\n  - id: S01\n    kind: tool\n    command: \"true\"\n    gates:\n      - "
	cases := []struct{ name, gate, want string }{
		{"unknown nature", "nature: vibes\n", "unknown nature"},
		{"unknown when", "nature: human\n        when: sideways\n", "unknown `when`"},
		{"computational needs command", "nature: computational\n", "must set a non-empty `command`"},
		{"inferential refuses command", "nature: inferential\n        command: \"true\"\n", "use `reviewer`/`model`"},
		{"human refuses command", "nature: human\n        command: \"true\"\n", "decides by consent"},
		{"empty policy", "nature: policy\n", "policy gate with no rule"},
		{"policy knob on wrong nature", "nature: human\n        max_attempts: 2\n", "but its nature is"},
		{"expires_after on wrong nature", "nature: policy\n        max_attempts: 2\n        expires_after: 1h\n", "only a human gate waits on"},
		{"unparseable expires_after", "nature: human\n        expires_after: soon\n", "unparseable `expires_after`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := mustParse(t, head+c.gate).Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, c.want)
			}
		})
	}
}

func TestValidate_EvidenceRules(t *testing.T) {
	head := "name: x\nstages:\n  - id: S01\n    kind: tool\n    command: \"true\"\n    evidence:\n      - "
	cases := []struct{ name, ev, want string }{
		{"no label", "kind: diff\n        content: x\n", "missing a `label`"},
		{"unknown kind", "kind: vibes\n        label: L\n        content: x\n", "unknown kind"},
		{"both content and from", "kind: diff\n        label: L\n        content: x\n        from: y\n", "pick one"},
		{"neither content nor from", "kind: diff\n        label: L\n", "sets neither"},
		{"unknown status", "kind: diff\n        label: L\n        status: maybe\n        content: x\n", "unknown status"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := mustParse(t, head+c.ev).Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, c.want)
			}
		})
	}
}

func TestValidate_FanoutRules(t *testing.T) {
	src := "name: x\nstages:\n  - id: S01\n    kind: tool\n    command: \"ls\"\n    produces: items\n  - id: S02\n    fanout:\n      "
	cases := []struct{ name, fan, want string }{
		{"no over", "template:\n        - id: a\n", "must set `over`"},
		{"unknown over", "over: S99\n      template:\n        - id: a\n", "fans out over unknown stage"},
		{"empty template", "over: S01\n      template: []\n", "empty `template`"},
		{"unknown on_item_failure", "over: S01\n      on_item_failure: maybe\n      template:\n        - id: a\n", "unknown on_item_failure"},
		{"template dep outside", "over: S01\n      template:\n        - id: a\n          depends_on: [S01]\n", "only depend on its siblings"},
		{"bad template id", "over: S01\n      template:\n        - id: \"bad id\"\n", "cannot be written to tasks.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := mustParse(t, src+c.fan).Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, c.want)
			}
		})
	}
}

// TestValidate_FanoutSourceMustDeclareProduces catches the wiring mistake that
// would otherwise surface at run time as "zero items" — which looks like success.
func TestValidate_FanoutSourceMustDeclareProduces(t *testing.T) {
	err := mustParse(t, "name: x\nstages:\n  - id: S01\n    kind: tool\n    command: ls\n  - id: S02\n    fanout:\n      over: S01\n      template:\n        - id: a\n          kind: tool\n          command: \"true\"\n").Validate()
	if err == nil || !strings.Contains(err.Error(), "does not declare `produces: items`") {
		t.Fatalf("Validate() = %v, want the produces complaint", err)
	}
}

func TestValidate_ReproNeedsFixedBy(t *testing.T) {
	err := mustParse(t, "name: x\nstages:\n  - id: S01\n    kind: repro\n    command: \"go test\"\n").Validate()
	if err == nil || !strings.Contains(err.Error(), "must set `fixed_by`") {
		t.Fatalf("Validate() = %v, want the fixed_by complaint", err)
	}
	err = mustParse(t, "name: x\nstages:\n  - id: S01\n    kind: tool\n    command: \"true\"\n    fixed_by: S02\n  - id: S02\n    kind: code\n").Validate()
	if err == nil || !strings.Contains(err.Error(), "is not a repro stage") {
		t.Fatalf("Validate() = %v, want the wrong-kind complaint", err)
	}
}

// TestValidate_AcceptsTheAutopilotShape is the phase's acceptance criterion in
// test form: one recipe expressing every kind and every gate nature.
func TestValidate_AcceptsTheAutopilotShape(t *testing.T) {
	y := `
name: feature-pipeline
stages:
  - id: S01
    kind: tool
    command: "az-stories"
    produces: items
  - id: S02
    fanout:
      over: S01
      max_items: 20
      max_parallel: 3
      template:
        - id: impl
          kind: code
          gates:
            - nature: policy
              max_attempts: 2
            - nature: inferential
              label: "Review de código"
        - id: unit
          kind: test
          command: "npm test"
          depends_on: [impl]
  - id: S03
    kind: tool
    command: "./classify.sh"
    depends_on: [S02]
    gates:
      - nature: inferential
        reviewer: dba
  - id: S04
    kind: tool
    command: "npm run migrate:stg"
    depends_on: [S03]
    gates:
      - nature: human
        when: before
        prompt: "Aplicar em STG?"
        label: "Aprovação de STG"
      - nature: computational
        command: "./schema-check.sh"
    evidence:
      - kind: sql
        label: "Migration"
        required_reading: true
        from: "cat migrations/*.sql"
  - id: S05
    kind: tool
    command: "./ship.sh"
    depends_on: [S04]
    gates:
      - nature: policy
        when: before
        branch_not: [main, develop]
`
	r := mustParse(t, y)
	if err := r.Validate(); err != nil {
		t.Fatalf("the autopilot shape must validate, got: %v", err)
	}
	tasks, dag, err := r.Compile()
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(tasks) != 5 || len(dag.Dependencies) != 5 {
		t.Fatalf("compiled %d tasks / %d dag entries, want 5/5", len(tasks), len(dag.Dependencies))
	}
	if tasks[1].Fanout == nil || len(tasks[1].Fanout.Template) != 2 {
		t.Errorf("fanout did not reach the compiled task: %+v", tasks[1].Fanout)
	}
	if len(tasks[3].Gates) != 2 || len(tasks[3].Evidence) != 1 {
		t.Errorf("gates/evidence did not reach the compiled task: %+v", tasks[3])
	}
	if tasks[3].Gates[0].EffectiveWhen() != types.GateBefore || tasks[3].Gates[1].EffectiveWhen() != types.GateAfter {
		t.Errorf("gate positions resolved wrong: %v / %v", tasks[3].Gates[0].EffectiveWhen(), tasks[3].Gates[1].EffectiveWhen())
	}
}

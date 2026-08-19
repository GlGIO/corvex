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
		{"expires_after on wrong nature", "nature: policy\n        max_attempts: 2\n        expires_after: 1h\n", "only a gate that waits on a person"},
		{"unparseable expires_after", "nature: human\n        expires_after: soon\n", "unparseable `expires_after`"},
		{"question with nothing to ask", "nature: question\n", "must set a non-empty `prompt`"},
		{"question refuses command", "nature: question\n        prompt: qual base?\n        command: \"true\"\n", "answered in words"},
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

// TestValidate_QuestionIsAGateARecipeCanDeclare is the positive control for the
// table above: a question with a prompt validates, and it may bound its wait the
// same way a human gate does — the only two natures that park a run on a person.
func TestValidate_QuestionIsAGateARecipeCanDeclare(t *testing.T) {
	r := mustParse(t, "name: x\nstages:\n  - id: S01\n    kind: tool\n    command: \"true\"\n"+
		"    gates:\n      - nature: question\n        prompt: contra qual base?\n        expires_after: 4h\n")
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	tasks, _, err := r.Compile()
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(tasks[0].Gates) != 1 || tasks[0].Gates[0].Nature != types.GateQuestion {
		t.Fatalf("the question did not survive compilation: %+v", tasks[0].Gates)
	}
	// A question guards the action: it asks for something the step needs in
	// order to do the work, and asking afterwards would be a survey.
	if tasks[0].Gates[0].EffectiveWhen() != types.GateBefore {
		t.Errorf("EffectiveWhen = %q, want before", tasks[0].Gates[0].EffectiveWhen())
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

// evidenceStage builds a one-stage recipe whose gate block and required_reading
// mark are the two variables: everything else about the declaration is fixed, so
// what the cases below measure is only whether the lock is announced and whether
// anything on the stage would ever ask a person to acknowledge it.
func evidenceStage(gates string, requiredReading bool) string {
	y := "name: x\nstages:\n  - id: S01\n    kind: tool\n    command: \"true\"\n" + gates +
		"    evidence:\n      - kind: diff\n        label: \"Diff\"\n        from: \"git diff --stat\"\n"
	if requiredReading {
		y += "        required_reading: true\n"
	}
	return y
}

// nonReaderGates are the gate blocks that never park the run on a person, and so
// never collect evidence: everything except human and question, plus the stage
// with no gate at all.
var nonReaderGates = map[string]string{
	"computational only": "    gates:\n      - nature: computational\n        command: \"./check.sh\"\n",
	"inferential only":   "    gates:\n      - nature: inferential\n        reviewer: dba\n",
	"policy only":        "    gates:\n      - nature: policy\n        branch_not: [main]\n",
	"no gate at all":     "",
}

// TestValidate_RequiredReadingNeedsAReader is the fail-closed half of the
// required_reading contract. The lock is armed by the gate file that humanGate
// and questionGate write; every other nature runs, records its own verdict, and
// never calls resolveDeclared. A recipe that declares the lock under one of
// those natures announces a barrier the runner does not build — the exact
// failure mode `required_reading` exists to prevent — so validation refuses it
// rather than letting `recipe show` print a star nobody has to earn.
func TestValidate_RequiredReadingNeedsAReader(t *testing.T) {
	readers := map[string]string{
		"human gate":    "    gates:\n      - nature: human\n        label: \"Aprovar\"\n",
		"question gate": "    gates:\n      - nature: question\n        prompt: \"Qual ambiente?\"\n",
		"human gate after a computational one": "    gates:\n      - nature: computational\n        command: \"true\"\n" +
			"      - nature: human\n        label: \"Aprovar\"\n",
	}
	for name, gates := range readers {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := mustParse(t, evidenceStage(gates, true)).Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil: this stage does park on a person", err)
			}
		})
	}

	for name, gates := range nonReaderGates {
		t.Run("refused/"+name, func(t *testing.T) {
			err := mustParse(t, evidenceStage(gates, true)).Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want a refusal: nobody here can acknowledge the required reading")
			}
			for _, want := range []string{
				`recipe "x": stage "S01" marks evidence "Diff" as ` + "`required_reading: true`",
				"`nature: human` and `nature: question`",
				"move the evidence to the stage whose gate a person answers",
				"announces a barrier the runner",
				"drop `required_reading: true`",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q; it says: %v", want, err)
				}
			}
		})
	}
}

// TestCompile_EvidenceWithoutRequiredReadingIsNotARefusal is the POSITIVE
// CONTROL of the pair, and it guards a user's file rather than a rule.
//
// The first version of this rule refused the whole `evidence:` block on a stage
// with no person-gate. Compile() calls Validate() and `corvex run` compiles
// through that same door, so the refusal did not merely fail `recipe validate`:
// a recipe already written on somebody's disk stopped RUNNING, on a machine
// whose owner had changed nothing. Evidence without `required_reading` announces
// no lock — it is documentation that is not collected today, which is dull, not
// dangerous — so it compiles, and this test fails if the rule ever widens back.
func TestCompile_EvidenceWithoutRequiredReadingIsNotARefusal(t *testing.T) {
	for name, gates := range nonReaderGates {
		t.Run(name, func(t *testing.T) {
			r := mustParse(t, evidenceStage(gates, false))
			if err := r.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil: no lock is announced here", err)
			}
			tasks, _, err := r.Compile()
			if err != nil {
				t.Fatalf("Compile() = %v, want nil: `corvex run` compiles through this same door, "+
					"so a refusal here stops a run that works today", err)
			}
			// The evidence survives compilation verbatim; what the runner does
			// with it is measured in internal/step.
			if len(tasks) != 1 || len(tasks[0].Evidence) != 1 || tasks[0].Evidence[0].Label != "Diff" {
				t.Fatalf("the declared evidence did not reach the task: %+v", tasks)
			}
		})
	}
}

// The legacy `human-gate` kind carries an implied human gate (step.effectiveGates
// synthesises it), so a stage of that kind reads its evidence with no `gates:`
// block of its own. Refusing it would break every recipe written before F2.
func TestValidate_LegacyHumanGateKindReadsItsEvidence(t *testing.T) {
	y := "name: x\nstages:\n  - id: S01\n    kind: human-gate\n    title: Aprovar\n" +
		"    evidence:\n      - kind: diff\n        label: \"Diff\"\n        required_reading: true\n        from: \"git diff --stat\"\n"
	if err := mustParse(t, y).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil: a human-gate stage IS the person", err)
	}
}

// A template step becomes a real task, so it can tell the same lie and is
// refused in the same words.
func TestValidate_FanoutTemplateEvidenceNeedsAReader(t *testing.T) {
	src := "name: x\nstages:\n  - id: S01\n    kind: tool\n    command: \"ls\"\n    produces: items\n" +
		"  - id: S02\n    fanout:\n      over: S01\n      template:\n        - id: check\n          kind: tool\n          command: \"true\"\n" +
		"          gates:\n            - nature: computational\n              command: \"./check.sh\"\n" +
		"          evidence:\n            - kind: diff\n              label: \"Diff\"\n              required_reading: true\n              from: \"git diff --stat\"\n"
	err := mustParse(t, src).Validate()
	if err == nil {
		t.Fatalf("Validate() = nil, want a refusal for the template step")
	}
	if !strings.Contains(err.Error(), `template step "check" marks evidence "Diff" as `+"`required_reading: true`") {
		t.Errorf("the refusal does not name the template step: %v", err)
	}
}

// personGateStage builds a one-stage recipe whose only variable is the gate
// block, so the cases below measure nothing but how many doors the stage puts
// in front of a person.
func personGateStage(kind, command, gates string) string {
	s := "name: x\nstages:\n  - id: S01\n    kind: " + kind + "\n"
	if command != "" {
		s += "    command: \"" + command + "\"\n"
	}
	return s + gates
}

// TestValidate_OneGateOnAPersonPerStage is the fail-closed half of a physical
// limit. The gate a person answers is one file per (run id, step id), claimed
// with O_EXCL; both natures that wait on a person go through the same openGate.
// So a second one on the same step opens a path that already exists and kills
// the run — after somebody approved the first and the work ran. Measured on the
// real Execute path in step.TestTwoGatesOnAPersonCollideOnOneFile; refused here
// so no run ever gets that far.
func TestValidate_OneGateOnAPersonPerStage(t *testing.T) {
	valid := map[string]string{
		// The canonical shape: consent plus as many machine checks as you like.
		"one human gate and one computational": "    gates:\n      - nature: human\n        when: before\n        label: \"Aprovar\"\n" +
			"      - nature: computational\n        when: after\n        command: \"go test ./...\"\n",
		"one human gate and one inferential": "    gates:\n      - nature: human\n        label: \"Aprovar\"\n" +
			"      - nature: inferential\n        when: after\n        reviewer: dba\n",
		"one question and one policy": "    gates:\n      - nature: question\n        prompt: \"contra qual base?\"\n" +
			"      - nature: policy\n        max_attempts: 2\n",
		"a lone human gate":   "    gates:\n      - nature: human\n        label: \"Aprovar\"\n",
		"no gate on a person": "    gates:\n      - nature: computational\n        command: \"true\"\n",
	}
	for name, gates := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := mustParse(t, personGateStage("tool", "true", gates)).Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil: this stage opens exactly one gate file", err)
			}
		})
	}

	// Two human gates on DIFFERENT stages is the fix the message tells authors
	// to apply, so it has to keep validating.
	t.Run("valid/two human gates on two stages", func(t *testing.T) {
		y := "name: x\nstages:\n" +
			"  - id: S01\n    kind: tool\n    command: \"true\"\n    gates:\n      - nature: human\n        label: \"Aprovar o plano\"\n" +
			"  - id: S02\n    kind: tool\n    command: \"true\"\n    depends_on: [S01]\n    gates:\n      - nature: human\n        label: \"Aprovar o resultado\"\n"
		if err := mustParse(t, y).Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil: two stages are two step ids, so two files", err)
		}
	})

	// The legacy kind implies a human gate, and step.effectiveGates skips the
	// implied one when the author declared their own. The validator mirrors
	// that dedup, or every pre-F2 recipe would start failing.
	t.Run("valid/legacy human-gate kind with its own human gate", func(t *testing.T) {
		y := personGateStage("human-gate", "", "    gates:\n      - nature: human\n        label: \"Aprovar\"\n")
		if err := mustParse(t, y).Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil: the implied gate and the declared one are the same gate", err)
		}
	})

	refused := map[string]struct{ yaml, wants string }{
		"two human gates, before and after": {
			personGateStage("tool", "true", "    gates:\n      - nature: human\n        when: before\n        label: \"Aprovar\"\n"+
				"      - nature: human\n        when: after\n        label: \"Conferir\"\n"),
			`"Conferir" (` + "`human`, `when: after`)",
		},
		"two human gates at the same position": {
			personGateStage("tool", "true", "    gates:\n      - nature: human\n        label: \"Aprovar\"\n"+
				"      - nature: human\n        label: \"Conferir\"\n"),
			`"Conferir" (` + "`human`, `when: before`)",
		},
		"a human gate and a question": {
			personGateStage("tool", "true", "    gates:\n      - nature: human\n        label: \"Aprovar\"\n"+
				"      - nature: question\n        when: after\n        prompt: \"contra qual base?\"\n"),
			`"contra qual base?" (` + "`question`, `when: after`)",
		},
		"the legacy kind plus a question": {
			personGateStage("human-gate", "", "    gates:\n      - nature: question\n        prompt: \"contra qual base?\"\n"),
			"the human gate implied by `kind: human-gate`",
		},
	}
	for name, c := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			err := mustParse(t, c.yaml).Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want a refusal: the second gate would fail with `file exists` and kill the run")
			}
			for _, want := range []string{
				`recipe "x": stage "S01" has 2 gates that park the run on a person`,
				"one file per (run id, step id)",
				"`gate open ...: file exists`",
				"kills the whole run after somebody had already answered the first",
				"`when: before` and `when: after` do not separate them",
				"Split them into two stages, the second `depends_on` the first",
				c.wants,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q; it says: %v", want, err)
				}
			}
		})
	}
}

// A fan-out template step becomes a real task per item, so the same two doors
// would collide once per item. orchestrator/fanout.go copies Gates verbatim,
// which is why the rule has to run over the template too.
func TestValidate_OneGateOnAPersonPerFanoutTemplateStep(t *testing.T) {
	y := "name: x\nstages:\n" +
		"  - id: S01\n    kind: tool\n    command: \"ls\"\n    produces: items\n" +
		"  - id: S02\n    kind: tool\n    fanout:\n      over: S01\n      template:\n" +
		"        - id: apply\n          kind: tool\n          command: \"true\"\n          gates:\n" +
		"            - nature: human\n              label: \"Aprovar\"\n" +
		"            - nature: question\n              when: after\n              prompt: \"deu certo?\"\n"
	err := mustParse(t, y).Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want a refusal: every item would open the same two doors on one file")
	}
	if !strings.Contains(err.Error(), `template step "apply" has 2 gates that park the run on a person`) {
		t.Errorf("the refusal does not name the template step: %v", err)
	}
}

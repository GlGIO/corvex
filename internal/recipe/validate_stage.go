package recipe

import (
	"fmt"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// validateStage checks one stage in isolation: id shape, kind, the command
// rules that follow from the kind, loops, gates and evidence. Cross-stage
// checks (dependencies, fan-out sources, repro targets) live in
// validateStageLinks, which runs after every id is known.
func (r *Recipe) validateStage(s Stage) error {
	// The id has to survive a round trip through tasks.md. Before F2 this was
	// unchecked and the failure was silent: the stage compiled, was written to
	// disk, and then vanished on the way back in because the heading parser
	// only recognised `S<digits>`. Refusing here is the whole fix — the parser
	// itself cannot tell an unreadable id from ordinary prose.
	if !task.ValidTaskID(s.ID) {
		return fmt.Errorf("recipe %q: stage id %q cannot be written to tasks.md and read back "+
			"(allowed: letters, digits, `_`, `.`, `-`, `/`, starting with a letter, digit or `_`)", r.Name, s.ID)
	}
	if !knownKinds[s.Kind] {
		return fmt.Errorf("recipe %q: stage %q has unknown kind %q (known: %s)", r.Name, s.ID, s.Kind, knownKindList)
	}

	if err := r.validateStageCommand(s); err != nil {
		return err
	}
	if s.Loop != nil {
		if s.Fanout != nil || !types.NormalizeKind(s.Kind).IsComputational() {
			return fmt.Errorf("recipe %q: stage %q has a `loop` but only computational stages support loops (kind: %q)", r.Name, s.ID, s.Kind)
		}
		if s.Loop.Max < 0 {
			return fmt.Errorf("recipe %q: stage %q loop.max must be >= 0", r.Name, s.ID)
		}
	}

	for i, g := range s.Gates {
		if err := r.validateGate(s, i, g); err != nil {
			return err
		}
	}
	if err := validateOnePersonGate(fmt.Sprintf("recipe %q: stage %q", r.Name, s.ID), s.Kind, s.Gates); err != nil {
		return err
	}
	for i, e := range s.Evidence {
		if err := r.validateEvidence(s, i, e); err != nil {
			return err
		}
	}
	if err := validateRequiredReadingHasAReader(fmt.Sprintf("recipe %q: stage %q", r.Name, s.ID), s.Kind, s.Gates, s.Evidence); err != nil {
		return err
	}
	return r.validateFanout(s)
}

// validateStageCommand enforces the relationship between kind and `command`.
//
// A fan-out stage is exempt: it has no body of its own, only a template. The
// legacy `human-gate` kind is exempt too — it normalises to a tool step but was
// always written without a command, and rejecting it now would break recipes
// that predate F2.
func (r *Recipe) validateStageCommand(s Stage) error {
	if s.Fanout != nil {
		if strings.TrimSpace(s.Command) != "" {
			return fmt.Errorf("recipe %q: stage %q sets both `fanout` and `command`; a fan-out stage runs its template, not a command of its own", r.Name, s.ID)
		}
		return nil
	}
	hasCommand := strings.TrimSpace(s.Command) != ""
	if s.Kind == types.LegacyKindHumanGate {
		if hasCommand {
			return fmt.Errorf("recipe %q: stage %q sets `command` but is a human-gate stage", r.Name, s.ID)
		}
		return nil
	}
	if types.NormalizeKind(s.Kind).IsComputational() {
		if !hasCommand {
			return fmt.Errorf("recipe %q: %s stage %q must set a non-empty `command`", r.Name, types.NormalizeKind(s.Kind), s.ID)
		}
		return nil
	}
	if hasCommand {
		return fmt.Errorf("recipe %q: stage %q sets `command` but is not a command stage (kind: %q)", r.Name, s.ID, s.Kind)
	}
	return nil
}

// validateGate checks one gate declaration. Each nature has exactly the knobs
// that mean something to it; setting a knob from another nature is refused
// rather than ignored, because a silently-ignored `max_attempts` on a human
// gate reads like a cap that is being enforced.
func (r *Recipe) validateGate(s Stage, i int, g types.Gate) error {
	where := fmt.Sprintf("recipe %q: stage %q gate #%d", r.Name, s.ID, i+1)
	if !g.Nature.IsValid() {
		return fmt.Errorf("%s has unknown nature %q (known: computational, inferential, human, policy, question)", where, g.Nature)
	}
	if g.When != "" && g.When != types.GateBefore && g.When != types.GateAfter {
		return fmt.Errorf("%s has unknown `when` %q (known: before, after)", where, g.When)
	}

	switch g.Nature {
	case types.GateComputational:
		if strings.TrimSpace(g.Command) == "" {
			return fmt.Errorf("%s is computational and must set a non-empty `command`", where)
		}
	case types.GateInferential:
		if strings.TrimSpace(g.Command) != "" {
			return fmt.Errorf("%s is inferential; use `reviewer`/`model`, not `command`", where)
		}
	case types.GateHuman:
		if strings.TrimSpace(g.Command) != "" {
			return fmt.Errorf("%s is human; it decides by consent, not by `command`", where)
		}
	case types.GateQuestion:
		// A question with nothing written in it parks a run in front of a blank
		// screen: the prompt IS the gate here, where on a human gate it is an
		// optional note beside the evidence.
		if strings.TrimSpace(g.Prompt) == "" {
			return fmt.Errorf("%s is a question and must set a non-empty `prompt`", where)
		}
		if strings.TrimSpace(g.Command) != "" {
			return fmt.Errorf("%s is a question for a person; it is answered in words, not by `command`", where)
		}
	case types.GatePolicy:
		if g.MaxAttempts < 0 {
			return fmt.Errorf("%s has max_attempts < 0", where)
		}
		if g.MaxCostUSD < 0 {
			return fmt.Errorf("%s has max_cost_usd < 0", where)
		}
		if g.MaxAttempts == 0 && g.MaxCostUSD == 0 && len(g.BranchNot) == 0 {
			return fmt.Errorf("%s is a policy gate with no rule (set max_attempts, max_cost_usd or branch_not)", where)
		}
	}

	// Both natures that park a run on a person may bound the wait, and only
	// those two: nothing else waits, so `expires_after` elsewhere is a value no
	// reader ever consults.
	if g.Nature == types.GateHuman || g.Nature == types.GateQuestion {
		if g.ExpiresAfter != "" {
			if _, err := time.ParseDuration(g.ExpiresAfter); err != nil {
				return fmt.Errorf("%s has an unparseable `expires_after` %q: %w", where, g.ExpiresAfter, err)
			}
		}
	} else if g.ExpiresAfter != "" {
		return fmt.Errorf("%s sets `expires_after`, which only a gate that waits on a person reads", where)
	}
	if g.Nature != types.GatePolicy && (g.MaxAttempts != 0 || g.MaxCostUSD != 0 || len(g.BranchNot) > 0) {
		return fmt.Errorf("%s sets a policy knob (max_attempts / max_cost_usd / branch_not) but its nature is %q", where, g.Nature)
	}
	return nil
}

// validateEvidence checks one declared evidence item. Content and From are
// mutually exclusive and one of them is required: an item with neither is a
// label with nothing behind it, which is exactly the shape of a gate screen
// that looks reviewed and is not.
func (r *Recipe) validateEvidence(s Stage, i int, e types.Evidence) error {
	where := fmt.Sprintf("recipe %q: stage %q evidence #%d", r.Name, s.ID, i+1)
	if strings.TrimSpace(e.Label) == "" {
		return fmt.Errorf("%s is missing a `label` (the approval lock names evidence by label)", where)
	}
	if !e.Kind.IsValid() {
		return fmt.Errorf("%s has unknown kind %q (known: test_output, verdict, diff, sql, link)", where, e.Kind)
	}
	if e.Status != "" && !e.Status.IsValid() {
		return fmt.Errorf("%s has unknown status %q (known: pass, warn, fail)", where, e.Status)
	}
	hasContent := strings.TrimSpace(e.Content) != ""
	hasFrom := strings.TrimSpace(e.From) != ""
	if hasContent && hasFrom {
		return fmt.Errorf("%s sets both `content` and `from`; pick one", where)
	}
	if !hasContent && !hasFrom {
		return fmt.Errorf("%s sets neither `content` nor `from`", where)
	}
	return nil
}

// validateRequiredReadingHasAReader refuses `required_reading: true` declared
// where no gate will ever ask a person to acknowledge it.
//
// # What the rule refuses, and what it deliberately lets through
//
// It refuses ONLY the item marked `required_reading: true`. Evidence declared
// without that mark on a stage whose gates never stop for a person passes, and
// the earlier, wider version of this rule — which refused the whole `evidence:`
// block — was wrong to refuse it. Two reasons, in order of weight:
//
//  1. The defect is a LIE ABOUT A LOCK, and only required_reading tells it.
//     `required_reading: true` announces the approval lock `gate approve`
//     enforces by label: the reader concludes a barrier exists. Evidence without
//     it announces nothing enforceable; at worst it is documentation that is not
//     collected today, which is dull, not dangerous, and a validator that
//     refuses dull things spends the author's trust on nothing.
//  2. The refusal reaches `run`, not just `recipe validate` — see below. A rule
//     that broke every existing file with an inert `evidence:` block would stop
//     runs that work today, on a machine whose owner changed nothing.
//
// # What is true of the evidence this rule lets through
//
// It is not collected. The only code that turns a declared item into evidence
// with content is Executor.resolveDeclared, and its only callers are humanGate
// and questionGate (internal/step/human_gate.go): both park the run and write a
// gate file, whose single consumer is openGate. So on a stage whose gates never
// stop for a person, the `from:` command never runs, the `content:` never
// surfaces, and no screen prints either — `recipe show` lists the item because
// it reads the YAML (internal/ops/recipe_catalog.go), not because anything will
// show it to somebody. That is recorded here, in the README, and measured by
// TestEvidenceWithoutRequiredReadingRunsAndIsNotCollected in internal/step: the
// recipe is accepted, and the claim about what happens to it is a test rather
// than a promise.
//
// # Why the refusal lives in Validate(), which `run` also crosses
//
// Recipe.Compile calls Validate (internal/recipe/recipe.go), and `corvex run`
// compiles through the same door, so this refusal stops a run and not merely a
// `recipe validate`. That is the intended reach and it is the reason the rule
// had to be narrowed rather than kept: the cost of a false refusal here is a
// user's working recipe that stops executing, not a lint they can ignore.
//
// The alternative — refuse only on the explicit `recipe validate` path
// (ops.ValidateRecipe) and let Compile through — was considered and rejected.
// It inverts the severity: the case that survives narrowing is exactly the one
// where a run must NOT proceed, because a recipe that says "a person must read
// this before approval" and has nobody to approve gives the whole pipeline the
// appearance of a reviewed change. Fail-open there would mean the lock is
// missing precisely when it is announced. A validator-only warning is the right
// shape for the inert case, and the inert case is now simply legal.
//
// # Why not the other fix — teach the computational gate to collect evidence
//
// Collecting is the cheap half and it is not the half that matters. A
// computational gate has no screen and no person: running every `from:` command
// on every gate would move `required_reading` from "declared and never
// collected" to "collected and never read", which is the same false comfort at a
// higher bill. The lock means "a person acknowledged this item by label", and
// only a gate that parks a run on a person can produce that acknowledgement.
func validateRequiredReadingHasAReader(where, kind string, gates []types.Gate, evidence []types.Evidence) error {
	label, ok := firstRequiredReading(evidence)
	if !ok || readsEvidence(kind, gates) {
		return nil
	}
	return fmt.Errorf("%s marks evidence %q as `required_reading: true`, but no gate here parks the run on a "+
		"person: that lock is armed by the gate file only `nature: human` and `nature: question` open, and "+
		"`gate approve` is the only command that enforces it — so this recipe announces a barrier the runner "+
		"never builds. Add the human or question gate this reading is for, move the evidence to the stage whose "+
		"gate a person answers, or drop `required_reading: true` (the item itself may stay: without the mark it "+
		"promises nothing, though nothing collects it here either)", where, label)
}

// validateOnePersonGate refuses a stage that would park the run on a person
// twice.
//
// This is a physical limit, not a taste rule. The gate a person answers is a
// file, and there is exactly one per (run id, step id): gate.Path builds
// `.corvex/runs/gates/<run id>-<step id>.json`, and gate.Open claims it with
// O_EXCL on purpose, so that a resumed run cannot silently overwrite a decision
// somebody already made. Both natures that wait on a person go through that one
// door — humanGate and questionGate call the same openGate. So the second such
// gate on a step opens the path the first one already wrote and gets back
// `file exists`, which humanGate wraps in step.Fatal: the whole run dies, and it
// dies AFTER a person approved the first gate, with the step's work already
// done. Measured on the real Execute path — see
// TestTwoGatesOnAPersonCollideOnOneFile in internal/step.
//
// The `when` axis does not save it: `before` and `after` are two calls of
// runGates over the same task, so the file is the same one either way, and two
// `before` gates collide just as hard.
//
// # Why the validator and not a wider gate.Path
//
// The obvious alternative is to give the file a second key — `<run>-<step>-2.json`,
// or the gate's label — and let a step hold as many gates as it likes. That is
// the better feature and it is NOT this fix, because it changes an on-disk
// format: gate files already written on a user's machine are found by today's
// name, `corvex gate list` walks that directory, and a run parked right now
// would have its pending gate become unreachable by the very command that exists
// to answer it. Widening the path is the owner's call, with a migration, not a
// side effect of closing a hole. Recorded here rather than taken.
//
// The narrower alternative — refuse at run time, in step.runGates — was rejected
// for the same reason every other rule here lives in the validator: the author
// finds out at `recipe validate`, before a run exists, instead of finding out
// from a dead run after a colleague already approved something.
func validateOnePersonGate(where, kind string, gates []types.Gate) error {
	declared, implied := personGates(kind, gates)
	total := len(declared)
	if implied {
		total++
	}
	if total < 2 {
		return nil
	}
	names := make([]string, 0, total)
	if implied {
		names = append(names, "the human gate implied by `kind: human-gate`")
	}
	for _, g := range declared {
		names = append(names, describePersonGate(g))
	}
	return fmt.Errorf("%s has %d gates that park the run on a person (%s), and a stage may only have one: "+
		"the gate a person answers is one file per (run id, step id) — `.corvex/runs/gates/<run id>-<step id>.json`, "+
		"claimed with O_EXCL — so the second gate to open finds the file the first one wrote, fails with "+
		"`gate open ...: file exists`, and kills the whole run after somebody had already answered the first. "+
		"`when: before` and `when: after` do not separate them, because both positions run against the same step. "+
		"Split them into two stages, the second `depends_on` the first, so each gate gets a file of its own",
		where, total, strings.Join(names, ", "))
}

// personGates lists the gates on a stage that stop the run in front of a person,
// separating the one the legacy kind implies from the ones the author wrote.
//
// It mirrors step.effectiveGates exactly, including the dedup: `kind:
// human-gate` implies a human gate only when the stage has not declared one, so
// the legacy kind plus an explicit human gate is one gate, not two — the same
// recipe the runner has always accepted. The legacy kind plus a `question`,
// though, really is two doors on one file, and it is refused.
func personGates(kind string, gates []types.Gate) (declared []types.Gate, implied bool) {
	for _, g := range gates {
		if g.Nature == types.GateHuman || g.Nature == types.GateQuestion {
			declared = append(declared, g)
		}
	}
	if kind != types.LegacyKindHumanGate {
		return declared, false
	}
	for _, g := range declared {
		if g.Nature == types.GateHuman {
			return declared, false
		}
	}
	return declared, true
}

// describePersonGate names a gate the way its author would recognise it.
func describePersonGate(g types.Gate) string {
	name := strings.TrimSpace(g.Label)
	if name == "" {
		name = strings.TrimSpace(g.Prompt)
	}
	if name == "" {
		return fmt.Sprintf("a `%s` gate at `when: %s`", g.Nature, g.EffectiveWhen())
	}
	return fmt.Sprintf("%q (`%s`, `when: %s`)", name, g.Nature, g.EffectiveWhen())
}

// readsEvidence reports whether any gate on a stage will look at its evidence.
//
// It mirrors step.effectiveGates on purpose, including the one implied gate that
// exists there: the legacy `human-gate` kind always meant "a node that is
// nothing but a person deciding", so such a stage reads evidence even with no
// `gates:` block of its own. Missing that would refuse every pre-F2 recipe.
func readsEvidence(kind string, gates []types.Gate) bool {
	if kind == types.LegacyKindHumanGate {
		return true
	}
	for _, g := range gates {
		if g.Nature == types.GateHuman || g.Nature == types.GateQuestion {
			return true
		}
	}
	return false
}

// firstRequiredReading names the first item that arms the approval lock.
func firstRequiredReading(evidence []types.Evidence) (string, bool) {
	for _, e := range evidence {
		if e.RequiredReading {
			return e.Label, true
		}
	}
	return "", false
}

// validateFanout checks a fan-out declaration in isolation. `over` is resolved
// later, in validateStageLinks, once every stage id is known.
func (r *Recipe) validateFanout(s Stage) error {
	f := s.Fanout
	if f == nil {
		return nil
	}
	where := fmt.Sprintf("recipe %q: stage %q fanout", r.Name, s.ID)
	if strings.TrimSpace(f.Over) == "" {
		return fmt.Errorf("%s must set `over` (the stage whose output supplies the items)", where)
	}
	if f.MaxItems < 0 {
		return fmt.Errorf("%s has max_items < 0", where)
	}
	if f.MaxParallel < 0 {
		return fmt.Errorf("%s has max_parallel < 0", where)
	}
	switch f.OnItemFailure {
	case "", types.OnItemFailureAbort, types.OnItemFailureContinue:
	default:
		return fmt.Errorf("%s has unknown on_item_failure %q (known: abort, continue)", where, f.OnItemFailure)
	}
	if len(f.Template) == 0 {
		return fmt.Errorf("%s has an empty `template`", where)
	}

	tmplIDs := make(map[string]bool, len(f.Template))
	for _, ts := range f.Template {
		if !task.ValidTaskID(ts.ID) {
			return fmt.Errorf("%s: template step id %q cannot be written to tasks.md and read back", where, ts.ID)
		}
		if tmplIDs[ts.ID] {
			return fmt.Errorf("%s: duplicate template step id %q", where, ts.ID)
		}
		if !knownKinds[ts.Kind] {
			return fmt.Errorf("%s: template step %q has unknown kind %q (known: %s)", where, ts.ID, ts.Kind, knownKindList)
		}
		// A template step becomes a real task (internal/orchestrator/fanout.go
		// copies Gates and Evidence verbatim onto every item), so the same lie
		// is available here and is refused the same way.
		if err := validateRequiredReadingHasAReader(fmt.Sprintf("%s: template step %q", where, ts.ID), ts.Kind, ts.Gates, ts.Evidence); err != nil {
			return err
		}
		// Same for the gate file: a template step becomes a real task with a
		// real step id, so two person-gates on the template collide once per
		// item — the same one-file-per-(run, step) limit, N times over.
		if err := validateOnePersonGate(fmt.Sprintf("%s: template step %q", where, ts.ID), ts.Kind, ts.Gates); err != nil {
			return err
		}
		tmplIDs[ts.ID] = true
	}
	for _, ts := range f.Template {
		for _, dep := range ts.DependsOn {
			if !tmplIDs[dep] {
				return fmt.Errorf("%s: template step %q depends on %q, which is not in the template "+
					"(a template step may only depend on its siblings; use the fan-out stage's own depends_on for outside edges)", where, ts.ID, dep)
			}
			if dep == ts.ID {
				return fmt.Errorf("%s: template step %q depends on itself", where, ts.ID)
			}
		}
	}
	return nil
}

// validateStageLinks resolves the references a stage makes to other stages:
// a fan-out's source and a repro's fixer.
func (r *Recipe) validateStageLinks(s Stage, ids map[string]bool) error {
	if s.Fanout != nil {
		if !ids[s.Fanout.Over] {
			return fmt.Errorf("recipe %q: stage %q fans out over unknown stage %q", r.Name, s.ID, s.Fanout.Over)
		}
		if s.Fanout.Over == s.ID {
			return fmt.Errorf("recipe %q: stage %q fans out over itself", r.Name, s.ID)
		}
		if !r.produces(s.Fanout.Over) {
			return fmt.Errorf("recipe %q: stage %q fans out over %q, which does not declare `produces: items`", r.Name, s.ID, s.Fanout.Over)
		}
	}
	if strings.TrimSpace(s.FixedBy) != "" {
		if types.NormalizeKind(s.Kind) != types.KindRepro {
			return fmt.Errorf("recipe %q: stage %q sets `fixed_by` but is not a repro stage (kind: %q)", r.Name, s.ID, s.Kind)
		}
		if !ids[s.FixedBy] {
			return fmt.Errorf("recipe %q: repro stage %q is fixed_by unknown stage %q", r.Name, s.ID, s.FixedBy)
		}
		if s.FixedBy == s.ID {
			return fmt.Errorf("recipe %q: repro stage %q is fixed_by itself", r.Name, s.ID)
		}
	}
	if types.NormalizeKind(s.Kind) == types.KindRepro && strings.TrimSpace(s.FixedBy) == "" {
		return fmt.Errorf("recipe %q: repro stage %q must set `fixed_by` — its verdict is temporal, "+
			"so it needs the stage that is expected to make the bug stop reproducing", r.Name, s.ID)
	}
	return nil
}

// produces reports whether the named stage declares `produces: items`.
func (r *Recipe) produces(id string) bool {
	for _, s := range r.Stages {
		if s.ID == id {
			return strings.TrimSpace(s.Produces) == "items"
		}
	}
	return false
}

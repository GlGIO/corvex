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
	for i, e := range s.Evidence {
		if err := r.validateEvidence(s, i, e); err != nil {
			return err
		}
	}
	if err := validateEvidenceHasAReader(fmt.Sprintf("recipe %q: stage %q", r.Name, s.ID), s.Kind, s.Gates, s.Evidence); err != nil {
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

// validateEvidenceHasAReader refuses `evidence:` declared where nothing will
// ever read it.
//
// Measured, not assumed. The only code that turns a declared item into evidence
// with content is Executor.resolveDeclared, and its only callers are humanGate
// and questionGate (internal/step/human_gate.go): both park the run and write a
// gate file. The accumulated set has exactly one consumer, openGate, which fills
// that file. So on a stage whose gates never stop for a person, a declared item
// is inert — the `from:` command never runs, the `content:` never surfaces, and
// no screen ever prints either.
//
// # Why the whole block, and not only `required_reading: true`
//
// required_reading is the graver half: it announces the approval lock that
// `gate approve` enforces by label, and a lock that is never armed is the most
// expensive kind of false comfort — the reader believes a barrier exists where
// there is none. But plain evidence on such a stage lies in the same family,
// because `recipe show` lists every declared item straight from the YAML
// (internal/ops/recipe_catalog.go), so a person reading the recipe concludes
// this material will be put in front of somebody. It will not. The alternative
// reading — "declare it anyway, it shows up in the run report" — was checked
// against the code and there is no such report: nothing outside openGate reads
// the evidence set. If one is ever built, this rule loosens in the same commit
// that gives the evidence its reader, not before.
//
// # Why not the other fix — teach the computational gate to collect evidence
//
// Collecting is the cheap half and it is not the half that matters. A
// computational gate has no screen and no person: running every `from:` command
// on every gate would move `required_reading` from "declared and never
// collected" to "collected and never read", which is the same false comfort at a
// higher bill. The lock means "a person acknowledged this item by label", and
// only a gate that parks a run on a person can produce that acknowledgement.
func validateEvidenceHasAReader(where, kind string, gates []types.Gate, evidence []types.Evidence) error {
	if len(evidence) == 0 || readsEvidence(kind, gates) {
		return nil
	}
	msg := fmt.Sprintf("%s declares `evidence:` but has no gate that reads it: declared evidence is collected "+
		"only when a gate parks the run on a person (`nature: human` or `nature: question`), so here the `from:` "+
		"commands never run and nobody is ever shown anything. "+
		"Add the human or question gate this evidence is for, move the evidence to the stage whose gate a person "+
		"answers, or drop the block", where)
	if label, ok := firstRequiredReading(evidence); ok {
		msg += fmt.Sprintf(". Worse here: %q is marked `required_reading: true`, which announces the approval lock "+
			"of `gate approve` — a lock armed by the gate file this stage never opens, so the recipe promises a "+
			"barrier that the runner does not build", label)
	}
	return fmt.Errorf("%s", msg)
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
		if err := validateEvidenceHasAReader(fmt.Sprintf("%s: template step %q", where, ts.ID), ts.Kind, ts.Gates, ts.Evidence); err != nil {
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

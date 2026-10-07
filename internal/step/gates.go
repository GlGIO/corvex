package step

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/types"
)

// effectiveGates is the gate list a task actually runs, including the one its
// kind implies.
//
// The only implied gate is the legacy `human-gate` kind: it always meant "a node
// that is nothing but a person deciding", so it keeps meaning that. Everything
// else is declared. In particular a `code` step does NOT get a synthesised
// inferential gate here — its reviewer is already the independent judge in
// runAITask, and adding a second one would silently double the token bill of
// every AI task in every recipe written before F2.
func effectiveGates(t *types.Task) []types.Gate {
	gates := t.Gates
	if t.Kind != types.LegacyKindHumanGate {
		return gates
	}
	for _, g := range gates {
		if g.Nature == types.GateHuman {
			return gates
		}
	}
	implied := types.Gate{Nature: types.GateHuman, When: types.GateBefore, Label: t.Title}
	return append([]types.Gate{implied}, gates...)
}

// gatesAt returns the gates that run at the given position.
func gatesAt(t *types.Task, when types.GateWhen) []types.Gate {
	var out []types.Gate
	for _, g := range effectiveGates(t) {
		if g.EffectiveWhen() == when {
			out = append(out, g)
		}
	}
	return out
}

// policyKnobs are the policy-gate values the rest of the executor reads as
// configuration rather than evaluating as a check.
//
// max_attempts and max_cost_usd are gates in the sense that matters — a runner
// rule that can stop the work — but they are enforced by the retry loop and the
// cost accounting, not by a call at a point in time. Naming them here keeps the
// recipe honest (the cap lives with the step it caps) without pretending they
// are evaluated like a shell check.
type policyKnobs struct {
	maxAttempts int
	maxCostUSD  float64
}

func policyFor(t *types.Task) policyKnobs {
	var k policyKnobs
	for _, g := range effectiveGates(t) {
		if g.Nature != types.GatePolicy {
			continue
		}
		if g.MaxAttempts > 0 && (k.maxAttempts == 0 || g.MaxAttempts < k.maxAttempts) {
			k.maxAttempts = g.MaxAttempts
		}
		if g.MaxCostUSD > 0 && (k.maxCostUSD == 0 || g.MaxCostUSD < k.maxCostUSD) {
			k.maxCostUSD = g.MaxCostUSD
		}
	}
	return k
}

// runGates evaluates every gate at one position, in declaration order, stopping
// at the first refusal.
//
// Order is the recipe's, not a nature ranking. The canonical migration gate of
// the reference flow chains computational → independent inferential → apply →
// verify → human, and that chain only means anything if the runner honours the
// order the author wrote.
func (e *Executor) runGates(ctx context.Context, r *Run, t *types.Task, when types.GateWhen, acc *evidenceSet) error {
	for _, g := range gatesAt(t, when) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := e.runGate(ctx, r, t, g, acc); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) runGate(ctx context.Context, r *Run, t *types.Task, g types.Gate, acc *evidenceSet) error {
	switch g.Nature {
	case types.GateComputational:
		return e.computationalGate(ctx, r, t, g, acc)
	case types.GateInferential:
		return e.inferentialGate(ctx, r, t, g, acc)
	case types.GatePolicy:
		return e.policyGate(ctx, t, g)
	case types.GateHuman:
		return e.humanGate(ctx, r, t, g, acc)
	case types.GateQuestion:
		return e.questionGate(ctx, r, t, g, acc)
	default:
		// Unreachable through a validated recipe; refusing beats guessing.
		return e.gateRefused(t, g, fmt.Sprintf("unknown gate nature %q", g.Nature))
	}
}

// computationalGate runs a shell check and turns its output into evidence.
func (e *Executor) computationalGate(ctx context.Context, r *Run, t *types.Task, g types.Gate, acc *evidenceSet) error {
	out, err := e.runShellForTask(ctx, r, t, g.Command)
	acc.add(gate.FromCommandOutput(gateLabel(g, "check"), out, err == nil))
	if err != nil {
		refusal := e.gateRefused(t, g, fmt.Sprintf("`%s` exited non-zero: %v", g.Command, err))
		if gr, ok := refusal.(*gateRefusal); ok {
			gr.output = out
		}
		return refusal
	}
	return nil
}

// inferentialGate runs an independent agent over the step.
//
// Independence is structural: a fresh Reviewer means a fresh provider call with
// its own prompt and read-only tools, so the judge never inherits the worker's
// context. That is what "never the author" buys, and it is why no check here
// compares model names — the same model with a clean context is independent, and
// a different model continuing the worker's conversation would not be.
func (e *Executor) inferentialGate(ctx context.Context, r *Run, t *types.Task, g types.Gate, acc *evidenceSet) error {
	model := g.Model
	if strings.TrimSpace(model) == "" {
		model = e.cfg.Provider.Models.Reviewer
	}
	skill := g.Reviewer
	if strings.TrimSpace(skill) == "" {
		skill = e.cfg.SkillRouting["review"]
	}
	// An inferential gate is a judge, so its spend is `review`, not `gate` —
	// the same nature as the reviewer built into a code step. What makes it a
	// gate is where it sits in the recipe, not what it costs.
	e.emit(event.Event{Type: event.ReviewStart, TaskID: t.ID, Phase: event.PhaseReview, Message: g.Describe()})

	// The reviewer reads the tree the work landed in, which for an isolated
	// fan-out item is that item's worktree — judging the run's checkout would be
	// judging a tree the step never touched.
	reviewer := NewReviewer(e.provider, model, e.taskDir(t), skill)
	res, err := reviewer.Review(ctx, t)
	if err != nil {
		if res != nil {
			if ceilErr := e.chargeGate(r, t, res.CostUSD); ceilErr != nil {
				return ceilErr
			}
		}
		acc.add(gate.FromVerdict(gateLabel(g, "review"), "ERROR", "", err.Error(), false))
		return e.gateRefused(t, g, fmt.Sprintf("independent review could not run: %v", err))
	}
	if ceilErr := e.chargeGate(r, t, res.CostUSD); ceilErr != nil {
		return ceilErr
	}
	passed := res.Verdict == VerdictPass
	acc.add(gate.FromVerdict(gateLabel(g, "review"), string(res.Verdict), res.Category, res.Summary, passed))
	e.emit(event.Event{Type: event.ReviewResult, TaskID: t.ID, Phase: event.PhaseReview, Message: string(res.Verdict), CostUSD: res.CostUSD, Model: res.model})
	if !passed {
		return e.gateRefused(t, g, fmt.Sprintf("independent review returned %s: %s", res.Verdict, res.Summary))
	}
	return nil
}

// policyGate evaluates the policy rules that are decided at a point in time.
// The counter and the ceiling are read elsewhere (see policyKnobs); what is left
// here is the branch level — the single most important safety rule of the
// reference flow, and the one that was prose until now.
func (e *Executor) policyGate(ctx context.Context, t *types.Task, g types.Gate) error {
	if len(g.BranchNot) == 0 {
		return nil
	}
	branch, err := e.currentBranch(ctx)
	if err != nil {
		// Refuse rather than pass: a branch rule that cannot read the branch
		// has not been satisfied, and this rule guards merges into main.
		return e.gateRefused(t, g, fmt.Sprintf("cannot read the current branch to enforce branch_not: %v", err))
	}
	for _, forbidden := range g.BranchNot {
		if strings.EqualFold(strings.TrimSpace(forbidden), branch) {
			return e.gateRefused(t, g, fmt.Sprintf("current branch %q is in branch_not", branch))
		}
	}
	return nil
}

// currentBranch reports the checked-out branch of the work directory.
func (e *Executor) currentBranch(ctx context.Context) (string, error) {
	if e.branchFn != nil {
		return e.branchFn(ctx)
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = e.workDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gateRefused records a refusal on the ledger and returns the task-level error.
//
// Task-level, not fatal: a rejected gate fails its step, and the scheduler's
// existing cascade skips that step's dependents while independent branches of
// the DAG keep running. Rejection is not a special case.
func (e *Executor) gateRefused(t *types.Task, g types.Gate, reason string) error {
	// `gate`, whatever the nature that refused. A screen counting how often
	// gates say no must not have to know whether a shell check, a policy rule
	// or an independent judge produced the evidence; the judge's own spend has
	// already accounted for itself on its review lines.
	e.emit(event.Event{
		Type:   event.GateFailed,
		TaskID: t.ID,
		Status: types.StatusFailed,
		Phase:  event.PhaseGate,
		// The REASON rides on the line, not only in the error returned to the
		// process that happened to be running the run.
		//
		// Measured: a person rejected a human gate with "a migration derruba a
		// coluna sem backfill", and the step detail — the canonical surface, the
		// one the UI draws — said `refused by gate` and nothing else. The
		// sentence existed in the terminal that ran the run (gone when it
		// closes) and in the gate file (which no screen joins). The person who
		// refuses and the person who reads the run later are routinely not the
		// same person, and when they are, they are not the same hour.
		//
		// The ledger redacts paths and $HOME from every message (internal/
		// activity), which is the same protection the gate's own label already
		// relies on.
		Message: g.Describe() + ": " + reason,
	})
	charmbraceletlog.Warn("gate refused", "task", t.ID, "gate", g.Describe(), "reason", reason)
	return &gateRefusal{nature: g.Nature, msg: fmt.Sprintf("task %s: gate %s refused: %s", t.ID, g.Describe(), reason)}
}

// gateRefusal is a gate's "no", carrying which nature said it: a deterministic
// check's refusal can be handed back to the worker as a diagnosis (see
// repairAfterGate), and a person's cannot — that one is a decision.
type gateRefusal struct {
	nature types.GateNature
	msg    string
	// output is what a computational gate's command printed. Prompt-only: it
	// is raw command output, so it never rides on a ledger line.
	output string
}

func (g *gateRefusal) Error() string { return g.msg }

// chargeGate bills a gate's LLM spend to the run, honouring the run ceiling and
// any per-step ceiling the recipe declared.
//
// It does not touch the per-task accumulator of runAITask: a gate is not one of
// the task's own attempts, and folding its cost into that number would make the
// retry accounting lie about what the worker spent.
func (e *Executor) chargeGate(r *Run, t *types.Task, cost float64) error {
	if cost <= 0 {
		return nil
	}
	var scratch float64
	_, runTotal := e.book.AddCost(&scratch, r.TotalCostUSD, cost)
	if ceiling := policyFor(t).maxCostUSD; ceiling > 0 && cost > ceiling {
		return Fatal(fmt.Errorf("task %s: gate cost $%.2f exceeded the step's policy ceiling $%.2f", t.ID, cost, ceiling))
	}
	if ceiling := e.cfg.Execution.MaxCostUSD; ceiling > 0 && runTotal > ceiling {
		return Fatal(fmt.Errorf("run aborted: cumulative cost $%.2f exceeded ceiling $%.2f (configure execution.max_cost_usd to raise)", runTotal, ceiling))
	}
	return nil
}

// gateLabel is what the evidence and the ledger call this gate.
func gateLabel(g types.Gate, fallback string) string {
	if l := strings.TrimSpace(g.Label); l != "" {
		return l
	}
	if p := strings.TrimSpace(g.Prompt); p != "" {
		return p
	}
	return fallback
}

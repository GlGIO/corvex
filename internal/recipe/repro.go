package recipe

import (
	"github.com/giovannialves/corvex/internal/types"
)

// Suffixes of the two nodes a repro stage compiles into.
const (
	ReproBefore = "/before"
	ReproAfter  = "/after"
)

// expandRepro rewrites every `kind: repro` stage into the two nodes its temporal
// verdict actually needs.
//
// A repro is not one check run twice by some special executor: it is two
// positions in the graph. The first runs where the stage was declared and passes
// only when the command *fails* — if the bug does not reproduce, the fix is about
// to be written against something that is not there, and that is the single most
// valuable thing this step type can tell you. The second runs after the fixing
// step and passes normally.
//
// Expanding at compile time rather than teaching the scheduler about repro is the
// same choice fan-out makes: the wave loop, the parallelism bound and the failure
// cascade all keep working because what they see is an ordinary DAG.
//
// Anything that depended on the repro stage is repointed at the "after" node —
// depending on a repro means depending on the bug being gone, not on it having
// once been present.
func expandRepro(tasks []types.Task, spec types.DAGSpec) ([]types.Task, types.DAGSpec) {
	rename := make(map[string]string)
	// fixerWaitsFor is the edge this expansion was missing, and its absence
	// undid the whole step type.
	//
	// MEASURED, first time anybody ran a repro stage: with a bug that did NOT
	// reproduce, `S01/before` failed saying "nothing to fix" — and the run then
	// executed the fixer anyway, because nothing in the graph connected the two.
	// The scheduler was right to do it (an independent branch continues when a
	// task fails); the graph was lying. A repro exists to stop a fix from being
	// written against a bug that is not there, and a fix that runs regardless is
	// the same as not having asked.
	fixerWaitsFor := make(map[string][]string)
	for _, t := range tasks {
		if types.NormalizeKind(t.Kind) == types.KindRepro {
			rename[t.ID] = t.ID + ReproAfter
			if t.FixedBy != "" {
				fixerWaitsFor[t.FixedBy] = append(fixerWaitsFor[t.FixedBy], t.ID+ReproBefore)
			}
		}
	}
	if len(rename) == 0 {
		return tasks, spec
	}

	out := make([]types.Task, 0, len(tasks)+len(rename))
	deps := make(map[string][]string, len(tasks)+len(rename))

	for _, t := range tasks {
		if types.NormalizeKind(t.Kind) != types.KindRepro {
			t.DependsOn = dedupe(append(repoint(t.DependsOn, rename), fixerWaitsFor[t.ID]...))
			out = append(out, t)
			deps[t.ID] = t.DependsOn
			continue
		}

		before := t
		before.ID = t.ID + ReproBefore
		before.Title = titleWithSuffix(t.Title, "reproduz antes do fix")
		before.Kind = string(types.KindTool)
		before.ExpectFail = true
		before.FixedBy = ""
		before.DependsOn = repoint(t.DependsOn, rename)

		after := t
		after.ID = t.ID + ReproAfter
		after.Title = titleWithSuffix(t.Title, "não reproduz depois do fix")
		after.Kind = string(types.KindTool)
		after.ExpectFail = false
		after.FixedBy = ""
		// The after node waits on the fixer and on its own before node: running
		// the second half of a temporal verdict without the first half having
		// held would make the verdict meaningless.
		after.DependsOn = dedupe(append(repoint([]string{t.FixedBy}, rename), before.ID))
		// Gates and evidence belong to the verdict, which is the after node.
		before.Gates = nil
		before.Evidence = nil

		out = append(out, before, after)
		deps[before.ID] = before.DependsOn
		deps[after.ID] = after.DependsOn
	}

	spec.Dependencies = deps
	return out, spec
}

func repoint(ids []string, rename map[string]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if to, ok := rename[id]; ok {
			out = append(out, to)
			continue
		}
		out = append(out, id)
	}
	return out
}

func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func titleWithSuffix(title, suffix string) string {
	if title == "" {
		return suffix
	}
	return title + " (" + suffix + ")"
}

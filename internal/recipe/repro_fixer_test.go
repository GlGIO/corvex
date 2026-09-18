package recipe

// The edge that makes a repro mean something.
//
// A repro stage exists to answer one question before any money is spent: does
// the bug actually happen? The `before` node passes only when the command fails,
// and when it passes-because-it-reproduced the fix goes ahead.
//
// MEASURED the first time anybody ran the step type: with a bug that did NOT
// reproduce, `before` failed with "nothing to fix" — and the run executed the
// fixer anyway, then reported a failure at the end. The scheduler behaved
// correctly (a failed task skips its DEPENDENTS and independent branches carry
// on); the graph was the thing that lied, because nothing connected the fixer to
// the question. A fix written against a bug that is not there is exactly what
// this step type was built to prevent, and it was one edge away from preventing
// nothing.

import (
	"strings"
	"testing"
)

const reproRecipe = `name: inc
description: |
  Um repro e o conserto dele.
stages:
  - id: S01
    kind: repro
    title: "O bug"
    fixed_by: S02
    command: "grep -q corrigido app.txt"
  - id: S02
    kind: tool
    title: "O conserto"
    command: "echo corrigido > app.txt"
`

func TestRepro_TheFixerWaitsForTheBugToReproduce(t *testing.T) {
	tasks, spec, err := mustParse(t, reproRecipe).Compile()
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	byID := map[string][]string{}
	for _, tk := range tasks {
		byID[tk.ID] = tk.DependsOn
	}
	for _, want := range []string{"S01/before", "S01/after", "S02"} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("compiled ids = %v, missing %s", keys(byID), want)
		}
	}

	// The edge itself: the fix cannot start before the bug has been shown.
	if !contains(byID["S02"], "S01/before") {
		t.Errorf("the fixer depends on %v — it can run while the bug is unproven", byID["S02"])
	}
	// And the two edges that were already right stay right: the verdict's second
	// half waits for both the fix and its own first half.
	for _, want := range []string{"S02", "S01/before"} {
		if !contains(byID["S01/after"], want) {
			t.Errorf("S01/after depends on %v, missing %s", byID["S01/after"], want)
		}
	}
	// The DAG the runner reads has to carry the same edge as the tasks: they are
	// written to disk separately, and a dependency in one and not the other is a
	// schedule that disagrees with the file a person reads.
	if !contains(spec.Dependencies["S02"], "S01/before") {
		t.Errorf("the DAG spec says S02 depends on %v — the file and the graph disagree", spec.Dependencies["S02"])
	}
}

// The edge goes to the FIXER and to nobody else. `fixed_by` is required on a
// repro (the validator refuses a repro without it — a temporal verdict with no
// second half is not a verdict), so the question is never "what if there is no
// fixer"; it is whether a stage that happens to sit nearby gets dragged into the
// wait. An unrelated step must stay unrelated: a repro that pauses half the
// recipe is a repro people stop writing.
func TestRepro_OnlyTheNamedFixerWaits(t *testing.T) {
	tasks, _, err := mustParse(t, reproRecipe+`  - id: S03
    kind: test
    title: "Nada a ver com o bug"
    command: "true"
`).Compile()
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == "S03" && len(tk.DependsOn) != 0 {
			t.Errorf("S03 gained %v out of nowhere: it never named the repro", tk.DependsOn)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func keys(m map[string][]string) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}

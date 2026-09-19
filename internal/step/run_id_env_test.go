package step

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// $CORVEX_RUN_ID: the run can name itself, and both sides of a step read the
// same name.
//
// The dogfood that produced this: a recipe that has to carry state from one
// step to the next — an id created in S02 and read in S03 — had nowhere to put
// it, because the only variable the runner exported was $CORVEX_RUN_BASE, a
// commit. So the recipe derived its state directory from the run's INPUTS, and
// two concurrent runs with identical inputs shared it: one run's S02 overwrote
// the other's id and its S03 walked the wrong item all the way to the end.
//
// This test asserts the three things that fix has to be true for:
//
//  1. the value is THIS run's id — not empty, and not the starting commit,
//     which is the other variable and the one a recipe would otherwise abuse;
//  2. a stage's `command:` and its gates' `command:` see the SAME value, at
//     both gate positions, because the pattern this exists for has the gate
//     re-running the stage's own call in dry-run mode and landing on the same
//     state directory;
//  3. nothing else was added to the environment. The id is enough to be unique;
//     a repository path or a project name exported alongside it would be a fact
//     about the machine leaking into whatever the recipe writes with it.
func TestRunShell_ExportsTheRunIDToStageAndGateAlike(t *testing.T) {
	repo := gitRepoWithOneCommit(t)
	base := gitHead(t, repo)

	out := t.TempDir()
	path := func(name string) string { return filepath.Join(out, name) }
	// Written by the stage and by each gate; `printf '%s'` rather than `echo`
	// so an empty value produces an empty file instead of a newline that a
	// trimmed comparison could mistake for a value.
	write := func(name, expr string) string {
		return fmt.Sprintf("printf '%%s' %s > %q", expr, path(name))
	}

	e := &Executor{
		workDir: repo,
		cfg:     &config.Config{},
		book:    &Bookkeeper{},
		emit:    func(event.Event) {},
	}
	total := 0.0
	r := &Run{
		TasksPath:    filepath.Join(repo, "tasks.md"),
		AnchorPath:   filepath.Join(repo, "anchor.yaml"),
		Anchor:       &types.AnchorState{},
		Completed:    map[string]bool{},
		DAG:          dag.NewDAG([]types.Task{{ID: "S01"}}),
		TotalCostUSD: &total,
		Identity:     RunIdentity{RunID: "run_5cafe1", Repo: repo, Project: "alpha", Recipe: "board"},
	}
	tk := &types.Task{
		ID: "S01", Title: "Escreve o id", Kind: string(types.KindTool),
		Command: strings.Join([]string{
			write("stage", `"$CORVEX_RUN_ID"`),
			write("base", `"$CORVEX_RUN_BASE"`),
			// Names only: a value may contain anything, and it is the SET of
			// variables corvex adds that this test is about.
			fmt.Sprintf("env | grep -o '^CORVEX_[A-Za-z0-9_]*' | sort > %q", path("names")),
		}, "; "),
		Gates: []types.Gate{
			{Nature: types.GateComputational, Label: "antes", When: types.GateBefore, Command: write("gate-before", `"$CORVEX_RUN_ID"`)},
			{Nature: types.GateComputational, Label: "depois", When: types.GateAfter, Command: write("gate-after", `"$CORVEX_RUN_ID"`)},
		},
	}

	if err := e.Execute(context.Background(), r, tk); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(path(name))
		if err != nil {
			t.Fatalf("the %s command did not run: %v", name, err)
		}
		return string(b)
	}

	stage := read("stage")
	if stage != r.Identity.RunID {
		t.Fatalf("CORVEX_RUN_ID in the stage = %q, want this run's id %q", stage, r.Identity.RunID)
	}
	// Positive control for the comparison below: the fixture really is a git
	// repository, so "the id is not the commit" is a claim about two present
	// values rather than about two empty strings.
	if got := read("base"); got != base {
		t.Fatalf("the fixture's CORVEX_RUN_BASE = %q, want the repo's HEAD %q", got, base)
	}
	if stage == base {
		t.Fatalf("CORVEX_RUN_ID = %q is the starting commit, not an identity", stage)
	}
	for _, name := range []string{"gate-before", "gate-after"} {
		if got := read(name); got != stage {
			t.Errorf("the %s gate saw CORVEX_RUN_ID=%q, the stage saw %q — a recipe cannot derive the same state on both sides", name, got, stage)
		}
	}

	// Leakage: exactly the CORVEX_ variables corvex adds, whatever the machine
	// running the test already had in its own environment.
	//
	// `CORVEX_GATE_ANSWER` joined the list deliberately, and this line is where
	// that deliberation is recorded: it carries the reply to a step's `question`
	// gate, and without it a question is a note — the run asks, a person answers,
	// and the step runs the command it would have run anyway. It is present on
	// every step and empty on the ones that did not ask, so a recipe reads it as
	// `${CORVEX_GATE_ANSWER:-}`.
	want := map[string]bool{"CORVEX_RUN_BASE": true, "CORVEX_RUN_ID": true, "CORVEX_GATE_ANSWER": true}
	for _, kv := range os.Environ() {
		if name := strings.SplitN(kv, "=", 2)[0]; strings.HasPrefix(name, "CORVEX_") {
			want[name] = true
		}
	}
	var wantNames []string
	for name := range want {
		wantNames = append(wantNames, name)
	}
	sort.Strings(wantNames)
	got := strings.Fields(read("names"))
	if strings.Join(got, " ") != strings.Join(wantNames, " ") {
		t.Errorf("the step's CORVEX_ environment is %v, want exactly %v — an added variable is a new promise to every recipe", got, wantNames)
	}
}

// gitRepoWithOneCommit is a repository with a single empty commit, isolated from
// the machine's git configuration so a developer's own defaults cannot change
// what the fixture is.
func gitRepoWithOneCommit(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t.com"}, {"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	return repo
}

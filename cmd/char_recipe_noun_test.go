package cmd

// The `recipe` noun (F4, wave 2): list, show, validate, compile — plus the
// resolution `run start` does between a recipe and an already compiled project
// (F3, D4), which is the one place in this phase where getting it wrong would
// destroy a user's finished work rather than print the wrong thing.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recipeWithGate exercises what `recipe show` exists to display: the two axes
// F2 separated (kind of work × nature of gate) and the evidence behind a gate.
const recipeWithGateYAML = `name: shipit
description: ship with a human gate
stages:
  - id: S01
    title: Build
    kind: tool
    command: "make build"
  - id: S02
    title: Ship
    kind: tool
    depends_on: [S01]
    command: "make ship"
    gates:
      - nature: human
        label: "Aprovar deploy"
        prompt: "Ship to production?"
    evidence:
      - kind: diff
        label: "Diff"
        required_reading: true
        content: "one line"
`

func writeRecipe(t *testing.T, f *fixture, name, body string) {
	t.Helper()
	f.Write(filepath.Join(".corvex", "recipes", name+".yaml"), body)
}

func TestCharacterizeRecipeListEmpty(t *testing.T) {
	f := newFixture(t)
	args := []string{"recipe", "list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_list_empty", scrub(transcript(args, stdout, stderr, err)))
}

// One healthy recipe and one broken file: the broken one is reported on its own
// line instead of failing the listing, because hiding every healthy recipe
// behind one bad YAML is the failure mode the gate inbox already taught.
func TestCharacterizeRecipeListWithABrokenOne(t *testing.T) {
	f := newFixture(t)
	writeRecipe(t, f, "demo", statuslogsRecipeYAML)
	writeRecipe(t, f, "broken", "name: broken\nstages: [oops\n")

	args := []string{"recipe", "list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_list_broken", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRecipeShow(t *testing.T) {
	f := newFixture(t)
	writeRecipe(t, f, "shipit", recipeWithGateYAML)

	args := []string{"recipe", "show", "shipit"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_show", scrub(transcript(args, stdout, stderr, err)))
}

// Reading a recipe must not write one. tasks.md is state, and `recipe show` on
// a compiled project would otherwise be able to reset it.
func TestCharacterizeRecipeShowWritesNothing(t *testing.T) {
	f := newFixture(t)
	writeRecipe(t, f, "demo", statuslogsRecipeYAML)

	if _, _, err := runCLIIn(t, f.Dir, "recipe", "show", "demo"); err != nil {
		t.Fatalf("recipe show: %v", err)
	}
	if _, _, err := runCLIIn(t, f.Dir, "recipe", "validate", "demo"); err != nil {
		t.Fatalf("recipe validate: %v", err)
	}
	if _, statErr := statFile(f.Path(".corvex", "tasks", "demo", "tasks.md")); statErr == nil {
		t.Fatal("recipe show/validate wrote tasks.md; reading a plan must not compile it")
	}
}

func TestCharacterizeRecipeValidate(t *testing.T) {
	f := newFixture(t)
	writeRecipe(t, f, "demo", statuslogsRecipeYAML)

	args := []string{"recipe", "validate", "demo"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_validate", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRecipeValidateRejectsABadRecipe(t *testing.T) {
	f := newFixture(t)
	writeRecipe(t, f, "bad", "name: bad\nstages:\n  - id: S01\n    depends_on: [S99]\n")

	args := []string{"recipe", "validate", "bad"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_validate_bad", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRecipeCompileVerb(t *testing.T) {
	f := newFixture(t)
	writeRecipe(t, f, "demo", statuslogsRecipeYAML)

	args := []string{"recipe", "compile", "demo"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_compile", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRecipeNotFound(t *testing.T) {
	f := newFixture(t)
	args := []string{"recipe", "show", "ghost"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_show_not_found", scrub(transcript(args, stdout, stderr, err)))
}

// D4, half one: a recipe that was never compiled is compiled by `run start`.
// --dry-run stops before spending anything, which is what makes this assertable
// without stubbing the model.
func TestCharacterizeRunStartCompilesARecipeOnDemand(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).GitInit()
	writeRecipe(t, f, "demo", statuslogsRecipeYAML)

	args := []string{"run", "start", "demo", "--dry-run"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_start_compiles", scrub(transcript(args, stdout, stderr, err)))

	if _, statErr := statFile(f.Path(".corvex", "tasks", "demo", "tasks.md")); statErr != nil {
		t.Fatalf("run start did not compile the recipe: %v", statErr)
	}
}

// D4, half two — the one that protects finished work: a recipe edited after its
// DAG was compiled STOPS the run instead of recompiling over the statuses.
func TestCharacterizeRunStartRefusesToRecompileOverProgress(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).GitInit()
	writeRecipe(t, f, "demo", statuslogsRecipeYAML)
	if _, _, err := runCLIIn(t, f.Dir, "recipe", "compile", "demo"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Mark a step finished, then touch the recipe so it is newer than the DAG.
	tasksPath := filepath.Join(".corvex", "tasks", "demo", "tasks.md")
	f.Write(tasksPath, strings.Replace(f.Read(tasksPath), "S01 — Bootstrap ⬜ PENDING", "S01 — Bootstrap ✅ PASSED", 1))
	touchLater(t, f.Path(".corvex", "recipes", "demo.yaml"))

	args := []string{"run", "start", "demo", "--dry-run"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_start_drift", scrub(transcript(args, stdout, stderr, err)))
	if err == nil {
		t.Fatal("a changed recipe over finished work must stop the run")
	}
	if !strings.Contains(f.Read(tasksPath), "✅ PASSED") {
		t.Error("the compiled DAG lost its status anyway — this is the exact defect the stop exists to prevent")
	}

	// And --recompile is the documented way through, which says what it cost.
	args = []string{"run", "start", "demo", "--dry-run", "--recompile"}
	stdout, stderr, err = runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "recipenoun_start_recompile", scrub(transcript(args, stdout, stderr, err)))
	if strings.Contains(f.Read(tasksPath), "✅ PASSED") {
		t.Error("--recompile did not recompile")
	}
}

// --no-recompile keeps the compiled DAG, statuses and all.
func TestCharacterizeRunStartNoRecompileKeepsTheDAG(t *testing.T) {
	privateIndex(t)
	f := newFixture(t).GitInit()
	writeRecipe(t, f, "demo", statuslogsRecipeYAML)
	if _, _, err := runCLIIn(t, f.Dir, "recipe", "compile", "demo"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	tasksPath := filepath.Join(".corvex", "tasks", "demo", "tasks.md")
	f.Write(tasksPath, strings.Replace(f.Read(tasksPath), "S01 — Bootstrap ⬜ PENDING", "S01 — Bootstrap ✅ PASSED", 1))
	touchLater(t, f.Path(".corvex", "recipes", "demo.yaml"))

	if _, _, err := runCLIIn(t, f.Dir, "run", "start", "demo", "--dry-run", "--no-recompile"); err != nil {
		t.Fatalf("--no-recompile must run the compiled DAG as it is: %v", err)
	}
	if !strings.Contains(f.Read(tasksPath), "✅ PASSED") {
		t.Error("--no-recompile rewrote the DAG")
	}
}

// touchLater moves a file's mtime a minute into the future, which is how drift
// is detected. Filesystem timestamp granularity is the reason for a minute
// rather than a millisecond.
func touchLater(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(time.Minute)
	if err := chtimes(path, later); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

// statFile and chtimes keep the os import out of the test body above, where the
// point is the behaviour rather than the syscall.
func statFile(path string) (os.FileInfo, error) { return os.Stat(path) }

func chtimes(path string, when time.Time) error { return os.Chtimes(path, when, when) }

package cmd

// Characterization of the three commands in this group that WRITE to disk:
// recipe (compiles .corvex/recipes/<n>.yaml into tasks.md), reset (rewrites one
// task's status inside tasks.md) and init (scaffolds .corvex/).
//
// For each, two goldens: the transcript (argv + streams + error) and the bytes
// that landed on disk. The second is the one a refactor is most likely to break
// silently, since nothing on stdout changes when the writer's formatting drifts.

import (
	"path/filepath"
	"testing"
)

// statuslogsRecipeYAML exercises the compiler's interesting branches in one
// file: a default-kind stage, an explicit typed stage with a dependency, and a
// command stage with a loop (so loop_until/loop_max reach tasks.md).
const statuslogsRecipeYAML = `name: demo
description: characterization recipe
stages:
  - id: S01
    title: Bootstrap
    description: Prepare the tree.
    criteria:
      - Tree exists
  - id: S02
    title: Build backend
    type: backend
    depends_on: [S01]
    description: Wire the API.
    criteria:
      - Endpoint responds
      - Tests pass
  - id: S03
    title: Verify
    kind: command
    depends_on: [S01, S02]
    command: "go test ./..."
    loop:
      until: "test -f done"
      max: 5
`

// ── recipe ───────────────────────────────────────────────────────────────────

// Main path: compile the recipe. Locks stdout (two lines), the charmlog line on
// stderr, and — separately — the generated tasks.md.
func TestCharacterizeStatuslogsRecipeCompile(t *testing.T) {
	f := newFixture(t)
	f.Write(filepath.Join(".corvex", "recipes", "demo.yaml"), statuslogsRecipeYAML)

	args := []string{"recipe", "demo"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_compile", scrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "statuslogs_recipe_tasks_md", scrub(f.Read(filepath.Join(".corvex", "tasks", "demo", "tasks.md"))))
}

// Compiling twice must be idempotent (the second run overwrites with identical
// bytes). This is the property a refactor of WriteTasksFile could break without
// any visible output change.
func TestCharacterizeStatuslogsRecipeCompileTwice(t *testing.T) {
	f := newFixture(t)
	f.Write(filepath.Join(".corvex", "recipes", "demo.yaml"), statuslogsRecipeYAML)

	if _, _, err := runCLIIn(t, f.Dir, "recipe", "demo"); err != nil {
		t.Fatalf("first compile failed: %v", err)
	}
	first := f.Read(filepath.Join(".corvex", "tasks", "demo", "tasks.md"))

	args := []string{"recipe", "demo"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_compile_twice", scrub(transcript(args, stdout, stderr, err)))

	if second := f.Read(filepath.Join(".corvex", "tasks", "demo", "tasks.md")); second != first {
		t.Errorf("recipe compile is not idempotent\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// After compiling, the project shows up in `list` as "no spec" (tasks.md but no
// spec.md) — the cross-command consequence of recipe writing only tasks.md.
func TestCharacterizeStatuslogsRecipeThenList(t *testing.T) {
	f := newFixture(t)
	f.Write(filepath.Join(".corvex", "recipes", "demo.yaml"), statuslogsRecipeYAML)

	if _, _, err := runCLIIn(t, f.Dir, "recipe", "demo"); err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	args := []string{"list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_then_list", scrub(transcript(args, stdout, stderr, err)))
}

// And `status` reads the compiled DAG back: every stage PENDING, dependencies
// preserved. Proves the writer/parser round-trip that the recipe path relies on.
func TestCharacterizeStatuslogsRecipeThenStatus(t *testing.T) {
	f := newFixture(t)
	f.Write(filepath.Join(".corvex", "recipes", "demo.yaml"), statuslogsRecipeYAML)

	if _, _, err := runCLIIn(t, f.Dir, "recipe", "demo"); err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	args := []string{"status", "demo"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_then_status", scrub(transcript(args, stdout, stderr, err)))
}

// No such recipe file: the error carries the absolute path it looked at.
func TestCharacterizeStatuslogsRecipeNotFound(t *testing.T) {
	f := newFixture(t)

	args := []string{"recipe", "nope"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_not_found", scrub(transcript(args, stdout, stderr, err)))
}

// Malformed YAML: recipe.Parse's error, unwrapped by runRecipe.
func TestCharacterizeStatuslogsRecipeParseError(t *testing.T) {
	f := newFixture(t)
	f.Write(filepath.Join(".corvex", "recipes", "broken.yaml"), "name: broken\nstages: [oops\n")

	args := []string{"recipe", "broken"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_parse_error", scrub(transcript(args, stdout, stderr, err)))
}

// Valid YAML, invalid recipe: a dependency on a stage that does not exist.
// Compile() runs Validate() first, so nothing is written.
func TestCharacterizeStatuslogsRecipeUnknownDependency(t *testing.T) {
	f := newFixture(t)
	f.Write(filepath.Join(".corvex", "recipes", "bad.yaml"),
		"name: bad\nstages:\n  - id: S01\n    title: One\n    depends_on: [S99]\n")

	args := []string{"recipe", "bad"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_unknown_dep", scrub(transcript(args, stdout, stderr, err)))

	if _, statErr := statuslogsStat(f.Path(".corvex", "tasks", "bad", "tasks.md")); statErr == nil {
		t.Error("invalid recipe wrote tasks.md; validation is supposed to run before any write")
	}
}

// A recipe with no stages at all.
func TestCharacterizeStatuslogsRecipeNoStages(t *testing.T) {
	f := newFixture(t)
	f.Write(filepath.Join(".corvex", "recipes", "hollow.yaml"), "name: hollow\ndescription: nothing\n")

	args := []string{"recipe", "hollow"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_no_stages", scrub(transcript(args, stdout, stderr, err)))
}

// ── reset ────────────────────────────────────────────────────────────────────

// Main path: reset the PASSED S01 back to PENDING. Two goldens — the transcript
// (stdout empty, one charmlog line on stderr) and the rewritten tasks.md, which
// is where a writer refactor would show up.
func TestCharacterizeStatuslogsResetTask(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"reset", "alpha", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_reset_task", scrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "statuslogs_reset_tasks_md", scrub(f.Read(filepath.Join(".corvex", "tasks", "alpha", "tasks.md"))))
}

// The rewrite is observable through status too: 1/3 done becomes 0/3.
func TestCharacterizeStatuslogsResetThenStatus(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	if _, _, err := runCLIIn(t, f.Dir, "reset", "alpha", "S01"); err != nil {
		t.Fatalf("reset failed: %v", err)
	}

	args := []string{"status", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_reset_then_status", scrub(transcript(args, stdout, stderr, err)))
}

// Lower-case task arg is upper-cased before the lookup, same as logs.
func TestCharacterizeStatuslogsResetLowercaseArg(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"reset", "alpha", "s02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_reset_lowercase", scrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "statuslogs_reset_lowercase_tasks_md", scrub(f.Read(filepath.Join(".corvex", "tasks", "alpha", "tasks.md"))))
}

// Resetting an already-PENDING task is accepted and still rewrites the file.
func TestCharacterizeStatuslogsResetAlreadyPending(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"reset", "alpha", "S03"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_reset_already_pending", scrub(transcript(args, stdout, stderr, err)))
}

// Unknown task ID: the error names the tasks.md path.
func TestCharacterizeStatuslogsResetTaskNotFound(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"reset", "alpha", "S99"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_reset_task_not_found", scrub(transcript(args, stdout, stderr, err)))
}

// Unknown project: the failure comes from ParseTasksFile, wrapped by reset's
// own "resetting task:" prefix.
func TestCharacterizeStatuslogsResetUnknownProject(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"reset", "nope", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_reset_unknown_project", scrub(transcript(args, stdout, stderr, err)))
}

// Arity: reset needs exactly two args.
func TestCharacterizeStatuslogsResetMissingTaskArg(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"reset", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_reset_missing_task_arg", scrub(transcript(args, stdout, stderr, err)))
}

// ── init ─────────────────────────────────────────────────────────────────────

// Main path in a virgin directory. Two goldens: the transcript, and the tree of
// everything created (mode + relative path), which locks both the directory set
// and the file permissions.
func TestCharacterizeStatuslogsInit(t *testing.T) {
	dir := t.TempDir()

	args := []string{"init"}
	stdout, stderr, err := runCLIIn(t, dir, args...)
	goldenAssert(t, "statuslogs_init", scrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "statuslogs_init_tree", statuslogsTree(t, dir))
}

// The .corvex/.gitignore init drops so materialised secrets never get committed.
func TestCharacterizeStatuslogsInitGitignore(t *testing.T) {
	dir := t.TempDir()

	if _, _, err := runCLIIn(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	data, readErr := statuslogsReadFile(filepath.Join(dir, ".corvex", ".gitignore"))
	if readErr != nil {
		t.Fatalf("reading .corvex/.gitignore: %v", readErr)
	}
	goldenAssert(t, "statuslogs_init_gitignore", data)
}

// Second init in the same directory refuses. Note the check is a bare Stat on
// .corvex — nothing else is inspected, and no file already written is touched.
func TestCharacterizeStatuslogsInitAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCLIIn(t, dir, "init"); err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	args := []string{"init"}
	stdout, stderr, err := runCLIIn(t, dir, args...)
	goldenAssert(t, "statuslogs_init_already_exists", scrub(transcript(args, stdout, stderr, err)))
}

// A pre-existing .corvex/ that init did not create is enough to block it, which
// is how every fixture in this file behaves.
func TestCharacterizeStatuslogsInitRefusesExistingFixture(t *testing.T) {
	f := newFixture(t)

	args := []string{"init"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_init_refuses_fixture", scrub(transcript(args, stdout, stderr, err)))
}

// After init, list works and reports no projects — the scaffold is consistent
// with what the readers in this group expect.
func TestCharacterizeStatuslogsInitThenList(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCLIIn(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	args := []string{"list"}
	stdout, stderr, err := runCLIIn(t, dir, args...)
	goldenAssert(t, "statuslogs_init_then_list", scrub(transcript(args, stdout, stderr, err)))
}

package cmd

// Characterization of the shared preamble every command in this group runs
// before doing its own work: loadConfig() then requireCorvexDir(). Those two
// produce the errors a user actually hits first, and each command wraps them
// differently (or not at all), so each gets its own golden.

import (
	"os"
	"path/filepath"
	"testing"
)

// statuslogsBrokenConfigYAML is not valid YAML, so config.Load fails and
// loadConfig returns the "loading config:" wrapper — the earliest failure any
// of these commands can produce.
const statuslogsBrokenConfigYAML = "project:\n  name: [oops\n"

// ── loadConfig failure ───────────────────────────────────────────────────────

// One golden per command so a refactor that moves the config load (or starts
// wrapping its error) cannot slip through on the strength of the others.
func TestCharacterizeStatuslogsBrokenConfig(t *testing.T) {
	cases := []struct {
		golden string
		args   []string
	}{
		{"statuslogs_badconfig_list", []string{"list"}},
		{"statuslogs_badconfig_status", []string{"status", "alpha"}},
		{"statuslogs_badconfig_logs", []string{"logs", "alpha"}},
		{"statuslogs_badconfig_reset", []string{"reset", "alpha", "S01"}},
		{"statuslogs_badconfig_recipe", []string{"recipe", "demo"}},
	}

	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)
			f.Write(filepath.Join(".corvex", "config.yaml"), statuslogsBrokenConfigYAML)

			stdout, stderr, err := runCLIIn(t, f.Dir, tc.args...)
			goldenAssert(t, tc.golden, scrub(transcript(tc.args, stdout, stderr, err)))
		})
	}
}

// ── requireCorvexDir failure ─────────────────────────────────────────────────

// Same directory (no .corvex/ anywhere) through every command. list and logs
// have their own cases in the sibling files; these three complete the set.
func TestCharacterizeStatuslogsNoCorvexDir(t *testing.T) {
	cases := []struct {
		golden string
		args   []string
	}{
		{"statuslogs_nocorvex_status", []string{"status", "alpha"}},
		{"statuslogs_nocorvex_reset", []string{"reset", "alpha", "S01"}},
		{"statuslogs_nocorvex_recipe", []string{"recipe", "demo"}},
	}

	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, tc.args...)
			goldenAssert(t, tc.golden, scrub(transcript(tc.args, stdout, stderr, err)))
		})
	}
}

// A .corvex symlink whose target is gone: requireCorvexDir distinguishes this
// from "missing" and returns the recreate-the-symlink hint instead.
func TestCharacterizeStatuslogsBrokenCorvexSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, ".corvex")); err != nil {
		t.Fatalf("creating dangling symlink: %v", err)
	}

	args := []string{"list"}
	stdout, stderr, err := runCLIIn(t, dir, args...)
	goldenAssert(t, "statuslogs_broken_corvex_symlink", scrub(transcript(args, stdout, stderr, err)))
}

// ── recipe: I/O failures around the compile ──────────────────────────────────

// The recipe path exists but is a directory, so os.ReadFile fails with
// something other than IsNotExist and takes the second error branch.
func TestCharacterizeStatuslogsRecipePathIsDir(t *testing.T) {
	f := newFixture(t)
	f.Mkdir(filepath.Join(".corvex", "recipes", "dir.yaml"))

	args := []string{"recipe", "dir"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_path_is_dir", scrub(transcript(args, stdout, stderr, err)))
}

// A regular file already sits where the project directory must go, so MkdirAll
// fails after the recipe compiled successfully — the "compiled but could not
// persist" branch.
func TestCharacterizeStatuslogsRecipeProjectDirBlocked(t *testing.T) {
	f := newFixture(t)
	f.Write(filepath.Join(".corvex", "recipes", "blocked.yaml"), statuslogsRecipeYAML)
	f.Write(filepath.Join(".corvex", "tasks", "blocked"), "not a directory\n")

	args := []string{"recipe", "blocked"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_recipe_dir_blocked", scrub(transcript(args, stdout, stderr, err)))
}

// NOT characterized: runRecipe's final branch, where WriteTasksFile fails after
// a successful compile. It is reachable (mkdir a directory at
// .corvex/tasks/<n>/tasks.md and the atomic rename fails), but the error text is
// the raw syscall message and rename-onto-a-directory reports differently across
// kernels ("file exists" on darwin, "directory not empty"/"is a directory" on
// linux). A golden that flips with the OS is worse than a declared gap.

// ── init: unwritable working directory ───────────────────────────────────────

// A read-only cwd makes the very first MkdirAll fail. Skipped for root (which
// ignores the mode bits) and skipped if the chmod turns out not to bite, so the
// case can never flake into a false red.
func TestCharacterizeStatuslogsInitUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode bits do not prevent mkdir")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	probe := filepath.Join(dir, "probe")
	if err := os.Mkdir(probe, 0o755); err == nil {
		_ = os.Remove(probe)
		t.Skip("directory is still writable after chmod 0555")
	}

	args := []string{"init"}
	stdout, stderr, err := runCLIIn(t, dir, args...)
	goldenAssert(t, "statuslogs_init_unwritable", scrub(transcript(args, stdout, stderr, err)))
}

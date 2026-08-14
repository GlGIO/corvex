package cmd

// Characterization of `corvex logs` plus the two truncation-sensitive status
// cases that need their own fixture. LEI 1: goldens record today's bytes,
// including the trailing blank line showTaskLog leaves behind.

import (
	"path/filepath"
	"testing"
)

// statuslogsMinimalTasksMD has a task with no description, no criteria and no
// files — every optional block in showTaskLog skipped at once.
const statuslogsMinimalTasksMD = "---\ngenerated_by: characterize\ndag:\n  S01: []\n---\n\n" +
	"## S01 — Bare Task ⬜ PENDING\n"

// ── status: byte-wise title truncation ───────────────────────────────────────

// The title is 60+ bytes and byte 39 falls inside the "ç" of "configuração",
// so status.go's title[:maxTitleLen-1] cut splits a UTF-8 rune. The golden
// records the resulting replacement character. NOT a fix — see the report.
func TestCharacterizeStatuslogsStatusSplitRuneTitle(t *testing.T) {
	tasks := "---\ngenerated_by: characterize\ndag:\n  S01: []\n---\n\n" +
		"## S01 — Migração para o padrão de configuração unificada e validação ⬜ PENDING\n\n" +
		"```yaml\ntype: general\n```\n\n" +
		"### O que fazer\nLong title.\n"
	f := newFixture(t).AddProject("longtitle", fixtureSpecMD, tasks)

	args := []string{"status", "longtitle"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_split_rune_title", scrub(transcript(args, stdout, stderr, err)))
}

// ── logs ─────────────────────────────────────────────────────────────────────

// Main path, whole project: three task blocks separated by "---" + blank line,
// each ending with the extra blank line showTaskLog prints.
func TestCharacterizeStatuslogsLogsAll(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"logs", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_all", scrub(transcript(args, stdout, stderr, err)))
}

// Single PASSED task: criteria render as "[✓]" and the Files block lists
// created ("+") before modified ("~").
func TestCharacterizeStatuslogsLogsSingleTaskPassed(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"logs", "alpha", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_single_passed", scrub(transcript(args, stdout, stderr, err)))
}

// Single non-PASSED task, requested in lower case: runLogs upper-cases the arg
// before lookup, and criteria stay unchecked ("[ ]").
func TestCharacterizeStatuslogsLogsSingleTaskLowercaseArg(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"logs", "alpha", "s02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_single_lowercase", scrub(transcript(args, stdout, stderr, err)))
}

// With anchor.yaml present, the completed entry for S01 contributes Summary and
// Decisions blocks. Note which anchor fields do NOT surface (files_created /
// files_modified) — that asymmetry is part of the recorded behaviour.
func TestCharacterizeStatuslogsLogsWithAnchor(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)
	f.Write(filepath.Join(".corvex", "tasks", "alpha", "anchor.yaml"), statuslogsAnchorYAML)

	args := []string{"logs", "alpha", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_with_anchor", scrub(transcript(args, stdout, stderr, err)))
}

// Whole project with an anchor: proves the completion block only attaches to the
// task the anchor names, and that block ordering survives the "---" separators.
func TestCharacterizeStatuslogsLogsAllWithAnchor(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)
	f.Write(filepath.Join(".corvex", "tasks", "alpha", "anchor.yaml"), statuslogsAnchorYAML)

	args := []string{"logs", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_all_with_anchor", scrub(transcript(args, stdout, stderr, err)))
}

// A task with none of the optional sections: only the heading line and the
// trailing blank line.
func TestCharacterizeStatuslogsLogsMinimalTask(t *testing.T) {
	f := newFixture(t).AddProject("bare", fixtureSpecMD, statuslogsMinimalTasksMD)

	args := []string{"logs", "bare"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_minimal", scrub(transcript(args, stdout, stderr, err)))
}

// Unknown task ID: showTaskLog's error, returned before anything is printed.
func TestCharacterizeStatuslogsLogsTaskNotFound(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"logs", "alpha", "S99"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_task_not_found", scrub(transcript(args, stdout, stderr, err)))
}

// Missing tasks.md: same wrapped ReadFile error as status.
func TestCharacterizeStatuslogsLogsMissingTasks(t *testing.T) {
	f := newFixture(t).AddProject("beta", fixtureSpecMD, "")

	args := []string{"logs", "beta"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_missing_tasks", scrub(transcript(args, stdout, stderr, err)))
}

// Empty tasks.md: the loop body never runs, so logs succeeds printing nothing.
func TestCharacterizeStatuslogsLogsEmptyTasks(t *testing.T) {
	f := newFixture(t).AddProject("empty", fixtureSpecMD, "\n\n")

	args := []string{"logs", "empty"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_empty_tasks", scrub(transcript(args, stdout, stderr, err)))
}

// Arity: logs accepts 1 or 2 args.
func TestCharacterizeStatuslogsLogsTooManyArgs(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"logs", "alpha", "S01", "extra"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_logs_too_many_args", scrub(transcript(args, stdout, stderr, err)))
}

// No .corvex/ at all: requireCorvexDir fires before any parsing.
func TestCharacterizeStatuslogsLogsNoCorvexDir(t *testing.T) {
	args := []string{"logs", "alpha"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "statuslogs_logs_no_corvex", scrub(transcript(args, stdout, stderr, err)))
}

package cmd

// Characterization of `corvex plan` (including --reanchor) and `corvex start`.
//
// These two commands are the only ones in cmd/ that WRITE the plan itself, so
// the goldens here deliberately cover three surfaces per case:
//
//  1. the transcript (argv + stdout + stderr + returned error),
//  2. the bytes of every file the command wrote (tasks.md, anchor.yaml,
//     spec.md),
//  3. the fact that a file was left ALONE when the command claims it was.
//
// Everything that touches the provider runs against a scripted NDJSON stub
// (planstartStubClaudeSaying) — never the real Claude CLI. See the harness
// notes in characterize_test.go: provider.NewProvider ignores PATH, so
// CORVEX_CLAUDE_BIN (set by stubClaude) is the only brake.
//
// LAW OF THIS PHASE: these goldens record what the code DOES today, bugs
// included. Nothing here is a bug report; see the agent's bugsObserved.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ── planstart helpers ────────────────────────────────────────────────────────

// planstartResultLine is the claude-cli `result` line every scripted stream
// ends with. Fixed cost/token/duration numbers so anything the CLI derives from
// them (the "$0.42 spent" line the grill loop prints) stays byte-stable and can
// be locked with scrubExcept(..., "cost").
const planstartResultLine = `{"type":"result","subtype":"success","result":"stub done",` +
	`"total_cost_usd":0.42,"total_input_tokens":1200,"total_output_tokens":800,"duration_ms":1500}`

// planstartStubClaudeSaying installs a fake `claude` binary that replays a
// two-line NDJSON stream: one assistant text event carrying body, then the
// result line. That is exactly the shape internal/provider/claude parses, so
// the orchestrator (Planner, Griller) sees body as the model's full output.
//
// The payload is written to a file and catted rather than echoed, so no amount
// of quoting in body can break the shell script.
func planstartStubClaudeSaying(t *testing.T, body string) {
	t.Helper()

	assistant, err := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": body},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshalling stub assistant line: %v", err)
	}

	payload := filepath.Join(t.TempDir(), "stream.ndjson")
	stream := string(assistant) + "\n" + planstartResultLine + "\n"
	if err := os.WriteFile(payload, []byte(stream), 0o600); err != nil {
		t.Fatalf("writing stub stream: %v", err)
	}
	// /bin/cat by absolute path: the stub's own dir is prepended to PATH and we
	// do not want to depend on what else is resolvable from there.
	stubClaude(t, "/bin/cat "+payload)
}

// planstartGeneratedTasksMD is what the stubbed Planner "emits" — a valid
// single-task tasks.md, deliberately different from fixtureTasksMD so an
// overwrite is visible in the golden.
const planstartGeneratedTasksMD = "---\ngenerated_by: planner-stub\ndag:\n  S01: []\n---\n\n" +
	"## S01 — Stub Generated Task ⬜ PENDING\n\n" +
	"```yaml\ntype: general\n```\n\n" +
	"### O que fazer\nDo the stub thing\n\n" +
	"### Critérios de sucesso\n- [ ] Stub done\n"

// planstartGrillDone is the minimal Griller answer that ends the interview
// loop, letting `start` reach its final "Ready to execute" block.
const planstartGrillDone = "Looks resolved.\n\n```grill\n{\"type\":\"done\"}\n```\n"

// planstartGrillQuestion is a full question payload (reflection omitted, as the
// model does on the first turn) used to lock how `start` renders a question.
const planstartGrillQuestion = "Here is the ambiguity.\n\n```grill\n" +
	`{"type":"question","text":"Which storage backend?","recommended":"Postgres","rationale":"Already in the stack."}` +
	"\n```\n"

// planstartBrainstormAllInOne drives the whole brainstorm path with ONE static
// stub. It works because each parser looks for its own fenced block and takes
// the LAST match: Brainstormer.Interview reads ```brainstorm``` (done),
// GenerateSpec reads ```spec```, and the grill loop that follows reads
// ```grill``` (done). So a single payload carrying all three walks
// brainstorm → spec.md written → grill → "Ready to execute".
const planstartBrainstormAllInOne = "Enough context.\n\n" +
	"```brainstorm\n{\"type\":\"done\"}\n```\n\n" +
	"```spec\n# delta\n\n## Objective\n\nExport rows as CSV.\n```\n\n" +
	"```grill\n{\"type\":\"done\"}\n```\n"

// planstartGitShortSHA matches the commit id `git worktree add` prints in
// "HEAD is now at <sha> init". scrub()'s hash normaliser leaves an all-digit
// short sha alone (it requires at least one a-f), which would make the golden
// flake roughly 4% of runs — so neutralise it BEFORE scrub sees it.
var planstartGitShortSHA = regexp.MustCompile(`(HEAD is now at )[0-9a-f]{7,40}`)

// planstartScrub is scrub() plus the git-short-sha normaliser.
func planstartScrub(s string) string {
	return scrub(planstartGitShortSHA.ReplaceAllString(s, "${1}<SHA>"))
}

// planstartScrubKeepHash is planstartScrub but keeps hex hashes intact. Used on
// anchor.yaml, where spec_hash is a sha256 of a compile-time constant spec and
// therefore deterministic — locking the literal hash is the whole point of the
// --reanchor golden.
func planstartScrubKeepHash(s string) string {
	return scrubExcept(planstartGitShortSHA.ReplaceAllString(s, "${1}<SHA>"), "hash")
}

// planstartFileGolden renders a file the command wrote as a golden body. The
// trailing-newline note matters: goldenAssert appends a newline to whatever it
// is handed, so a file that ends WITHOUT one (which is exactly what the Planner
// writes, because extractTasksContent TrimSpace's the model output) would
// otherwise be indistinguishable from one that ends with one.
func planstartFileGolden(rel, content string) string {
	return fmt.Sprintf("--- file %s ---\n%s\n--- end of %s (ends with newline: %v) ---",
		rel, content, rel, strings.HasSuffix(content, "\n"))
}

// planstartMissing asserts a path does not exist, so "the command wrote
// nothing" is a checked property rather than an assumption.
func planstartMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected %s to be absent, but it exists", path)
	}
}

// ── plan: main path (writes tasks.md + anchor.yaml) ──────────────────────────

// The primary reason this group exists: plan's real output is a FILE. Project
// has a spec but no tasks.md; the stubbed Planner emits a valid plan.
func TestCharacterizePlanFreshWritesTasksMD(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, "")
	planstartStubClaudeSaying(t, planstartGeneratedTasksMD)

	args := []string{"plan", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	goldenAssert(t, "planstart_plan_fresh", planstartScrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "planstart_plan_fresh_tasks_md",
		planstartScrub(planstartFileGolden("tasks.md", f.Read(".corvex/tasks/alpha/tasks.md"))))
	goldenAssert(t, "planstart_plan_fresh_anchor_yaml",
		planstartScrubKeepHash(planstartFileGolden("anchor.yaml", f.Read(".corvex/tasks/alpha/anchor.yaml"))))
}

// Same success path, but tasks.md already exists with a PASSED task. Locks that
// a successful plan REPLACES the file wholesale — the previous S01 ✅ PASSED is
// gone, because the file is whatever the model returned.
func TestCharacterizePlanOverwritesExistingTasksMD(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	planstartStubClaudeSaying(t, planstartGeneratedTasksMD)

	args := []string{"plan", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	goldenAssert(t, "planstart_plan_overwrite", planstartScrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "planstart_plan_overwrite_tasks_md",
		planstartScrub(planstartFileGolden("tasks.md", f.Read(".corvex/tasks/alpha/tasks.md"))))
}

// The model narrates instead of emitting a file. The Planner retries three
// times and fails — but every attempt has already WRITTEN the prose over
// tasks.md, so a previously valid plan is destroyed by a failed run. Locking
// this is the point: it is current behaviour, not desired behaviour.
func TestCharacterizePlanNarrationClobbersTasksMD(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	planstartStubClaudeSaying(t, "I'll now analyse the spec and write the tasks for you.")

	args := []string{"plan", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	goldenAssert(t, "planstart_plan_narration", planstartScrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "planstart_plan_narration_tasks_md",
		planstartScrub(planstartFileGolden("tasks.md", f.Read(".corvex/tasks/alpha/tasks.md"))))
	// A failed plan must not leave an anchor behind (the drift guard would then
	// believe the spec was planned).
	planstartMissing(t, f.Path(".corvex", "tasks", "alpha", "anchor.yaml"))
}

// ── plan: --reanchor (main path + guards) ────────────────────────────────────

// --reanchor's contract: rewrite anchor.yaml's spec_hash, never invoke the
// Planner, never touch tasks.md. All three are asserted. No claude stub is
// installed on purpose — if this path ever started calling the provider the
// test would spend real money, so its absence is part of the characterization.
func TestCharacterizePlanReanchor(t *testing.T) {
	f := newFixture(t).
		AddProject("alpha", fixtureSpecMD, fixtureTasksMD).
		Write(".corvex/tasks/alpha/anchor.yaml", "project: alpha\nspec_hash: stalehash\nnext_task: S02\n")

	args := []string{"plan", "alpha", "--reanchor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	goldenAssert(t, "planstart_plan_reanchor", planstartScrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "planstart_plan_reanchor_anchor_yaml",
		planstartScrubKeepHash(planstartFileGolden("anchor.yaml", f.Read(".corvex/tasks/alpha/anchor.yaml"))))
	// "tasks.md left untouched" — the log line claims it, the golden proves it.
	goldenAssert(t, "planstart_plan_reanchor_tasks_md",
		planstartScrub(planstartFileGolden("tasks.md", f.Read(".corvex/tasks/alpha/tasks.md"))))
}

// --reanchor with no anchor.yaml at all: anchor.Load returns a zero state and
// the file is created from scratch (note completed: [] appears).
func TestCharacterizePlanReanchorNoExistingAnchor(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"plan", "alpha", "--reanchor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	goldenAssert(t, "planstart_plan_reanchor_fresh", planstartScrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "planstart_plan_reanchor_fresh_anchor_yaml",
		planstartScrubKeepHash(planstartFileGolden("anchor.yaml", f.Read(".corvex/tasks/alpha/anchor.yaml"))))
}

func TestCharacterizePlanReanchorNoSpec(t *testing.T) {
	f := newFixture(t).AddProject("alpha", "", fixtureTasksMD)

	args := []string{"plan", "alpha", "--reanchor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "planstart_plan_reanchor_no_spec", planstartScrub(transcript(args, stdout, stderr, err)))
	planstartMissing(t, f.Path(".corvex", "tasks", "alpha", "anchor.yaml"))
}

func TestCharacterizePlanReanchorNoTasks(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, "")

	args := []string{"plan", "alpha", "--reanchor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "planstart_plan_reanchor_no_tasks", planstartScrub(transcript(args, stdout, stderr, err)))
	planstartMissing(t, f.Path(".corvex", "tasks", "alpha", "anchor.yaml"))
}

// A tasks.md with a heading in the right shape but an invalid status word: the
// parser rejects it and --reanchor refuses rather than anchoring a broken plan.
// The golden carries the parser's full multi-line hint.
func TestCharacterizePlanReanchorBrokenTasks(t *testing.T) {
	f := newFixture(t).
		AddProject("alpha", fixtureSpecMD, "## S01 — Broken ✅ FROBNICATED\n")

	args := []string{"plan", "alpha", "--reanchor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "planstart_plan_reanchor_broken_tasks", planstartScrub(transcript(args, stdout, stderr, err)))
	planstartMissing(t, f.Path(".corvex", "tasks", "alpha", "anchor.yaml"))
}

// ── plan: borders ───────────────────────────────────────────────────────────

// The provider fails outright. Locks the full error chain
// (plan → planner execution → claude cli exit status + stderr passthrough).
func TestCharacterizePlanProviderFailure(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, "")
	stubClaude(t, `echo "stub claude: refusing" >&2; exit 3`)

	args := []string{"plan", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "planstart_plan_provider_failure", planstartScrub(transcript(args, stdout, stderr, err)))
	// The provider never produced output, so nothing was written.
	planstartMissing(t, f.Path(".corvex", "tasks", "alpha", "tasks.md"))
}

// No spec.md and no --reanchor: the Planner reads the spec first, so this fails
// before any provider call. The stub is installed anyway — if the order ever
// flips, the test must not start paying for tokens.
func TestCharacterizePlanMissingSpec(t *testing.T) {
	f := newFixture(t).AddProject("alpha", "", "")
	planstartStubClaudeSaying(t, planstartGeneratedTasksMD)

	args := []string{"plan", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "planstart_plan_missing_spec", planstartScrub(transcript(args, stdout, stderr, err)))
}

// Unknown provider in config.yaml: fails before the planner is constructed.
func TestCharacterizePlanUnknownProvider(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, "")
	f.Write(".corvex/config.yaml", strings.Replace(fixtureConfigYAML,
		"default: claude-cli", "default: gpt-9000", 1))

	args := []string{"plan", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "planstart_plan_unknown_provider", planstartScrub(transcript(args, stdout, stderr, err)))
}

// Run from a directory with no .corvex at all.
func TestCharacterizePlanNoCorvexDir(t *testing.T) {
	args := []string{"plan", "alpha"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "planstart_plan_no_corvex", planstartScrub(transcript(args, stdout, stderr, err)))
}

// Run from the main repo while a worktree for the project exists: plan must
// refuse, because the generated tasks.md would land in the wrong .corvex.
func TestCharacterizePlanWorktreeMismatch(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, "").GitInit()
	if err := os.MkdirAll(f.Dir+"-alpha", 0o755); err != nil {
		t.Fatalf("creating sibling worktree dir: %v", err)
	}

	args := []string{"plan", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "planstart_plan_worktree_mismatch", planstartScrub(transcript(args, stdout, stderr, err)))
	planstartMissing(t, f.Path(".corvex", "tasks", "alpha", "tasks.md"))
}

// --here overrides the worktree guard, so planning proceeds in the main repo.
func TestCharacterizePlanWorktreeMismatchHere(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, "").GitInit()
	if err := os.MkdirAll(f.Dir+"-alpha", 0o755); err != nil {
		t.Fatalf("creating sibling worktree dir: %v", err)
	}
	planstartStubClaudeSaying(t, planstartGeneratedTasksMD)

	args := []string{"plan", "alpha", "--here"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "planstart_plan_here", planstartScrub(transcript(args, stdout, stderr, err)))
	goldenAssert(t, "planstart_plan_here_tasks_md",
		planstartScrub(planstartFileGolden("tasks.md", f.Read(".corvex/tasks/alpha/tasks.md"))))
}

func TestCharacterizePlanNoArgs(t *testing.T) {
	args := []string{"plan"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "planstart_plan_no_args", planstartScrub(transcript(args, stdout, stderr, err)))
}

// Locks the flag surface (--here, --reanchor and their help text).
func TestCharacterizePlanHelp(t *testing.T) {
	args := []string{"plan", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "planstart_plan_help", planstartScrub(transcript(args, stdout, stderr, err)))
}

// ── start: main path (creates a real worktree) ───────────────────────────────

// The full happy path: no worktree yet → git worktree add, .corvex symlink,
// mode prompt (3 = plan, offered because spec.md exists), grill loop ends
// immediately, final "Ready to execute" block. err is nil.
//
// Base branch is answered "HEAD" rather than accepting the "main" default:
// gitInit runs with GIT_CONFIG_GLOBAL=/dev/null, so the initial branch name is
// whatever git compiles in (master on most builds) and "main" would fail.
func TestCharacterizeStartNewWorktreePlanMode(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	planstartStubClaudeSaying(t, planstartGrillDone)

	args := []string{"start", "alpha"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "HEAD\n3\n", args...)

	// scrubExcept(..., "cost") keeps "$0.42 spent" visible: the number comes
	// from the stub's result line, not from a clock.
	body := planstartGitShortSHA.ReplaceAllString(transcript(args, stdout, stderr, err), "${1}<SHA>")
	goldenAssert(t, "planstart_start_new_worktree_plan", scrubExcept(body, "cost"))

	// The worktree really exists and shares .corvex with the main repo.
	wt := f.Dir + "-alpha"
	if _, statErr := os.Stat(filepath.Join(wt, ".corvex", "tasks", "alpha", "spec.md")); statErr != nil {
		t.Fatalf("worktree .corvex not usable: %v", statErr)
	}
	info, lstatErr := os.Lstat(filepath.Join(wt, ".corvex"))
	if lstatErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected .corvex in the worktree to be a symlink (err=%v)", lstatErr)
	}
}

// An existing worktree short-circuits creation: "Using existing worktree",
// no base-branch prompt, then the same grill/plan flow. Also the cheapest way
// to exercise start's tail without depending on git's output at all.
func TestCharacterizeStartExistingWorktreePlanMode(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	planstartStartExistingWorktree(t, f, "alpha")
	planstartStubClaudeSaying(t, planstartGrillDone)

	args := []string{"start", "alpha"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "3\n", args...)
	goldenAssert(t, "planstart_start_existing_worktree",
		scrubExcept(planstartGitShortSHA.ReplaceAllString(transcript(args, stdout, stderr, err), "${1}<SHA>"), "cost"))
}

// planstartStartExistingWorktree pre-creates the sibling directory `start`
// treats as an existing worktree, with its own .corvex (a copy, not a symlink,
// so the golden never depends on symlink resolution) holding one project.
func planstartStartExistingWorktree(t *testing.T, f *fixture, project string) string {
	t.Helper()
	wt := f.Dir + "-" + project
	dir := filepath.Join(wt, ".corvex", "tasks", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating existing worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".corvex", "config.yaml"), []byte(fixtureConfigYAML), 0o644); err != nil {
		t.Fatalf("writing worktree config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.md"), []byte(fixtureSpecMD), 0o644); err != nil {
		t.Fatalf("writing worktree spec: %v", err)
	}
	return wt
}

// Mode 2 (grill) writes a minimal spec.md from the typed description before
// grilling. The written file is goldened — it is the only thing this path
// persists.
func TestCharacterizeStartGrillModeWritesSpec(t *testing.T) {
	f := newFixture(t).GitInit()
	wt := planstartStartExistingWorktreeBare(t, f, "gamma")
	planstartStubClaudeSaying(t, planstartGrillDone)

	args := []string{"start", "gamma"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "2\nAdd a CSV export button\n\n", args...)

	goldenAssert(t, "planstart_start_grill_mode",
		scrubExcept(planstartGitShortSHA.ReplaceAllString(transcript(args, stdout, stderr, err), "${1}<SHA>"), "cost"))

	written, readErr := os.ReadFile(filepath.Join(wt, ".corvex", "tasks", "gamma", "spec.md"))
	if readErr != nil {
		t.Fatalf("spec.md was not written: %v", readErr)
	}
	goldenAssert(t, "planstart_start_grill_mode_spec_md",
		planstartScrub(planstartFileGolden("spec.md", string(written))))
}

// planstartStartExistingWorktreeBare is planstartStartExistingWorktree without
// a project spec — so promptMode must NOT offer option 3.
func planstartStartExistingWorktreeBare(t *testing.T, f *fixture, project string) string {
	t.Helper()
	wt := f.Dir + "-" + project
	if err := os.MkdirAll(filepath.Join(wt, ".corvex", "tasks"), 0o755); err != nil {
		t.Fatalf("creating existing worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".corvex", "config.yaml"), []byte(fixtureConfigYAML), 0o644); err != nil {
		t.Fatalf("writing worktree config: %v", err)
	}
	return wt
}

// Mode 1 (brainstorm) end to end: the interview declares itself done on the
// first turn, GenerateSpec writes spec.md from the ```spec``` block, and the
// grill loop closes the flow. Locks both the transcript and the generated
// spec.md — the brainstorm path's only durable output.
func TestCharacterizeStartBrainstormModeWritesSpec(t *testing.T) {
	f := newFixture(t).GitInit()
	wt := planstartStartExistingWorktreeBare(t, f, "delta")
	planstartStubClaudeSaying(t, planstartBrainstormAllInOne)

	args := []string{"start", "delta"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "1\nA CSV export\n\n", args...)

	goldenAssert(t, "planstart_start_brainstorm_mode",
		scrubExcept(planstartGitShortSHA.ReplaceAllString(transcript(args, stdout, stderr, err), "${1}<SHA>"), "cost"))

	written, readErr := os.ReadFile(filepath.Join(wt, ".corvex", "tasks", "delta", "spec.md"))
	if readErr != nil {
		t.Fatalf("spec.md was not written: %v", readErr)
	}
	goldenAssert(t, "planstart_start_brainstorm_mode_spec_md",
		planstartScrub(planstartFileGolden("spec.md", string(written))))
	// brainstorm-qa.md is only written when a question is actually answered;
	// a first-turn "done" must leave none behind.
	planstartMissing(t, filepath.Join(wt, ".corvex", "tasks", "delta", "brainstorm-qa.md"))
}

// planstartBrainstormQuestion is planstartBrainstormAllInOne with a QUESTION
// instead of a done, so the interview actually loops and every branch of
// readBrainstormAnswer can be driven from stdin. The ```spec``` and ```grill```
// blocks ride along so /done can still finish the flow (see the all-in-one
// comment for why one static payload can serve three parsers).
const planstartBrainstormQuestion = "One thing is unclear.\n\n" +
	"```brainstorm\n" +
	`{"type":"question","text":"Which delimiter?","recommended":"comma","rationale":"Matches the format name."}` +
	"\n```\n\n" +
	"```spec\n# epsilon\n\n## Objective\n\nExport rows as CSV.\n```\n\n" +
	"```grill\n{\"type\":\"done\"}\n```\n"

// Drives every branch of readBrainstormAnswer in one invocation, in this order:
// /summary (empty ledger), /skip, blank line (accepts the recommendation),
// bare /ask (usage error), /ask <question> (AskFollowup reply), /done.
// Also goldens brainstorm-qa.md, the file the loop appends to.
//
// Note the /ask reply is the stub's ENTIRE output, fenced blocks included —
// AskFollowup returns result.Output verbatim, and that is what gets printed.
func TestCharacterizeStartBrainstormAnswerLoop(t *testing.T) {
	f := newFixture(t).GitInit()
	wt := planstartStartExistingWorktreeBare(t, f, "epsilon")
	planstartStubClaudeSaying(t, planstartBrainstormQuestion)

	stdin := "1\n" + "A CSV export\n" + "\n" + // mode 1, description, submit
		"/summary\n" + "/skip\n" + // iteration 1
		"\n" + // iteration 2: accept "comma"
		"/ask\n" + "/ask Does it need a BOM?\n" + "/done\n" // iteration 3

	args := []string{"start", "epsilon"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, stdin, args...)

	goldenAssert(t, "planstart_start_brainstorm_answers",
		scrubExcept(planstartGitShortSHA.ReplaceAllString(transcript(args, stdout, stderr, err), "${1}<SHA>"), "cost"))

	qa, readErr := os.ReadFile(filepath.Join(wt, ".corvex", "tasks", "epsilon", "brainstorm-qa.md"))
	if readErr != nil {
		t.Fatalf("brainstorm-qa.md was not written: %v", readErr)
	}
	goldenAssert(t, "planstart_start_brainstorm_answers_qa_md",
		planstartScrub(planstartFileGolden("brainstorm-qa.md", string(qa))))
}

// No spec.md → the menu hides option 3, and typing "3" anyway silently falls
// back to brainstorm (which then rejects the empty description at EOF).
func TestCharacterizeStartNoSpecHidesPlanOption(t *testing.T) {
	f := newFixture(t).GitInit()
	planstartStartExistingWorktreeBare(t, f, "gamma")
	planstartStubClaudeSaying(t, planstartGrillDone)

	args := []string{"start", "gamma"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "3\n", args...)
	goldenAssert(t, "planstart_start_no_spec_menu", planstartScrub(transcript(args, stdout, stderr, err)))
}

// Default choice (empty line) is brainstorm; an empty description aborts.
func TestCharacterizeStartBrainstormEmptyDescription(t *testing.T) {
	f := newFixture(t).GitInit()
	planstartStartExistingWorktreeBare(t, f, "gamma")
	planstartStubClaudeSaying(t, planstartGrillDone)

	args := []string{"start", "gamma"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "\n", args...)
	goldenAssert(t, "planstart_start_brainstorm_empty", planstartScrub(transcript(args, stdout, stderr, err)))
}

// The Griller asks a question: locks the 🔍/💡/why rendering and the answer
// prompt, then the EOF error when stdin runs out mid-interview.
func TestCharacterizeStartGrillQuestionThenEOF(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	planstartStartExistingWorktree(t, f, "alpha")
	planstartStubClaudeSaying(t, planstartGrillQuestion)

	args := []string{"start", "alpha"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "3\n", args...)
	goldenAssert(t, "planstart_start_grill_question",
		scrubExcept(planstartGitShortSHA.ReplaceAllString(transcript(args, stdout, stderr, err), "${1}<SHA>"), "cost"))
}

// The provider fails during the grill step reached from `start`.
func TestCharacterizeStartGrillProviderFailure(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	planstartStartExistingWorktree(t, f, "alpha")
	stubClaude(t, `echo "stub claude: refusing" >&2; exit 3`)

	args := []string{"start", "alpha"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "3\n", args...)
	goldenAssert(t, "planstart_start_grill_failure", planstartScrub(transcript(args, stdout, stderr, err)))
}

// An existing worktree directory that has no .corvex: start still announces it,
// then refuses with the worktree-specific symlink hint.
func TestCharacterizeStartExistingWorktreeNoCorvex(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD).GitInit()
	if err := os.MkdirAll(f.Dir+"-alpha", 0o755); err != nil {
		t.Fatalf("creating sibling worktree dir: %v", err)
	}

	args := []string{"start", "alpha"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "3\n", args...)
	goldenAssert(t, "planstart_start_no_corvex_in_worktree", planstartScrub(transcript(args, stdout, stderr, err)))
}

// Outside any git repository start cannot pick a worktree path at all.
func TestCharacterizeStartNotAGitRepo(t *testing.T) {
	args := []string{"start", "alpha"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "planstart_start_no_git", planstartScrub(transcript(args, stdout, stderr, err)))
}

// A repo with zero commits: setupWorktree detects it up front and prints the
// recovery command instead of letting `git worktree add` fail cryptically.
func TestCharacterizeStartRepoWithoutCommits(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	planstartGitInitNoCommit(t, f.Dir)

	args := []string{"start", "alpha"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "HEAD\n", args...)
	goldenAssert(t, "planstart_start_no_commits", planstartScrub(transcript(args, stdout, stderr, err)))
}

// planstartGitInitNoCommit inits a repo and stops — no commit, so HEAD is
// unborn. Isolates git config exactly like gitInit in start_test.go.
func planstartGitInitNoCommit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t.com"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
}

func TestCharacterizeStartNoArgs(t *testing.T) {
	args := []string{"start"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "planstart_start_no_args", planstartScrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeStartHelp(t *testing.T) {
	args := []string{"start", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "planstart_start_help", planstartScrub(transcript(args, stdout, stderr, err)))
}

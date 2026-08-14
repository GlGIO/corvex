package cmd

// Characterization of `corvex doctor` and `corvex inspect`.
//
// Both commands read the machine (doctor: PATH, docker, repo skills; inspect:
// the activity ledger and the wall clock), so every golden here pins the host
// out of the picture first: CORVEX_CLAUDE_BIN points at a stub, PATH is emptied
// when the interesting case is "binary absent", and the HH:MM:SS clock that
// `inspect --task` prints through .Local() is normalised, because it would
// otherwise flip with the runner's timezone.
//
// LEI 1 applies: nothing below fixes anything. Where the current output is
// wrong (byte-truncated accented titles, glyph columns that don't line up) the
// golden records the wrong bytes on purpose.
//
// Rewrite every golden in this file with:
//
//	go test ./cmd/ -run TestCharacterizeDoctorinspect -update-golden

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/giovannialves/corvex/internal/activity"
)

// ── local normalisation ──────────────────────────────────────────────────────

// doctorinspectReClock matches the bare HH:MM:SS that printTaskDetail emits via
// e.Timestamp.Local(). The harness's "ts" scrubber only knows timestamps that
// carry a date, so this one is genuinely host-dependent (TZ) and needs its own
// token. time.Local is resolved once per process, so t.Setenv("TZ") is not a
// reliable alternative.
var doctorinspectReClock = regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}\b`)

// doctorinspectScrub is scrub() plus the clock token — the default for doctor
// goldens, where no cost or duration is printed at all.
func doctorinspectScrub(s string) string {
	return doctorinspectReClock.ReplaceAllString(scrub(s), "<CLOCK>")
}

// doctorinspectScrubKeepMetrics keeps the cost and duration values visible:
// inspect derives both from the fixture's activity.jsonl, so "$0.15" and "8s"
// are deterministic and worth locking byte-for-byte. Everything else (temp
// paths, RFC3339 ledger timestamps, the local clock) is still tokenised.
func doctorinspectScrubKeepMetrics(s string) string {
	return doctorinspectReClock.ReplaceAllString(scrubExcept(s, "cost", "dur"), "<CLOCK>")
}

// ── fixtures ─────────────────────────────────────────────────────────────────

// doctorinspectFixture is newFixture with config.yaml overwritten. Passing ""
// keeps the canonical fixtureConfigYAML.
func doctorinspectFixture(t *testing.T, configYAML string) *fixture {
	t.Helper()
	f := newFixture(t)
	if configYAML != "" {
		f.Write(filepath.Join(".corvex", "config.yaml"), configYAML)
	}
	return f
}

// doctorinspectStubClaude installs a claude stub that must never be executed by
// doctor — doctor only LookPath's the binary. The body exits non-zero so a
// future change that actually invokes it shows up as an obvious golden diff
// rather than a silent (and possibly billable) success.
func doctorinspectStubClaude(t *testing.T) {
	t.Helper()
	stubClaude(t, `echo "doctorinspect stub: should never be executed" >&2; exit 9`)
}

// doctorinspectEmptyPATH replaces PATH with an empty directory so exec.LookPath
// fails for every bare command name. CORVEX_CLAUDE_BIN survives this because it
// holds an absolute path, which LookPath resolves without consulting PATH —
// call this AFTER doctorinspectStubClaude.
func doctorinspectEmptyPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// ── doctor: the environment is healthy ───────────────────────────────────────

// The main path: 7 checks in a fixed order (provider, models, sandbox,
// escalation, cost, mcp-secrets, skills), all passing, exit 0.
func TestCharacterizeDoctorinspectDoctorHumanAllPass(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, "")

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_human_pass", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeDoctorinspectDoctorJSONAllPass(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, "")

	args := []string{"doctor", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_json_pass", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// doctor deliberately does NOT call requireCorvexDir: with no .corvex at all it
// falls back to config.Default() and still reports 7 passing checks.
func TestCharacterizeDoctorinspectDoctorNoCorvexDir(t *testing.T) {
	doctorinspectStubClaude(t)

	args := []string{"doctor"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "doctorinspect_doctor_no_corvex_dir", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// Repo skills are discovered under .corvex/skills/ first, then .claude/skills/,
// each requiring a SKILL.md. The printed order follows that two-directory walk,
// not an alphabetical sort of the union.
func TestCharacterizeDoctorinspectDoctorSkillsPresent(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, "").
		Write(filepath.Join(".corvex", "skills", "zeta", "SKILL.md"), "# zeta\n").
		Write(filepath.Join(".claude", "skills", "alpha-skill", "SKILL.md"), "# alpha-skill\n").
		// No SKILL.md → not counted.
		Mkdir(filepath.Join(".corvex", "skills", "empty-dir"))

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_skills_present", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// ── doctor: failing checks (exit non-zero) ───────────────────────────────────

const doctorinspectCfgBadProvider = `provider:
  default: openai
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`

func TestCharacterizeDoctorinspectDoctorFailUnknownProvider(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgBadProvider)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_fail_unknown_provider", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// Same failure through --json: the JSON body is still printed in full and the
// error is returned afterwards, so a caller parsing stdout on a non-zero exit
// still gets a valid document.
func TestCharacterizeDoctorinspectDoctorJSONFail(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgBadProvider)

	args := []string{"doctor", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_json_fail", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// CORVEX_CLAUDE_BIN names a binary that is not on the (emptied) PATH. The
// message quotes the name it looked for, which is what makes this check
// actionable — lock the exact wording.
func TestCharacterizeDoctorinspectDoctorFailBinaryMissing(t *testing.T) {
	f := doctorinspectFixture(t, "")
	doctorinspectEmptyPATH(t)
	t.Setenv("CORVEX_CLAUDE_BIN", "corvex-absent-claude")

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_fail_binary_missing", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// With CORVEX_CLAUDE_BIN unset the check falls back to the literal name
// "claude". Emptying PATH is what makes this deterministic on a developer
// machine that has the real CLI installed.
func TestCharacterizeDoctorinspectDoctorFailDefaultBinaryMissing(t *testing.T) {
	f := doctorinspectFixture(t, "")
	doctorinspectEmptyPATH(t)
	t.Setenv("CORVEX_CLAUDE_BIN", "")

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_fail_default_binary_missing", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

const doctorinspectCfgBadSandboxType = `provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: lxc
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`

func TestCharacterizeDoctorinspectDoctorFailSandboxType(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgBadSandboxType)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_fail_sandbox_type", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// A sandbox.profile short-circuits the type check entirely — an unknown profile
// is reported instead, and the message never mentions the type.
const doctorinspectCfgBadSandboxProfile = `provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  profile: podman
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`

func TestCharacterizeDoctorinspectDoctorFailSandboxProfile(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgBadSandboxProfile)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_fail_sandbox_profile", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// Exactly one escalation policy: checkEscalation iterates a map and returns on
// the first bad entry, so more than one invalid policy would make the golden
// depend on Go's map ordering.
const doctorinspectCfgEscalationNoTo = `provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
review:
  escalation:
    second_retry:
      action: upgrade-model
      after: 2
`

func TestCharacterizeDoctorinspectDoctorFailEscalationMissingTo(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgEscalationNoTo)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_fail_escalation_missing_to", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

const doctorinspectCfgEscalationUnknownAction = `provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
review:
  escalation:
    second_retry:
      action: restart-task
      after: 1
`

func TestCharacterizeDoctorinspectDoctorFailEscalationUnknownAction(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgEscalationUnknownAction)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_fail_escalation_unknown_action", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// One valid policy: the pass message reports the count, phrased "1 policies".
const doctorinspectCfgEscalationValid = `provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
  mount: relative/host/path:/workspace
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
review:
  escalation:
    second_retry:
      action: upgrade-model
      after: 2
      to: opus
`

// Also covers sandbox.mount with a relative host path: the check resolves it
// with filepath.Abs and still reports only "type=local", never the mount.
func TestCharacterizeDoctorinspectDoctorEscalationValidAndMount(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgEscalationValid)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_escalation_valid_mount", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// ── doctor: warnings (exit 0) ────────────────────────────────────────────────

const doctorinspectCfgDocker = `provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: docker
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`

// sandbox.type=docker with no docker binary is a warning, not a failure, so the
// overall exit stays 0.
func TestCharacterizeDoctorinspectDoctorWarnDockerMissing(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgDocker)
	doctorinspectEmptyPATH(t)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_warn_docker_missing", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// With a docker stub on PATH the same config passes and prints "type=docker".
func TestCharacterizeDoctorinspectDoctorDockerPresent(t *testing.T) {
	doctorinspectStubClaude(t)
	stubBin(t, "docker", `echo "docker stub"; exit 0`)
	f := doctorinspectFixture(t, doctorinspectCfgDocker)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_docker_present", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// Both ceilings at zero collapse into one warn line joining the two reasons
// with "; ".
const doctorinspectCfgNoCostCeilings = `provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 0
  max_cost_per_task_usd: 0
`

func TestCharacterizeDoctorinspectDoctorWarnNoCostCeilings(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgNoCostCeilings)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_warn_no_cost_ceilings", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// The same warning through --json. This golden exists because mutation-testing
// the pass/fail JSON pair showed nothing failed when statusString()'s "warn"
// branch was renamed: no golden observed a warn status in JSON. Warnings also
// keep the exit code at 0, which this pins too.
func TestCharacterizeDoctorinspectDoctorJSONWarn(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgNoCostCeilings)

	args := []string{"doctor", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_json_warn", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

const doctorinspectCfgMCP = `provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
  mcp_servers:
    - name: sentry
      command: npx
      args: ["-y", "@sentry/mcp"]
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`

func TestCharacterizeDoctorinspectDoctorWarnMCPNotGitignored(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgMCP)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_warn_mcp_not_gitignored", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// .corvex/.gitignore is consulted before the repo-root one; a plain substring
// match on "mcp.json" is enough to satisfy the check.
func TestCharacterizeDoctorinspectDoctorMCPGitignored(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgMCP).
		Write(filepath.Join(".corvex", ".gitignore"), "mcp.json\n")

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_mcp_gitignored", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// One skill_routing entry pointing at a skill that has no SKILL.md on disk.
// Exactly one entry, again because the missing list is built from a map.
const doctorinspectCfgSkillRouting = `provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_cost_usd: 25
  max_cost_per_task_usd: 5
skill_routing:
  frontend: pixel-perfect
`

func TestCharacterizeDoctorinspectDoctorWarnSkillRoutingMissing(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgSkillRouting)

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_warn_skill_routing_missing", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// The routed skill exists → pass, and the message lists it rather than the
// routing entry.
func TestCharacterizeDoctorinspectDoctorSkillRoutingSatisfied(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, doctorinspectCfgSkillRouting).
		Write(filepath.Join(".corvex", "skills", "pixel-perfect", "SKILL.md"), "# pixel-perfect\n")

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_skill_routing_satisfied", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// ── doctor: argument and config errors ───────────────────────────────────────

// cobra.NoArgs. SilenceUsage/SilenceErrors are set on rootCmd, so nothing is
// printed at all — the error alone is the output.
func TestCharacterizeDoctorinspectDoctorRejectsArgs(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, "")

	args := []string{"doctor", "extra-arg"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_rejects_args", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// A config.yaml that isn't valid YAML fails in loadConfig, before any check
// runs, so no check line is printed.
func TestCharacterizeDoctorinspectDoctorBrokenConfig(t *testing.T) {
	doctorinspectStubClaude(t)
	f := doctorinspectFixture(t, "provider:\n  default: [unclosed\n")

	args := []string{"doctor"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_doctor_broken_config", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeDoctorinspectDoctorHelp(t *testing.T) {
	args := []string{"doctor", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "doctorinspect_doctor_help", doctorinspectScrub(transcript(args, stdout, stderr, err)))
}

// ── inspect: fixtures ────────────────────────────────────────────────────────

// doctorinspectInspectFixture is the canonical inspect scenario: the two-task
// DAG from the harness plus a ledger where S01 retried once and then completed
// with real metrics. S02 has no ledger activity at all, which is what produces
// the "—" placeholders.
func doctorinspectInspectFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixture(t).
		AddProject("alpha", fixtureSpecMD, fixtureTasksMD).
		AddLedger("alpha",
			activity.Entry{Type: "task_start", TaskID: "S01", Phase: "worker", Attempt: 1},
			activity.Entry{Type: "retry", TaskID: "S01", Message: "review requested changes"},
			activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED",
				DurationMs: 8000, CostUSD: 0.15, TokensIn: 600, TokensOut: 300},
		)
}

// ── inspect: main path ───────────────────────────────────────────────────────

func TestCharacterizeDoctorinspectInspectHuman(t *testing.T) {
	f := doctorinspectInspectFixture(t)

	args := []string{"inspect", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_human", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeDoctorinspectInspectJSON(t *testing.T) {
	f := doctorinspectInspectFixture(t)

	args := []string{"inspect", "alpha", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_json", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// anchor.yaml supplies the optional "Intent:" line in the human summary and the
// "intent" field in JSON. Without it the line is omitted entirely (see the
// goldens above).
func TestCharacterizeDoctorinspectInspectHumanWithIntent(t *testing.T) {
	f := doctorinspectInspectFixture(t).
		Write(filepath.Join(".corvex", "tasks", "alpha", "anchor.yaml"),
			"project: alpha\nintent: characterize the inspect renderer\nnext_task: S02\n")

	args := []string{"inspect", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_human_intent", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// --task prints the raw event stream for one task, in ledger order, with the
// per-event "[STATUS]" / message / duration / cost suffixes. Note that Status
// and Message are mutually exclusive in the switch: an entry with both shows
// only the status.
func TestCharacterizeDoctorinspectInspectTaskDetail(t *testing.T) {
	f := doctorinspectInspectFixture(t)

	args := []string{"inspect", "alpha", "--task", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_task_detail", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// --task --json bypasses the aggregation entirely and dumps the matching raw
// ledger entries, including fields the human view never shows (phase, attempt,
// tokens).
func TestCharacterizeDoctorinspectInspectTaskJSON(t *testing.T) {
	f := doctorinspectInspectFixture(t)

	args := []string{"inspect", "alpha", "--task", "S01", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_task_json", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// An unknown task ID is not an error: the command says so and exits 0.
func TestCharacterizeDoctorinspectInspectTaskUnknown(t *testing.T) {
	f := doctorinspectInspectFixture(t)

	args := []string{"inspect", "alpha", "--task", "S99"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_task_unknown", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// --task --json with no match emits an empty JSON array, not null: runInspect
// pre-allocates the slice with make(). Worth locking — a `var filtered []Entry`
// refactor would print "null" and break every consumer.
func TestCharacterizeDoctorinspectInspectTaskUnknownJSON(t *testing.T) {
	f := doctorinspectInspectFixture(t)

	args := []string{"inspect", "alpha", "--task", "S99", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_task_unknown_json", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// ── inspect: renderer edge cases ─────────────────────────────────────────────

// doctorinspectStatusesTasksMD exercises every glyph glyphFor knows plus the
// three humanDuration branches. S05's title is deliberately 68 bytes with
// accents so the byte-based truncation in printSummary is captured.
const doctorinspectStatusesTasksMD = "---\ngenerated_by: characterize\ndag:\n  S01: []\n  S02: [S01]\n  S03: [S01]\n  S04: [S01]\n  S05: [S01]\n---\n\n" +
	"## S01 — Passed task ✅ PASSED\n\n```yaml\ntype: general\n```\n\n" +
	"### O que fazer\nDone\n\n### Critérios de sucesso\n- [ ] Done\n\n---\n\n" +
	"## S02 — Running task 🔄 RUNNING\n\n```yaml\ntype: general\ndepends_on: [S01]\n```\n\n" +
	"### O que fazer\nBusy\n\n### Critérios de sucesso\n- [ ] Done\n\n---\n\n" +
	"## S03 — Failed task ❌ FAILED\n\n```yaml\ntype: general\ndepends_on: [S01]\n```\n\n" +
	"### O que fazer\nBroken\n\n### Critérios de sucesso\n- [ ] Done\n\n---\n\n" +
	"## S04 — Skipped task ⏭️ SKIPPED\n\n```yaml\ntype: general\ndepends_on: [S01]\n```\n\n" +
	"### O que fazer\nSkipped\n\n### Critérios de sucesso\n- [ ] Done\n\n---\n\n" +
	"## S05 — Validar o módulo de autenticação com verificação de permissões ⬜ PENDING\n\n" +
	"```yaml\ntype: general\ndepends_on: [S01]\n```\n\n" +
	"### O que fazer\nPending\n\n### Critérios de sucesso\n- [ ] Done\n"

// doctorinspectStatusesFixture also appends a ledger entry for "S99", a task
// that is not in tasks.md: buildInspectData drops it silently, so its cost is
// missing from the total. That is current behaviour, recorded on purpose.
func doctorinspectStatusesFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixture(t).
		AddProject("wide", fixtureSpecMD, doctorinspectStatusesTasksMD).
		AddLedger("wide",
			// sub-second → "Nms"
			activity.Entry{Type: "task_complete", TaskID: "S01", Status: "PASSED",
				DurationMs: 750, CostUSD: 0.01, TokensIn: 10, TokensOut: 5},
			// over a minute → "NmSSs"
			activity.Entry{Type: "task_complete", TaskID: "S02", Status: "RUNNING",
				DurationMs: 95000, CostUSD: 1.5, TokensIn: 9000, TokensOut: 4000},
			// seconds → "Ns", plus two retries
			activity.Entry{Type: "retry", TaskID: "S03"},
			activity.Entry{Type: "retry", TaskID: "S03"},
			activity.Entry{Type: "task_complete", TaskID: "S03", Status: "FAILED",
				DurationMs: 45000, CostUSD: 0.4, TokensIn: 2000, TokensOut: 800},
			// ledger entry for a task absent from tasks.md → ignored entirely
			activity.Entry{Type: "task_complete", TaskID: "S99", Status: "PASSED",
				DurationMs: 999000, CostUSD: 99.99},
		)
}

// Locks the glyph column misalignment (%-8s counts runes, the emoji is two
// terminal cells wide) and the mojibake produced by the byte-slice title
// truncation. Both are bugs; both are what the CLI prints today.
func TestCharacterizeDoctorinspectInspectHumanAllStatuses(t *testing.T) {
	f := doctorinspectStatusesFixture(t)

	args := []string{"inspect", "wide"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_human_all_statuses", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// Same fixture through --json: the title is NOT truncated here, so the golden
// pair proves the 50-byte cut is a rendering concern only.
func TestCharacterizeDoctorinspectInspectJSONAllStatuses(t *testing.T) {
	f := doctorinspectStatusesFixture(t)

	args := []string{"inspect", "wide", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_json_all_statuses", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// Every task has zero duration and zero cost → the Slowest/Expensive block is
// suppressed, but the blank line that precedes it is still printed.
func TestCharacterizeDoctorinspectInspectNoMetrics(t *testing.T) {
	f := newFixture(t).
		AddProject("alpha", fixtureSpecMD, fixtureTasksMD).
		AddLedger("alpha",
			activity.Entry{Type: "retry", TaskID: "S01"},
			activity.Entry{Type: "retry", TaskID: "S02"},
		)

	args := []string{"inspect", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_no_metrics", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// Malformed JSONL lines are skipped rather than fatal, so a ledger truncated by
// a crashed run still renders.
func TestCharacterizeDoctorinspectInspectMalformedLedgerLine(t *testing.T) {
	f := doctorinspectInspectFixture(t)
	f.Write(filepath.Join(".corvex", "tasks", "alpha", "activity.jsonl"),
		f.Read(filepath.Join(".corvex", "tasks", "alpha", "activity.jsonl"))+
			"{\"type\":\"task_comp\n"+
			"not json at all\n")

	args := []string{"inspect", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_malformed_ledger", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// ── inspect: error and empty paths ───────────────────────────────────────────

// No ledger file at all: activity.Read maps os.IsNotExist to (nil, nil), so the
// command prints a hint and exits 0 without ever parsing tasks.md.
func TestCharacterizeDoctorinspectInspectNoActivity(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"inspect", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_no_activity", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// Same for a project that does not exist — the message names the project the
// user typed, with no "did you mean" suggestion even though suggestProject
// exists in helpers.go.
func TestCharacterizeDoctorinspectInspectUnknownProject(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"inspect", "alphaa"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_unknown_project", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// --json takes the same early return, printing the human sentence on stdout
// before any JSON could be emitted. A --json consumer therefore gets a
// non-JSON body with exit 0.
func TestCharacterizeDoctorinspectInspectNoActivityJSON(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"inspect", "alpha", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_no_activity_json", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// A ledger with entries but no tasks.md next to it: the failure surfaces as
// "reading tasks: parsing tasks <path>: ...".
func TestCharacterizeDoctorinspectInspectMissingTasksFile(t *testing.T) {
	f := newFixture(t).
		AddProject("gamma", fixtureSpecMD, "").
		AddLedger("gamma", activity.Entry{Type: "retry", TaskID: "S01"})

	args := []string{"inspect", "gamma"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_missing_tasks_file", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// The --json path reaches buildInspectData too, so the same tasks.md failure
// surfaces identically there — no partial JSON is emitted first.
func TestCharacterizeDoctorinspectInspectMissingTasksFileJSON(t *testing.T) {
	f := newFixture(t).
		AddProject("gamma", fixtureSpecMD, "").
		AddLedger("gamma", activity.Entry{Type: "retry", TaskID: "S01"})

	args := []string{"inspect", "gamma", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_missing_tasks_file_json", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// activity.jsonl replaced by a directory: os.ReadFile fails with something that
// is not IsNotExist, which is the only way into activity.Read's error return.
// A directory is used instead of chmod 000 so the case also fails when the test
// runs as root.
func TestCharacterizeDoctorinspectInspectUnreadableLedger(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Mkdir(filepath.Join(".corvex", "tasks", "alpha", "activity.jsonl"))

	args := []string{"inspect", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_unreadable_ledger", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// Unlike doctor, inspect requires .corvex to exist and reports the absolute
// working directory in the error.
func TestCharacterizeDoctorinspectInspectNoCorvexDir(t *testing.T) {
	args := []string{"inspect", "alpha"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "doctorinspect_inspect_no_corvex_dir", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

// cobra.ExactArgs(1) on both sides of the boundary.
func TestCharacterizeDoctorinspectInspectMissingArg(t *testing.T) {
	f := newFixture(t)

	args := []string{"inspect"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_missing_arg", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeDoctorinspectInspectTooManyArgs(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	args := []string{"inspect", "alpha", "beta"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "doctorinspect_inspect_too_many_args", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeDoctorinspectInspectHelp(t *testing.T) {
	args := []string{"inspect", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "doctorinspect_inspect_help", doctorinspectScrubKeepMetrics(transcript(args, stdout, stderr, err)))
}

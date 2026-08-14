package cmd

// Characterization of `corvex validate <project>` as the user sees it: argv in,
// stdout/stderr/exit-error out. These go through the real rootCmd (runCLIIn), so
// they lock runValidate's ORDER — config load, .corvex check, wizard-if-needed,
// config reload, stack setup, validator agent, verdict line — not just the
// pieces.
//
// Every case installs a fake `claude` (validateStubClaudeNDJSON or stubClaude);
// without one, provider.NewProvider happily shells out to the real CLI and the
// test spends real money. The two end-to-end cases boot a real app process via
// TestValidateHelperAppServer and use ready_timeout so the health poll cannot
// hang.
//
// No t.Parallel(): runCLIIn chdirs the process.

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// validateConfiguredYAML renders a .corvex/config.yaml whose validate section is
// already filled in, so runValidate skips the wizard entirely
// (validateConfigured is true as soon as port or start_command is set).
func validateConfiguredYAML(startCommand string, port, readyTimeout int) string {
	return fmt.Sprintf(`project:
  name: fixture
  description: characterization fixture
provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_retries: 2
  auto_commit: false
  max_cost_usd: 25
  max_cost_per_task_usd: 5
validate:
  stack:
    runtime: go
    framework: net/http
    start_command: %s
    port: %d
    ready_timeout: %d
    health_path: /health
  database:
    type: none
  ui:
    enabled: false
`, startCommand, port, readyTimeout)
}

// ── argument and precondition errors ─────────────────────────────────────────

func TestCharacterizeValidateCmdHelp(t *testing.T) {
	args := []string{"validate", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "validate_cmd_help", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeValidateCmdNoArgs(t *testing.T) {
	args := []string{"validate"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "validate_cmd_no_args", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeValidateCmdTooManyArgs(t *testing.T) {
	args := []string{"validate", "alpha", "beta"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "validate_cmd_too_many_args", scrub(transcript(args, stdout, stderr, err)))
}

// TestCharacterizeValidateCmdOutsideProject runs in an empty temp dir: there is
// no .corvex/, so loadConfig falls back to defaults and requireCorvexDir is the
// thing that stops it.
func TestCharacterizeValidateCmdOutsideProject(t *testing.T) {
	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "validate_cmd_outside_project", scrub(transcript(args, stdout, stderr, err)))
}

// TestCharacterizeValidateCmdConfigUnreadable locks the first error runValidate
// can return: loadConfig fails before anything else happens. .corvex/config.yaml
// is a directory here, which fails identically for root and non-root.
func TestCharacterizeValidateCmdConfigUnreadable(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	if err := os.Remove(f.Path(".corvex", "config.yaml")); err != nil {
		t.Fatalf("removing fixture config: %v", err)
	}
	f.Mkdir(".corvex/config.yaml")

	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "validate_cmd_config_unreadable", scrub(transcript(args, stdout, stderr, err)))
}

// ── wizard through the command ───────────────────────────────────────────────

// TestCharacterizeValidateCmdWizard is the first-run experience: an unconfigured
// project, so `validate` stops to build a config (one AI call), writes it, and
// then goes straight on to bring the stack up with what it just learned —
// failing on the drafted start_command, which does not exist.
//
// Locks the ordering that matters for F0: the wizard's stdout comes first, the
// "setting up validation stack" log after it, and the setup error is wrapped as
// "stack setup failed: …".
func TestCharacterizeValidateCmdWizard(t *testing.T) {
	validateStubClaudeNDJSON(t, validateConfigBlock(validateDraftJSON), 0.0123, 4500)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	// 3 uncertain fields all accepted as-is (so stack.port stays 0 and the port
	// preflight cannot flake), then "n" to the manual-edit question.
	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, "\n\n\nn\n", args...)

	body := transcript(args, stdout, stderr, err) +
		"\n--- .corvex/config.yaml on disk ---\n" + f.Read(".corvex/config.yaml")
	goldenAssert(t, "validate_cmd_wizard", scrubExcept(body, "cost"))
}

// TestCharacterizeValidateCmdWizardManualFallback is the same first run with a
// provider that fails: the wizard degrades to the manual questionnaire, and the
// answers the user gives are what the stack then tries to boot.
func TestCharacterizeValidateCmdWizardManualFallback(t *testing.T) {
	stubClaude(t, `echo "stub claude: no" >&2; exit 3`)
	dockerLog := validateStubDocker(t, "exit 0")
	stubBin(t, "corvex-char-migrate", "echo migrating schema\nexit 0")
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	// runtime=keep(node) · framework=express · start=corvex-char-missing-app ·
	// port=0 (keeps the preflight out of the picture) · health=keep ·
	// db=keep(postgres) · image=keep · migrate=corvex-char-migrate · ui=keep(n)
	stdin := "\nexpress\ncorvex-char-missing-app\n0\n\n\n\ncorvex-char-migrate\n\n"

	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIStdin(t, f.Dir, stdin, args...)

	// The postgres branch injects three POSTGRES_* env vars from a map, so the
	// -e flags must be sorted before comparison — see validateSortDockerEnv.
	body := transcript(args, stdout, stderr, err) +
		"\n--- docker calls (in order, -e flags sorted) ---\n" + validateSortDockerEnv(validateReadLog(t, dockerLog))
	goldenAssert(t, "validate_cmd_wizard_manual_fallback", scrub(body))
}

// TestCharacterizeValidateCmdWizardSaveFails locks the only wrapping runValidate
// adds around the wizard ("validate wizard: %w"), which needs the config write
// itself to fail.
func TestCharacterizeValidateCmdWizardSaveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory would not stop the write")
	}
	stubClaude(t, `echo "stub claude: no" >&2; exit 3`)
	f := newBareFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)

	corvexDir := f.Path(".corvex")
	if err := os.Chmod(corvexDir, 0o500); err != nil {
		t.Fatalf("chmod .corvex: %v", err)
	}
	// Restore before t.TempDir's own cleanup, which cannot delete children of a
	// directory it may not write to.
	t.Cleanup(func() { _ = os.Chmod(corvexDir, 0o700) })

	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "validate_cmd_wizard_save_fails", scrub(transcript(args, stdout, stderr, err)))
}

// ── configured project ───────────────────────────────────────────────────────

// TestCharacterizeValidateCmdConfiguredStackFails is the everyday failure: the
// config is already there (so no wizard), and the app command is broken.
func TestCharacterizeValidateCmdConfiguredStackFails(t *testing.T) {
	stubClaude(t, `echo "stub claude: should not be called" >&2; exit 3`)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(".corvex/config.yaml", validateConfiguredYAML("corvex-char-missing-app", 0, 1))

	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "validate_cmd_stack_fails", scrub(transcript(args, stdout, stderr, err)))
}

// TestCharacterizeValidateCmdUnknownProject documents the ordering cost of the
// current design: the project is never checked up front, so the ENTIRE stack
// (container, migrations, app, health poll) is brought up and torn down before
// the missing spec.md is noticed.
func TestCharacterizeValidateCmdUnknownProject(t *testing.T) {
	port := validateFreePort(t)
	startCmd := validateHelperStartCommand(t, port)
	validateStubClaudeNDJSON(t, "unused\n", 0.01, 1000)

	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(".corvex/config.yaml", validateConfiguredYAML(startCmd, port, 10))

	args := []string{"validate", "ghost"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	body := transcript(args, stdout, stderr, err)
	goldenAssert(t, "validate_cmd_unknown_project", scrub(validateScrubPort(body, port)))
}

// ── end to end ───────────────────────────────────────────────────────────────

// TestCharacterizeValidateCmdPass is the whole command working: real app process
// on a real port, health check satisfied, validator agent (faked) returning
// PASS, verdict line printed, exit error nil.
//
// Cost and duration stay visible in the golden because they come from the fake
// provider's result line, not from a clock.
func TestCharacterizeValidateCmdPass(t *testing.T) {
	port := validateFreePort(t)
	startCmd := validateHelperStartCommand(t, port)
	validateStubClaudeNDJSON(t, "Checked /health and the two endpoints from the spec.\n\nVERDICT: PASS\n", 0.42, 12000)

	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(".corvex/config.yaml", validateConfiguredYAML(startCmd, port, 10))

	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	body := transcript(args, stdout, stderr, err)
	goldenAssert(t, "validate_cmd_pass", scrubExcept(validateScrubPort(body, port), "cost", "dur"))

	if validatePortOpen(port) {
		t.Errorf("app on port %d survived the command: the stack was not torn down", port)
	}
}

// TestCharacterizeValidateCmdFail is the same run with a FAIL verdict: the
// summary still prints, the verdict line flips, and Execute returns the bare
// "validation failed" error (which cmd.Execute would turn into exit code 1).
func TestCharacterizeValidateCmdFail(t *testing.T) {
	port := validateFreePort(t)
	startCmd := validateHelperStartCommand(t, port)
	validateStubClaudeNDJSON(t, "POST /orders returned 500.\n\nCATEGORY: missing-edge-case\nVERDICT: FAIL\n", 0.37, 9000)

	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(".corvex/config.yaml", validateConfiguredYAML(startCmd, port, 10))

	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	body := transcript(args, stdout, stderr, err)
	goldenAssert(t, "validate_cmd_fail", scrubExcept(validateScrubPort(body, port), "cost", "dur"))
}

// TestCharacterizeValidateCmdIndeterminate locks the third verdict: the agent
// answered but never emitted a VERDICT line, which is treated exactly like a
// FAIL by the printing code.
func TestCharacterizeValidateCmdIndeterminate(t *testing.T) {
	port := validateFreePort(t)
	startCmd := validateHelperStartCommand(t, port)
	validateStubClaudeNDJSON(t, "I could not reach the app.\n", 0.05, 3000)

	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(".corvex/config.yaml", validateConfiguredYAML(startCmd, port, 10))

	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	body := transcript(args, stdout, stderr, err)
	goldenAssert(t, "validate_cmd_indeterminate", scrubExcept(validateScrubPort(body, port), "cost", "dur"))
}

// TestCharacterizeValidateCmdProviderFails locks the failure between a healthy
// stack and a verdict: the provider call itself errors out.
func TestCharacterizeValidateCmdProviderFails(t *testing.T) {
	port := validateFreePort(t)
	startCmd := validateHelperStartCommand(t, port)
	stubClaude(t, `echo "stub claude: quota exhausted" >&2; exit 7`)

	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	f.Write(".corvex/config.yaml", validateConfiguredYAML(startCmd, port, 10))

	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	body := transcript(args, stdout, stderr, err)
	goldenAssert(t, "validate_cmd_provider_fails", scrub(validateScrubPort(body, port)))
}

// TestCharacterizeValidateCmdUnknownProvider locks the check between stack setup
// and the agent: an unknown provider name is only discovered AFTER the stack is
// already running.
func TestCharacterizeValidateCmdUnknownProvider(t *testing.T) {
	port := validateFreePort(t)
	startCmd := validateHelperStartCommand(t, port)

	f := newFixture(t).AddProject("alpha", fixtureSpecMD, fixtureTasksMD)
	cfgYAML := strings.Replace(validateConfiguredYAML(startCmd, port, 10), "default: claude-cli", "default: gpt-9", 1)
	f.Write(".corvex/config.yaml", cfgYAML)

	args := []string{"validate", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)

	body := transcript(args, stdout, stderr, err)
	goldenAssert(t, "validate_cmd_provider_unknown", scrub(validateScrubPort(body, port)))

	if validatePortOpen(port) {
		t.Errorf("app on port %d survived the command: the deferred cleanup did not run", port)
	}
}

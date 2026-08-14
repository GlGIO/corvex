package cmd

// Characterization of the `validate` config wizard — the block F0 moves out of
// cmd/validate.go into internal/wizard.
//
// Covered here: validateConfigured, wizardPrompt, printDetected,
// confirmUncertain, applyFieldOverride, manualOverride, manualValidateWizard,
// runValidateWizard (both the AI-success and AI-failure branches) and
// saveConfig's on-disk shape.
//
// These call the functions directly instead of going through `corvex validate`,
// because runValidate continues into setupValidationStack right after the
// wizard — the wizard's own output would be buried under stack noise. The
// command-level path is characterized in char_validate_cmd_test.go.
//
// No t.Parallel() anywhere: validateCapture swaps process-global stdout/stdin.

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/orchestrator"
)

// validateDraftJSON is the ```config payload the fake claude returns for the
// AI branch of the wizard. Deliberately shaped to exercise every printing
// branch at once:
//
//   - detected entries WITH a source and WITHOUT one (two print formats),
//   - uncertain entries with a reason and without one (two hint formats),
//   - an uncertain field name applyFieldOverride does not know ("stack.bogus"),
//     which is silently dropped,
//   - ready_timeout: 1 so any code path that reaches waitForHealth fails in a
//     second instead of thirty.
const validateDraftJSON = `{
  "stack": {
    "runtime": "node",
    "framework": "nestjs",
    "start_command": "corvex-char-missing-app",
    "port": 0,
    "health_path": "/health",
    "ready_timeout": 1
  },
  "database": {"type": "none", "image": "", "migrate_command": ""},
  "ui": {"enabled": false},
  "detected": [
    {"field": "stack.runtime", "value": "node", "source": "package.json"},
    {"field": "stack.framework", "value": "nestjs", "source": "package.json dependencies"},
    {"field": "database.type", "value": "none", "source": ""}
  ],
  "uncertain": [
    {"field": "stack.health_path", "guess": "/health", "reason": "no explicit health endpoint found"},
    {"field": "stack.port", "guess": "0", "reason": ""},
    {"field": "stack.bogus", "guess": "whatever", "reason": "field applyFieldOverride ignores"}
  ]
}`

// ── validateConfigured ───────────────────────────────────────────────────────

// TestCharacterizeValidateConfigured locks the gate that decides whether the
// wizard runs at all: a port OR a start command is enough, everything else is
// irrelevant.
func TestCharacterizeValidateConfigured(t *testing.T) {
	cases := []struct {
		label string
		cfg   config.ValidateConfig
	}{
		{"zero value", config.ValidateConfig{}},
		{"port only", config.ValidateConfig{Stack: config.ValidateStackConfig{Port: 3000}}},
		{"start_command only", config.ValidateConfig{Stack: config.ValidateStackConfig{StartCommand: "npm start"}}},
		{"both", config.ValidateConfig{Stack: config.ValidateStackConfig{Port: 3000, StartCommand: "npm start"}}},
		{"runtime+framework but no port/command", config.ValidateConfig{Stack: config.ValidateStackConfig{Runtime: "node", Framework: "nestjs"}}},
		{"database configured only", config.ValidateConfig{Database: config.ValidateDBConfig{Type: "postgres", Image: "postgres:16"}}},
		{"ui enabled only", config.ValidateConfig{UI: config.ValidateUIConfig{Enabled: true}}},
	}

	var b strings.Builder
	b.WriteString("# validateConfigured(v) — true means \"skip the wizard\"\n\n")
	for _, c := range cases {
		fmt.Fprintf(&b, "%-40s → %v\n", c.label, validateConfigured(c.cfg))
	}
	goldenAssert(t, "validate_configured_table", b.String())
}

// ── wizardPrompt ─────────────────────────────────────────────────────────────

// TestCharacterizeValidateWizardPrompt locks the single primitive every prompt
// in the wizard is built from, including the two behaviours that surprise
// people: an empty answer keeps the default (so a prompt whose default is
// non-empty can never be cleared), and EOF returns the default too (so a
// non-interactive `validate` silently accepts every default).
func TestCharacterizeValidateWizardPrompt(t *testing.T) {
	cases := []struct {
		label      string
		stdin      string
		question   string
		defaultVal string
	}{
		{"answer given, with default", "8080\n", "Port?", "3000"},
		{"empty answer keeps default", "\n", "Port?", "3000"},
		{"whitespace answer keeps default", "   \n", "Port?", "3000"},
		{"answer is trimmed", "  gin  \n", "Framework?", ""},
		{"no default → no brackets", "fastapi\n", "Framework?", ""},
		{"EOF returns default", "", "Port?", "3000"},
		{"EOF with empty default returns empty", "", "Framework?", ""},
		{"answer without trailing newline is still read at EOF", "9090", "Port?", "3000"},
	}

	var b strings.Builder
	for _, c := range cases {
		var got string
		stdout, stderr := validateCapture(t, c.stdin, func(r *bufio.Reader) {
			got = wizardPrompt(r, c.question, c.defaultVal)
		})
		fmt.Fprintf(&b, "## %s\nstdin=%q question=%q default=%q\nprinted=%q\nreturned=%q\nstderr=%q\n\n",
			c.label, c.stdin, c.question, c.defaultVal, stdout, got, stderr)
	}
	goldenAssert(t, "validate_wizard_prompt", b.String())
}

// ── printDetected ────────────────────────────────────────────────────────────

func TestCharacterizeValidatePrintDetected(t *testing.T) {
	var b strings.Builder

	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		printDetected([]orchestrator.DetectedField{
			{Field: "stack.runtime", Value: "node", Source: "package.json"},
			{Field: "stack.port", Value: "3000", Source: "src/main.ts"},
			{Field: "database.type", Value: "none", Source: ""},
			{Field: "stack.framework", Value: "", Source: "guessed"},
		})
	})
	b.WriteString(validateTranscript("printDetected with 4 fields (one sourceless, one valueless)", stdout, stderr, nil))
	b.WriteString("\n")

	stdout, stderr = validateCapture(t, "", func(*bufio.Reader) {
		printDetected(nil)
	})
	b.WriteString(validateTranscript("printDetected with nothing detected", stdout, stderr, nil))

	goldenAssert(t, "validate_print_detected", b.String())
}

// ── confirmUncertain ─────────────────────────────────────────────────────────

// TestCharacterizeValidateConfirmUncertain locks the prompt text (guess and
// "guess — reason" hint forms) AND the resulting config, since the answers are
// routed through applyFieldOverride and a typo in a field name is dropped in
// silence.
func TestCharacterizeValidateConfirmUncertain(t *testing.T) {
	uncertain := []orchestrator.UncertainField{
		{Field: "stack.health_path", Guess: "/health", Reason: "no explicit health endpoint found"},
		{Field: "stack.port", Guess: "3000", Reason: ""},
		{Field: "stack.ready_timeout", Guess: "30", Reason: "convention"},
		{Field: "ui.enabled", Guess: "n", Reason: "no frontend found"},
		{Field: "stack.bogus", Guess: "whatever", Reason: "not a field applyFieldOverride knows"},
	}

	// health_path: accept the guess (empty answer) · port: override with 4321 ·
	// ready_timeout: answer garbage, which does NOT fall back to the displayed
	// guess of 30 but to the config's current value (0) — see bugsObserved ·
	// ui.enabled: "yes" · bogus: answered but dropped.
	stdin := "\n4321\nnot-a-number\nyes\nsomething\n"

	v := config.ValidateConfig{Stack: config.ValidateStackConfig{Runtime: "node"}}
	stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
		confirmUncertain(r, &v, uncertain)
	})

	body := validateTranscript("confirmUncertain with 5 fields", stdout, stderr, nil) +
		"\n" + validateDumpConfig(t, v)

	// Empty list must print nothing at all.
	stdout2, stderr2 := validateCapture(t, "", func(r *bufio.Reader) {
		empty := config.ValidateConfig{}
		confirmUncertain(r, &empty, nil)
	})
	body += "\n" + validateTranscript("confirmUncertain with no uncertain fields", stdout2, stderr2, nil)

	goldenAssert(t, "validate_confirm_uncertain", body)
}

// ── applyFieldOverride ───────────────────────────────────────────────────────

// TestCharacterizeValidateApplyFieldOverride walks every dotted path the
// switch knows, plus two it does not (an unknown field and a bad int), each
// against the same seed so the golden shows exactly one field moving.
func TestCharacterizeValidateApplyFieldOverride(t *testing.T) {
	seed := func() config.ValidateConfig {
		return config.ValidateConfig{
			Stack: config.ValidateStackConfig{
				Runtime: "node", Framework: "nestjs", StartCommand: "npm start",
				Port: 3000, ReadyTimeout: 30, HealthPath: "/health",
			},
			Database: config.ValidateDBConfig{Type: "postgres", Image: "postgres:16", MigrateCommand: "npm run migrate"},
			UI:       config.ValidateUIConfig{Enabled: false},
		}
	}

	cases := []struct{ field, value string }{
		{"stack.runtime", "python"},
		{"stack.framework", "fastapi"},
		{"stack.start_command", "uvicorn app:app"},
		{"stack.port", "8080"},
		{"stack.port", "eighty-eighty"},
		{"stack.port", ""},
		{"stack.health_path", "/healthz"},
		{"stack.health_path", ""},
		{"stack.ready_timeout", "5"},
		{"stack.ready_timeout", "nope"},
		{"database.type", "mysql"},
		{"database.image", "mysql:8"},
		{"database.migrate_command", "make migrate"},
		{"ui.enabled", "y"},
		{"ui.enabled", "yes"},
		{"ui.enabled", "true"},
		{"ui.enabled", "Y"},
		{"ui.enabled", "n"},
		{"stack.env_file", "backend/.env-stg"},
		{"unknown.field", "ignored"},
		{"", "ignored"},
	}

	var b strings.Builder
	b.WriteString("# applyFieldOverride(v, field, value) — only the changed fields are listed\n")
	b.WriteString("# seed: runtime=node framework=nestjs start_command=\"npm start\" port=3000\n")
	b.WriteString("#       ready_timeout=30 health_path=/health env_file=\"\"\n")
	b.WriteString("#       database=postgres/postgres:16/\"npm run migrate\" ui.enabled=false\n\n")
	for _, c := range cases {
		before := seed()
		after := seed()
		applyFieldOverride(&after, c.field, c.value)
		fmt.Fprintf(&b, "%-28s value=%-18q → %s\n", c.field, c.value, validateDiffFields(before, after))
	}
	goldenAssert(t, "validate_apply_field_override", b.String())
}

// validateDiffFields renders the fields that differ between two configs, or
// "(no change)". Keeps the golden readable: one line per case instead of a
// 12-line YAML dump each.
func validateDiffFields(before, after config.ValidateConfig) string {
	type pair struct {
		name     string
		from, to string
	}
	pairs := []pair{
		{"stack.runtime", before.Stack.Runtime, after.Stack.Runtime},
		{"stack.framework", before.Stack.Framework, after.Stack.Framework},
		{"stack.start_command", before.Stack.StartCommand, after.Stack.StartCommand},
		{"stack.port", fmt.Sprint(before.Stack.Port), fmt.Sprint(after.Stack.Port)},
		{"stack.ready_timeout", fmt.Sprint(before.Stack.ReadyTimeout), fmt.Sprint(after.Stack.ReadyTimeout)},
		{"stack.health_path", before.Stack.HealthPath, after.Stack.HealthPath},
		{"stack.env_file", before.Stack.EnvFile, after.Stack.EnvFile},
		{"database.type", before.Database.Type, after.Database.Type},
		{"database.image", before.Database.Image, after.Database.Image},
		{"database.migrate_command", before.Database.MigrateCommand, after.Database.MigrateCommand},
		{"ui.enabled", fmt.Sprint(before.UI.Enabled), fmt.Sprint(after.UI.Enabled)},
	}
	var changed []string
	for _, p := range pairs {
		if p.from != p.to {
			changed = append(changed, fmt.Sprintf("%s: %q → %q", p.name, p.from, p.to))
		}
	}
	if len(changed) == 0 {
		return "(no change)"
	}
	return strings.Join(changed, ", ")
}

// ── manualOverride ───────────────────────────────────────────────────────────

// TestCharacterizeValidateManualOverride locks the "edit any field manually"
// pass: nine prompts in a fixed order, every current value offered as the
// default. Note stack.env_file is NOT offered — a config that uses env_file
// cannot be edited through this wizard.
func TestCharacterizeValidateManualOverride(t *testing.T) {
	current := config.ValidateConfig{
		Stack: config.ValidateStackConfig{
			Runtime: "node", Framework: "nestjs", StartCommand: "npm run start:test",
			Port: 3000, ReadyTimeout: 30, HealthPath: "/health", EnvFile: "backend/.env-stg",
		},
		Database: config.ValidateDBConfig{Type: "postgres", Image: "postgres:16", MigrateCommand: "npm run migrate"},
		UI:       config.ValidateUIConfig{Enabled: true},
	}

	// runtime → keep · framework → gin · start_command → keep · port → 8080 ·
	// health_path → /healthz · db type → none · image → keep · migrate → keep ·
	// ui → n
	stdin := "\ngin\n\n8080\n/healthz\nnone\n\n\nn\n"

	v := current
	stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
		manualOverride(r, &v)
	})
	body := validateTranscript("manualOverride (all 9 prompts)", stdout, stderr, nil) +
		"\n" + validateDumpConfig(t, v)

	// Same function against a zero config: every default is empty (and port
	// prints as "0"), which changes the bracket rendering.
	zero := config.ValidateConfig{}
	stdout2, stderr2 := validateCapture(t, "", func(r *bufio.Reader) {
		manualOverride(r, &zero)
	})
	body += "\n" + validateTranscript("manualOverride on a zero config, stdin at EOF", stdout2, stderr2, nil) +
		"\n" + validateDumpConfig(t, zero)

	goldenAssert(t, "validate_manual_override", body)
}

// ── manualValidateWizard ─────────────────────────────────────────────────────

// TestCharacterizeValidateManualWizardPostgres locks the fallback wizard's
// postgres branch: the extra image/migrate prompts, the hardcoded
// ready_timeout of 30 (never asked), the injected POSTGRES_* env map, and the
// exact bytes saveConfig writes to .corvex/config.yaml.
func TestCharacterizeValidateManualWizardPostgres(t *testing.T) {
	f := newFixture(t)
	cfg := config.Default()

	// runtime=go · framework=gin · start=./run-app · port=8080 · health=keep ·
	// db=keep(postgres) · image=keep · migrate=make migrate · ui=y
	stdin := "go\ngin\n./run-app\n8080\n\n\n\nmake migrate\ny\n"

	var err error
	stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
		err = manualValidateWizard(r, f.Dir, cfg)
	})

	body := validateTranscript("manualValidateWizard — postgres", stdout, stderr, err) +
		"\n" + validateDumpConfig(t, cfg.Validate) +
		"\n--- .corvex/config.yaml on disk ---\n" + f.Read(".corvex/config.yaml")
	goldenAssert(t, "validate_manual_wizard_postgres", scrub(body))
}

// TestCharacterizeValidateManualWizardNoDB locks the branch that skips the
// database prompts, and the fact that answering "" to
// "Health check path? (empty to skip)" does NOT skip it — wizardPrompt returns
// the default, so /health is kept.
func TestCharacterizeValidateManualWizardNoDB(t *testing.T) {
	f := newFixture(t)
	cfg := config.Default()

	// runtime=python · framework=fastapi · start=uvicorn app · port=9000 ·
	// health="" (kept as /health) · db=none · ui=""(n)
	stdin := "python\nfastapi\nuvicorn app\n9000\n\nnone\n\n"

	var err error
	stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
		err = manualValidateWizard(r, f.Dir, cfg)
	})

	body := validateTranscript("manualValidateWizard — database none", stdout, stderr, err) +
		"\n" + validateDumpConfig(t, cfg.Validate)
	goldenAssert(t, "validate_manual_wizard_nodb", scrub(body))
}

// TestCharacterizeValidateManualWizardDBDefaults locks the per-type default
// docker image offered by the fallback wizard (postgres:16, mysql:8, otherwise
// "<type>:latest") and the fact that the POSTGRES_* env map is injected for
// postgres ONLY — a mysql stack gets no credentials at all, so the container
// will refuse to boot unless the user edits config.yaml by hand.
func TestCharacterizeValidateManualWizardDBDefaults(t *testing.T) {
	var b strings.Builder
	for _, dbType := range []string{"mysql", "mongodb", "sqlite"} {
		f := newFixture(t)
		cfg := config.Default()
		// runtime/framework/start/port/health all default; then the db type;
		// then (when asked) image and migrate default; then ui defaults.
		stdin := "\n\n\n\n\n" + dbType + "\n"

		var err error
		stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
			err = manualValidateWizard(r, f.Dir, cfg)
		})
		b.WriteString(validateTranscript("manualValidateWizard — database "+dbType, stdout, stderr, err))
		b.WriteString("\n" + validateDumpConfig(t, cfg.Validate) + "\n")
	}
	goldenAssert(t, "validate_manual_wizard_db_defaults", scrub(b.String()))
}

// TestCharacterizeValidateManualWizardEOF is the CI case: stdin is closed, so
// every prompt takes its default and a full config is written without a single
// answer from the user.
func TestCharacterizeValidateManualWizardEOF(t *testing.T) {
	f := newFixture(t)
	cfg := config.Default()

	var err error
	stdout, stderr := validateCapture(t, "", func(r *bufio.Reader) {
		err = manualValidateWizard(r, f.Dir, cfg)
	})

	body := validateTranscript("manualValidateWizard — stdin at EOF", stdout, stderr, err) +
		"\n" + validateDumpConfig(t, cfg.Validate)
	goldenAssert(t, "validate_manual_wizard_eof", scrub(body))
}

// TestCharacterizeValidateManualWizardSaveFailure locks the error path of
// saveConfig: no .corvex directory to write into.
func TestCharacterizeValidateManualWizardSaveFailure(t *testing.T) {
	dir := t.TempDir() // deliberately has no .corvex/
	cfg := config.Default()

	var err error
	stdout, stderr := validateCapture(t, "", func(r *bufio.Reader) {
		err = manualValidateWizard(r, dir, cfg)
	})

	body := validateTranscript("manualValidateWizard — .corvex missing", stdout, stderr, err)
	goldenAssert(t, "validate_manual_wizard_save_error", scrub(body))
}

// ── runValidateWizard ────────────────────────────────────────────────────────

// TestCharacterizeValidateWizardAI locks the happy path of the AI branch: the
// banner, the live tool-use progress line, the detected list, the uncertain
// confirmations, the "Edit any field manually?" question answered no, the
// inference cost line, and the config.yaml written to disk.
//
// Cost and duration survive scrubbing (scrubExcept ... "cost") because they
// come from the stub, not from a clock.
func TestCharacterizeValidateWizardAI(t *testing.T) {
	validateStubClaudeNDJSON(t, validateConfigBlock(validateDraftJSON), 0.0123, 4500)

	f := newFixture(t)
	cfg := config.Default()

	// health_path → keep guess · port → 4567 · bogus → answered, dropped ·
	// "Edit any field manually?" → n
	stdin := "\n4567\nx\nn\n"

	var err error
	stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
		err = runValidateWizard(context.Background(), r, f.Dir, cfg)
	})

	body := validateTranscript("runValidateWizard — AI inference succeeds", stdout, stderr, err) +
		"\n" + validateDumpConfig(t, cfg.Validate) +
		"\n--- .corvex/config.yaml on disk ---\n" + f.Read(".corvex/config.yaml")
	goldenAssert(t, "validate_wizard_ai", scrubExcept(body, "cost"))
}

// TestCharacterizeValidateWizardAIManualEdit is the same draft with the manual
// edit answered "y", so runValidateWizard chains straight into manualOverride.
func TestCharacterizeValidateWizardAIManualEdit(t *testing.T) {
	validateStubClaudeNDJSON(t, validateConfigBlock(validateDraftJSON), 0.0123, 4500)

	f := newFixture(t)
	cfg := config.Default()

	// 3 uncertain answers (all "keep"), then y, then the 9 manualOverride
	// prompts: runtime→go, framework→keep, start→./app, port→7777,
	// health→keep, db type→sqlite, image→keep, migrate→keep, ui→n.
	stdin := "\n\n\ny\ngo\n\n./app\n7777\n\nsqlite\n\n\nn\n"

	var err error
	stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
		err = runValidateWizard(context.Background(), r, f.Dir, cfg)
	})

	body := validateTranscript("runValidateWizard — AI draft then manual edit", stdout, stderr, err) +
		"\n" + validateDumpConfig(t, cfg.Validate)
	goldenAssert(t, "validate_wizard_ai_manual_edit", scrubExcept(body, "cost"))
}

// TestCharacterizeValidateWizardAIFailure locks the fallback: when the provider
// call fails, the warning is logged and the manual wizard takes over from the
// SAME reader, so the answers scripted for the AI branch would be consumed by
// the manual prompts.
func TestCharacterizeValidateWizardAIFailure(t *testing.T) {
	stubClaude(t, `echo "stub claude: no" >&2; exit 3`)

	f := newFixture(t)
	cfg := config.Default()

	// Manual wizard: runtime=keep · framework=express · start=keep · port=8080 ·
	// health=keep · db=none · ui=keep(n)
	stdin := "\nexpress\n\n8080\n\nnone\n\n"

	var err error
	stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
		err = runValidateWizard(context.Background(), r, f.Dir, cfg)
	})

	body := validateTranscript("runValidateWizard — provider fails, manual fallback", stdout, stderr, err) +
		"\n" + validateDumpConfig(t, cfg.Validate)
	goldenAssert(t, "validate_wizard_ai_failure", scrub(body))
}

// TestCharacterizeValidateWizardBadBlock locks the other inference failure: the
// provider succeeds but its output carries no ```config block, so parsing fails
// and the manual wizard takes over. The warning wording differs from the
// exec-failure case, which is why it gets its own golden.
func TestCharacterizeValidateWizardBadBlock(t *testing.T) {
	validateStubClaudeNDJSON(t, "I could not figure out the stack, sorry.\n", 0.0077, 2500)

	f := newFixture(t)
	cfg := config.Default()
	stdin := "\n\n\n\n\nnone\n\n"

	var err error
	stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
		err = runValidateWizard(context.Background(), r, f.Dir, cfg)
	})

	body := validateTranscript("runValidateWizard — no config block in AI output", stdout, stderr, err) +
		"\n" + validateDumpConfig(t, cfg.Validate)
	goldenAssert(t, "validate_wizard_bad_block", scrub(body))
}

// TestCharacterizeValidateWizardUnknownProvider locks the third failure mode:
// provider.NewProvider itself refuses the configured provider name, which also
// degrades to the manual wizard rather than aborting.
func TestCharacterizeValidateWizardUnknownProvider(t *testing.T) {
	f := newFixture(t)
	cfg := config.Default()
	cfg.Provider.Default = "gpt-9"

	stdin := "\n\n\n\n\nnone\n\n"

	var err error
	stdout, stderr := validateCapture(t, stdin, func(r *bufio.Reader) {
		err = runValidateWizard(context.Background(), r, f.Dir, cfg)
	})

	body := validateTranscript("runValidateWizard — unknown provider", stdout, stderr, err)
	goldenAssert(t, "validate_wizard_unknown_provider", scrub(body))
}

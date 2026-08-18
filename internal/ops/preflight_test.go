package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The preflight (F9). What it has to get right is the order: it answers before
// the first token, and it says which dependency is missing rather than "a task
// failed" three retries later.

const preflightRecipe = `name: shipit
description: needs tooling
requires:
  - bin: corvex-preflight-absent
    why: it is how the release is cut
  - env: CORVEX_PREFLIGHT_ABSENT
stages:
  - id: S01
    title: Build
    kind: tool
    command: "true"
`

func writeRecipeFile(t *testing.T, workDir, name, body string) {
	t.Helper()
	dir := filepath.Join(workDir, ".corvex", "recipes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPreflight_ReportsWhatIsMissingAndWhy(t *testing.T) {
	workDir := t.TempDir()
	writeRecipeFile(t, workDir, "shipit", preflightRecipe)

	checks, err := PreflightRequirements(workDir, "shipit")
	if err != nil {
		t.Fatalf("PreflightRequirements: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("got %d check(s), want 2: %+v", len(checks), checks)
	}
	failure := MissingRequirements(checks)
	if failure == nil {
		t.Fatal("a recipe declaring a missing binary and a missing variable passed the preflight")
	}
	msg := failure.Error()
	for _, want := range []string{"corvex-preflight-absent", "CORVEX_PREFLIGHT_ABSENT", "it is how the release is cut", "Nothing was spent"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the failure does not mention %q:\n%s", want, msg)
		}
	}
}

// A satisfied requirement passes, and the check never prints the VALUE of the
// variable it looked for — a preflight that echoed the credential would be the
// leak it exists to prevent.
func TestPreflight_PassesAndNeverEchoesAValue(t *testing.T) {
	workDir := t.TempDir()
	writeRecipeFile(t, workDir, "ok", `name: ok
requires:
  - env: CORVEX_PREFLIGHT_PRESENT
  - bin: sh
stages:
  - id: S01
    title: Build
    kind: tool
    command: "true"
`)
	t.Setenv("CORVEX_PREFLIGHT_PRESENT", "super-secret-value")

	checks, err := PreflightRequirements(workDir, "ok")
	if err != nil {
		t.Fatalf("PreflightRequirements: %v", err)
	}
	if failure := MissingRequirements(checks); failure != nil {
		t.Fatalf("a satisfied preflight refused the run: %v", failure)
	}
	for _, c := range checks {
		if strings.Contains(c.Detail, "super-secret-value") {
			t.Errorf("the preflight echoed the value of %s: %q", c.Name, c.Detail)
		}
	}
}

// The legacy spec.md path has nowhere to declare requirements and must keep
// working: no recipe means no checks, not a refusal.
func TestPreflight_LegacyProjectDeclaresNothing(t *testing.T) {
	checks, err := PreflightRequirements(t.TempDir(), "alpha")
	if err != nil {
		t.Fatalf("PreflightRequirements: %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("a project with no recipe produced %d check(s)", len(checks))
	}
	if failure := MissingRequirements(checks); failure != nil {
		t.Fatalf("a project with no recipe was refused: %v", failure)
	}
}

// A malformed `requires:` is a recipe error, caught by validation rather than
// silently ignored — an unenforced declaration is worse than none, because the
// author believes it is enforced.
func TestPreflight_RequirementMustDeclareExactlyOneThing(t *testing.T) {
	workDir := t.TempDir()
	writeRecipeFile(t, workDir, "bad", `name: bad
requires:
  - bin: az
    env: TOKEN
stages:
  - id: S01
    title: Build
    kind: tool
    command: "true"
`)
	if _, err := ValidateRecipe(workDir, "bad"); err == nil {
		t.Fatal("a requirement declaring both bin and env was accepted")
	}
}

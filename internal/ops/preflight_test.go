package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/recipe"
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

// A `bin:` with a separator is a path, and the path the preflight stats has to
// be the one the stage will run: the executor invokes the command with `sh -c`
// and cmd.Dir = the run's workDir, so a relative script belongs to workDir and
// to nothing else.
//
// This is the POSITIVE CONTROL of the pair below: the script is in workDir and
// nowhere near the process working directory.
func TestPreflight_RelativeBinResolvesAgainstWorkDir(t *testing.T) {
	workDir := t.TempDir()
	writeScript(t, workDir, 0o755)
	writeRecipeFile(t, workDir, "shipit", relativeBinRecipe)
	// Somewhere else entirely, so a check that consulted the CWD would find
	// nothing and refuse a run that is ready.
	t.Chdir(t.TempDir())

	checks, err := PreflightRequirements(workDir, "shipit")
	if err != nil {
		t.Fatalf("PreflightRequirements: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("got %d check(s), want 1: %+v", len(checks), checks)
	}
	c := checks[0]
	if !c.OK {
		t.Fatalf("a script that IS in the workDir was refused: %+v", c)
	}
	want := filepath.Join(workDir, "scripts", "deploy.sh")
	if c.Detail != want {
		t.Errorf("Detail is %q, want the absolute path %q — a relative detail means nothing in the ledger", c.Detail, want)
	}
}

// The NEGATIVE CONTROL of the pair: the script exists in the process working
// directory and NOT in the workDir. Without this case the positive one would
// also pass for an implementation that merely added the CWD to the search,
// which is the false positive that matters most — it approves a run and writes
// a path into the ledger that resolves to a file the run will never see.
func TestPreflight_RelativeBinIgnoresProcessCWD(t *testing.T) {
	decoy := t.TempDir()
	writeScript(t, decoy, 0o755)
	t.Chdir(decoy)

	workDir := t.TempDir()
	writeRecipeFile(t, workDir, "shipit", relativeBinRecipe)

	checks, err := PreflightRequirements(workDir, "shipit")
	if err != nil {
		t.Fatalf("PreflightRequirements: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("got %d check(s), want 1: %+v", len(checks), checks)
	}
	if checks[0].OK {
		t.Fatalf("the preflight approved a script that only exists in the process CWD: %+v", checks[0])
	}
	failure := MissingRequirements(checks)
	if failure == nil {
		t.Fatal("a run whose declared script is not in its workDir was allowed to start")
	}
	if !strings.Contains(failure.Error(), "scripts/deploy.sh") {
		t.Errorf("the failure does not name the script:\n%s", failure)
	}
}

// A file that is there but has no execute bit is a chmod away from working.
// Calling it "not on PATH" sends the reader to install something they already
// have, so the check says which of the two problems it is.
func TestPreflight_RelativeBinFoundButNotExecutable(t *testing.T) {
	workDir := t.TempDir()
	writeScript(t, workDir, 0o644)
	writeRecipeFile(t, workDir, "shipit", relativeBinRecipe)
	t.Chdir(t.TempDir())

	checks, err := PreflightRequirements(workDir, "shipit")
	if err != nil {
		t.Fatalf("PreflightRequirements: %v", err)
	}
	if checks[0].OK {
		t.Fatalf("a script without its execute bit passed the preflight: %+v", checks[0])
	}
	if !strings.Contains(checks[0].Detail, "found but not executable") {
		t.Errorf("Detail is %q, want it to say the file is there but not executable", checks[0].Detail)
	}
}

// Regression guard for the other half of the contract: a bare name is still a
// PATH lookup, so resolving paths against the workDir cannot have turned every
// declared CLI into a file the repo has to contain.
func TestPreflight_BareBinNameStillResolvesOnPATH(t *testing.T) {
	workDir := t.TempDir()
	writeRecipeFile(t, workDir, "shipit", `name: shipit
requires:
  - bin: sh
stages:
  - id: S01
    title: Build
    kind: tool
    command: "true"
`)
	t.Chdir(t.TempDir())

	checks, err := PreflightRequirements(workDir, "shipit")
	if err != nil {
		t.Fatalf("PreflightRequirements: %v", err)
	}
	if !checks[0].OK {
		t.Fatalf("sh stopped being found on PATH: %+v", checks[0])
	}
	if strings.HasPrefix(checks[0].Detail, workDir) {
		t.Errorf("a bare name was looked up inside the workDir: %q", checks[0].Detail)
	}
}

const relativeBinRecipe = `name: shipit
requires:
  - bin: scripts/deploy.sh
    why: it is how the release is cut
stages:
  - id: S01
    title: Deploy
    kind: tool
    command: "scripts/deploy.sh"
`

func writeScript(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "deploy.sh"), []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

// TestPreflight_BareBinFoundOnPATHButNotExecutable is the case the message used
// to get wrong, and it is the DOMINANT one: `bin: az` — a bare name — is the
// shape the README's own example declares.
//
// exec.LookPath only distinguishes "missing" from "there but not executable" for
// the PATH form of a `bin:`. For a bare name it walks PATH and skips every
// candidate it cannot run, then returns one flat "executable file not found in
// $PATH". So the ErrPermission branch this check used to rely on never fired
// here, and a CLI sitting in a PATH directory with mode 0644 was reported as not
// installed — which costs the reader an install of something they already have.
//
// The negative control is in the same test: a name that really is nowhere still
// has to read "not on PATH", or the fix has traded one lie for the other.
func TestPreflight_BareBinFoundOnPATHButNotExecutable(t *testing.T) {
	binDir := t.TempDir()
	present := filepath.Join(binDir, "corvex-preflight-chmodme")
	if err := os.WriteFile(present, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workDir := t.TempDir()
	writeRecipeFile(t, workDir, "shipit", `name: shipit
requires:
  - bin: corvex-preflight-chmodme
    why: it is how the release is cut
  - bin: corvex-preflight-nowhere
stages:
  - id: S01
    title: Build
    kind: tool
    command: "true"
`)

	checks, err := PreflightRequirements(workDir, "shipit")
	if err != nil {
		t.Fatalf("PreflightRequirements: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("got %d check(s), want 2: %+v", len(checks), checks)
	}
	if checks[0].OK {
		t.Fatalf("a binary without its execute bit passed the preflight: %+v", checks[0])
	}
	if !strings.Contains(checks[0].Detail, "found but not executable") {
		t.Errorf("Detail is %q, want it to say the file is there and needs a chmod, not an install", checks[0].Detail)
	}
	if !strings.Contains(checks[0].Detail, present) {
		t.Errorf("Detail is %q, want it to name %s — the reader has to know WHICH copy to chmod", checks[0].Detail, present)
	}
	// Negative control: nothing anywhere is still "not on PATH".
	if checks[1].Detail != "not on PATH" {
		t.Errorf("a binary that is genuinely absent reads %q, want %q", checks[1].Detail, "not on PATH")
	}
}

// A `bin:` that resolves to a DIRECTORY is on disk under exactly that name, and
// LookPath rejects it with EISDIR — which is neither ErrPermission nor a missing
// file, so it used to be reported as "not found: <abs>" about a path the reader
// can see in their own repository.
func TestPreflight_BinThatIsADirectorySaysSo(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "scripts", "deploy.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecipeFile(t, workDir, "shipit", relativeBinRecipe)
	t.Chdir(t.TempDir())

	checks, err := PreflightRequirements(workDir, "shipit")
	if err != nil {
		t.Fatalf("PreflightRequirements: %v", err)
	}
	if checks[0].OK {
		t.Fatalf("a directory passed the preflight as a binary: %+v", checks[0])
	}
	if !strings.Contains(checks[0].Detail, "is a directory") {
		t.Errorf("Detail is %q, want it to say the path is a directory instead of claiming it is not there", checks[0].Detail)
	}
}

// `requires: - mcp: <name>` is checked against the RESOLVED config.
//
// "The agent can reach production data" is a dependency exactly like a CLI being
// installed, and it is the one that fails most expensively: the reference flow
// records an agent burning 85k tokens to conclude "I could not prove it, the
// database MCP does not exist in my environment".
//
// The check asks the config corvex parsed, not the file. The first version of
// this guard lived in a recipe and grepped `^mcp:` — while the key is
// `mcp_servers:`. It would have refused a correctly configured repository and
// passed a misspelled one, which is the whole failure mode of a control that
// reimplements the rule instead of going through its door.
func TestPreflight_MCPRequirementReadsTheResolvedConfig(t *testing.T) {
	tests := []struct {
		name    string
		servers []config.MCPServerConfig
		want    bool
		detail  string
	}{
		{
			name:    "declared",
			servers: []config.MCPServerConfig{{Name: "prd"}},
			want:    true,
			detail:  "declared",
		},
		{
			name:    "declared under another name",
			servers: []config.MCPServerConfig{{Name: "stg"}},
			want:    false,
			detail:  "not declared in mcp_servers:",
		},
		{
			name:    "nothing configured at all",
			servers: nil,
			want:    false,
			detail:  "not declared in mcp_servers:",
		},
		{
			name:    "case does not decide access to production",
			servers: []config.MCPServerConfig{{Name: "PRD"}},
			want:    true,
			detail:  "declared",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg config.Config
			cfg.Sandbox.MCPServers = tt.servers
			checks := checkRequirementsWithConfig(t.TempDir(),
				[]recipe.Requirement{{MCP: "prd", Why: "o diagnóstico lê PRD"}}, cfg)
			if len(checks) != 1 {
				t.Fatalf("got %d checks, want 1", len(checks))
			}
			if checks[0].OK != tt.want {
				t.Errorf("OK = %v, want %v (%s)", checks[0].OK, tt.want, checks[0].Detail)
			}
			if checks[0].Detail != tt.detail {
				t.Errorf("Detail = %q, want %q", checks[0].Detail, tt.detail)
			}
			if checks[0].Kind != "mcp" {
				t.Errorf("Kind = %q, want mcp", checks[0].Kind)
			}
		})
	}
}

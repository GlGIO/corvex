package step

// Credential custody (F9). The claim under test is narrow and load-bearing: a
// credential the runner is told to keep NEVER reaches the worker, whatever the
// allowlist says — because a credential in the worker's environment is one Bash
// call away from leaving the machine, and no gate downstream can undo that.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/types"
)

func TestCollectAuthEnv_RunnerKeepsWhatItWasToldToKeep(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-model")
	t.Setenv("CORVEX_SECRET_TOKEN", "shipping-credential")

	allow := config.DefaultEnvAllowlist()

	// Without custody the prefix wins and both travel — this is today's
	// behaviour, and the control that proves the test below is not vacuous.
	open := collectAuthEnv(allow, nil)
	if open["CORVEX_SECRET_TOKEN"] != "shipping-credential" {
		t.Fatalf("the allowlist did not forward the credential at all: %v", keysOf(open))
	}

	held := collectAuthEnv(allow, []string{"CORVEX_SECRET_TOKEN"})
	if _, leaked := held["CORVEX_SECRET_TOKEN"]; leaked {
		t.Error("a credential named in runner_only_env reached the worker")
	}
	if held["ANTHROPIC_API_KEY"] != "sk-model" {
		t.Error("custody locked the worker out of its own model credential — the denylist must be exact, never a prefix")
	}
}

// Custody must not depend on which prefix happened to match first.
func TestCollectAuthEnv_DenialWinsOverEveryPrefix(t *testing.T) {
	t.Setenv("CORVEX_TOKEN", "value")
	env := collectAuthEnv([]string{"CORVEX_", "CORVEX_TOKEN", ""}, []string{"CORVEX_TOKEN"})
	if _, leaked := env["CORVEX_TOKEN"]; leaked {
		t.Error("a longer prefix let a held credential through")
	}
}

// The tool catalogue is a boundary only if the raw path is closed: an agent that
// finds the typed tool inconvenient otherwise just shells out.
func TestWorker_ConfigCanCloseToolsOnTopOfTheBuiltInBlock(t *testing.T) {
	w := NewWorker(&mockProvider{}, "sonnet", "/tmp", nil, nil, nil, config.SecurityConfig{
		DisallowedTools: []string{"WebFetch", "Bash(curl:*)"},
	})

	req := w.buildRequest(&types.Task{ID: "S01", Title: "t"}, "", nil, "", "", GateAnswer{})
	joined := strings.Join(req.DisallowedTools, " ")
	for _, want := range []string{"WebFetch", "Bash(curl:*)", "Write(.corvex/**)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("disallowed tools lost %q: %v", want, req.DisallowedTools)
		}
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The test the first version of custody did NOT have, and whose absence an
// audit found: `collectAuthEnv` is a pure map filter, and filtering the
// forwarded set proves nothing about what the CHILD inherits. Every sandbox and
// the direct exec path start from os.Environ(), so a credential that was merely
// "not forwarded" was inherited anyway — custody was inert on the default
// configuration while three separate comments said it was not.
//
// This test runs a real process through the real sandbox and reads its
// environment back.
func TestCustody_HeldCredentialNeverReachesTheChildProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh to dump the environment")
	}
	t.Setenv("CORVEX_HELD_SECRET", "sk-ant-CANARY-CUSTODY")
	t.Setenv("CORVEX_FORWARDED", "fine")

	dump := filepath.Join(t.TempDir(), "env.txt")
	sb := sandbox.NewLocalSandbox(config.SandboxConfig{WorkDir: t.TempDir()})
	if err := sb.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if _, err := sb.Run(context.Background(), sandbox.RunRequest{
		Command: []string{"/bin/sh", "-c", "env > " + dump},
		Env:     map[string]string{"CORVEX_HELD_SECRET": "handed-back-explicitly"},
		DenyEnv: []string{"CORVEX_HELD_SECRET"},
	}); err != nil {
		t.Fatalf("sandbox run: %v", err)
	}

	body, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("reading the child's environment: %v", err)
	}
	got := string(body)
	if strings.Contains(got, "CANARY-CUSTODY") {
		t.Error("the held credential was inherited from the host environment")
	}
	if strings.Contains(got, "handed-back-explicitly") {
		t.Error("deny lost to an explicit Env entry — custody must win over both sources")
	}
	if !strings.Contains(got, "CORVEX_FORWARDED=fine") {
		t.Error("custody removed a variable nobody asked it to remove")
	}
}

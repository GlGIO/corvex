package cmd

// Shared plumbing for the `validate` characterization goldens.
//
// Everything here is prefixed `validate` (or lives on TestValidateHelper*)
// because several agents write goldens into this one package and a bare helper
// name would collide at merge time. The harness in characterize_test.go stays
// untouched: this file only ADDS what the validate group needs and reuses
// writeStdinFile/streamBlock/goldenAssert/scrub from there.
//
// Why a second capture helper at all: runCLIStdin only wraps rootCmd.Execute,
// but the biggest block validate.go owns (setupValidationStack, startApp,
// startDBContainer, waitForHealth, the wizard prompt functions) is reachable
// as plain Go functions that print with fmt.Printf and charmlog. Driving them
// directly is the only way to characterize the stack paths without a real
// database, a real app and a real 60s poll.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	charmlog "github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/stack"
	"github.com/muesli/termenv"
	"gopkg.in/yaml.v3"
)

// ── capture ──────────────────────────────────────────────────────────────────

// validateCapture swaps the process stdout/stderr/stdin (and charmlog's
// writer, which captured the ORIGINAL os.Stderr when it was constructed and
// does not follow reassignment) for the duration of fn, then returns whatever
// fn's code printed.
//
// fn receives a *bufio.Reader over stdinContent, which is exactly what
// runValidate hands to the wizard functions.
//
// HARD RULE: any child process started inside fn must be dead before fn
// returns. The child inherits the write end of the stdout pipe, so a survivor
// keeps io.Copy blocked and the helper hangs instead of returning.
func validateCapture(t *testing.T, stdinContent string, fn func(reader *bufio.Reader)) (stdout, stderr string) {
	t.Helper()

	// rootCmd's PersistentPreRun normally does this when NO_COLOR is set; a
	// direct call never goes through cobra, so force ASCII here or the goldens
	// depend on whether a CLI test ran first. Both calls are process-global and
	// irreversible — which is what we want inside a test binary.
	lipgloss.SetColorProfile(termenv.Ascii)
	charmlog.SetColorProfile(termenv.Ascii)

	stdinFile := writeStdinFile(t, stdinContent)
	origStdin := os.Stdin
	os.Stdin = stdinFile
	defer func() {
		os.Stdin = origStdin
		_ = stdinFile.Close()
	}()

	origStdout, origStderr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	charmlog.SetOutput(errW)

	var wg sync.WaitGroup
	var outBuf, errBuf bytes.Buffer
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&outBuf, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&errBuf, errR) }()

	// Filled in the defer so output survives a panic inside fn.
	defer func() {
		_ = outW.Close()
		_ = errW.Close()
		wg.Wait()
		_ = outR.Close()
		_ = errR.Close()
		os.Stdout, os.Stderr = origStdout, origStderr
		charmlog.SetOutput(origStderr)
		stdout, stderr = outBuf.String(), errBuf.String()
	}()

	fn(bufio.NewReader(os.Stdin))
	return
}

// validateStreams is what cmd/ hands internal/stack: the process stdout/stderr,
// read at CALL time. Inside validateCapture those are the capture pipes, which
// is exactly what the stack used to pick up from os.Stdout directly.
func validateStreams() stack.Streams {
	return stack.Streams{Out: os.Stdout, Err: os.Stderr}
}

// validateTranscript is transcript() for a direct function call: same
// stdout/stderr/error layout, with a label instead of an argv line so the
// golden says which function produced it.
func validateTranscript(label, stdout, stderr string, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", label)
	b.WriteString("\n--- stdout ---\n")
	b.WriteString(streamBlock(stdout))
	b.WriteString("\n--- stderr ---\n")
	b.WriteString(streamBlock(stderr))
	b.WriteString("\n--- error ---\n")
	if err == nil {
		b.WriteString("(nil)\n")
	} else {
		fmt.Fprintf(&b, "%s\n", err)
	}
	return b.String()
}

// validateDumpConfig renders a ValidateConfig as YAML so a golden can lock the
// wizard's EFFECT (which fields ended up set) next to its printed prompts.
func validateDumpConfig(t *testing.T, v config.ValidateConfig) string {
	t.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling validate config: %v", err)
	}
	return "--- validate config ---\n" + string(data)
}

// ── ports ────────────────────────────────────────────────────────────────────

// validateFreePort returns a TCP port that was free a moment ago: bind :0, read
// the assigned port, release it. Inherently a small race, which is why every
// golden that carries a port runs it through validateScrubPort.
func validateFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing reserved port: %v", err)
	}
	return port
}

// validateScrubPort replaces one concrete port number with <PORT>. Ports are
// allocated by the kernel, so they can never appear literally in a golden.
func validateScrubPort(s string, port int) string {
	return strings.ReplaceAll(s, strconv.Itoa(port), "<PORT>")
}

// validatePortOpen reports whether something accepts connections on port.
// Used to assert that the app really came up and that cleanup really killed it
// (portInUse is the production helper and is characterized separately).
func validatePortOpen(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// validateWaitPortClosed polls until nothing is listening on port, up to 3s.
func validateWaitPortClosed(port int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !validatePortOpen(port) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// validatePIDAlive reports whether pid still exists (signal 0 probe).
func validatePIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// ── fake claude ──────────────────────────────────────────────────────────────

// validateStubClaudeNDJSON installs a fake `claude` binary that replays a fixed
// stream-json transcript: one tool_use event (so the live progress writer's
// "  · Read package.json" line is characterized too), one assistant text event
// carrying `text`, then one result line carrying cost/tokens/duration. This is
// what makes the AI branch of the wizard (inferValidateConfig →
// Configurer.InferValidate) reachable and deterministic without spending a cent.
//
// Cost and duration come from the stub, so goldens can keep them visible with
// scrubExcept(s, "cost", "dur") instead of blanking them.
func validateStubClaudeNDJSON(t *testing.T, text string, costUSD float64, durationMs int64) {
	t.Helper()

	lines := []map[string]any{
		{
			"type": "assistant",
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "toolu_stub",
						"name":  "Read",
						"input": map[string]any{"file_path": "package.json"},
					},
				},
			},
		},
		{
			"type": "assistant",
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "text", "text": text},
				},
			},
		},
		{
			"type":                "result",
			"subtype":             "success",
			"result":              "ok",
			"total_cost_usd":      costUSD,
			"total_input_tokens":  1200,
			"total_output_tokens": 340,
			"duration_ms":         durationMs,
		},
	}

	var body strings.Builder
	for _, l := range lines {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("marshaling stub ndjson: %v", err)
		}
		body.Write(raw)
		body.WriteString("\n")
	}

	path := filepath.Join(t.TempDir(), "claude-stream.ndjson")
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatalf("writing stub ndjson: %v", err)
	}
	stubClaude(t, "cat '"+path+"'")
}

// validateSortDockerEnv sorts the `-e KEY=VALUE` pairs of a recorded docker
// argv line, in place. Mandatory whenever cfg.Database.Env has more than one
// entry: it is a Go map, so startDBContainer emits the -e flags in random
// order and a raw golden would flake roughly every other run.
func validateSortDockerEnv(log string) string {
	lines := strings.Split(log, "\n")
	for i, line := range lines {
		toks := strings.Fields(line)
		var envs, out []string
		placed := false
		for j := 0; j < len(toks); j++ {
			if toks[j] == "-e" && j+1 < len(toks) {
				envs = append(envs, toks[j+1])
				if !placed {
					out = append(out, "\x00")
					placed = true
				}
				j++
				continue
			}
			out = append(out, toks[j])
		}
		if len(envs) < 2 {
			continue
		}
		sort.Strings(envs)
		var rep []string
		for _, e := range envs {
			rep = append(rep, "-e", e)
		}
		lines[i] = strings.Replace(strings.Join(out, " "), "\x00", strings.Join(rep, " "), 1)
	}
	return strings.Join(lines, "\n")
}

// validateConfigBlock wraps a config JSON payload in the ```config fence that
// parseConfigurerOutput looks for.
func validateConfigBlock(jsonBody string) string {
	return "Inspected the repo.\n\n```config\n" + jsonBody + "\n```\n"
}

// ── the "application" ────────────────────────────────────────────────────────

const (
	validateHelperEnv     = "CORVEX_CHAR_VALIDATE_HELPER"
	validateHelperPortEnv = "CORVEX_CHAR_VALIDATE_PORT"
)

// TestValidateHelperAppServer is not a test. It is the application that
// setupValidationStack starts: re-executing this test binary with -test.run is
// the only way to give the stack a real HTTP server to health-check without
// depending on python/node/nc being installed on the machine.
//
// It stays silent (nothing on stdout) because its stdout is the parent test's
// captured pipe, and it is expected to be SIGKILLed by the stack's cleanup.
func TestValidateHelperAppServer(t *testing.T) {
	if os.Getenv(validateHelperEnv) != "1" {
		t.Skip("helper process only — re-executed by the validate characterization tests")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv(validateHelperPortEnv))
	if err != nil {
		os.Exit(9)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	_ = http.Serve(ln, mux)
	os.Exit(0)
}

// validateHelperStartCommand returns a stack.start_command that boots
// TestValidateHelperAppServer on port, and exports the env the helper reads.
// startApp splits the command with strings.Fields, so a path with a space
// would be shredded — skip rather than produce a confusing failure.
func validateHelperStartCommand(t *testing.T, port int) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate the test binary: %v", err)
	}
	if strings.ContainsAny(exe, " \t") {
		t.Skipf("test binary path %q contains whitespace; strings.Fields would split it", exe)
	}
	t.Setenv(validateHelperEnv, "1")
	t.Setenv(validateHelperPortEnv, strconv.Itoa(port))
	return exe + " -test.run=^TestValidateHelperAppServer$"
}

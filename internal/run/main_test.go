package run_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// ---------------------------------------------------------------------------
// Helper-process protocol
//
// Some claims in this package are only provable across a real process boundary
// ("a second process lists what is alive", "a SIGKILLed run is detected as
// dead"). A test that calls the function in its own process proves neither.
//
// The mechanism: the test binary re-executes itself with helperEnv set.
// TestMain sees that variable, does the helper's job, and exits before m.Run()
// — so no test runs in the child and the child produces no testing output. The
// result travels through a file named by helperOut, never through stdout, so
// nothing this package does prints anything.
// ---------------------------------------------------------------------------

const (
	helperEnv   = "CORVEX_RUN_TEST_HELPER"
	helperOut   = "CORVEX_RUN_TEST_HELPER_OUT"
	helperRepo  = "CORVEX_RUN_TEST_HELPER_REPO"
	helperCount = "CORVEX_RUN_TEST_HELPER_COUNT"
	helperBase  = "CORVEX_RUN_TEST_HELPER_BASE"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		os.Exit(helperMain(mode))
	}

	// HOME ISOLATION (LEI 4c), enforced twice.
	//
	// 1. CORVEX_HOME is pointed at a scratch directory before any test runs, so
	//    even a test that forgets to override it cannot reach the real
	//    ~/.corvex.
	// 2. The real ~/.corvex/runs.jsonl is fingerprinted before and after the
	//    suite. If the suite touched it, the run fails loudly instead of
	//    quietly polluting the user's machine.
	realIndex := realHomeIndexPath()
	before := fingerprint(realIndex)

	scratch, err := os.MkdirTemp("", "corvex-run-home-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create scratch home:", err)
		os.Exit(2)
	}
	if err := os.Setenv(run.HomeEnv, scratch); err != nil {
		fmt.Fprintln(os.Stderr, "cannot set", run.HomeEnv, err)
		os.Exit(2)
	}

	code := m.Run()
	_ = os.RemoveAll(scratch)

	if after := fingerprint(realIndex); after != before {
		fmt.Fprintf(os.Stderr, "FAIL: a test touched the real home index %s (%s -> %s)\n",
			realIndex, before, after)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func realHomeIndexPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".corvex", run.IndexFile)
}

func fingerprint(path string) string {
	if path == "" {
		return "no-home"
	}
	st, err := os.Stat(path)
	if err != nil {
		return "absent"
	}
	return fmt.Sprintf("size=%d mtime=%d", st.Size(), st.ModTime().UnixNano())
}

// TestHomeGuardIsNotVacuous proves the TestMain guard would actually notice a
// write to the real home index. It runs against a temp file, never the real
// one — the guard has to be verifiable without violating what it guards.
func TestHomeGuardIsNotVacuous(t *testing.T) {
	path := filepath.Join(t.TempDir(), run.IndexFile)
	if got := fingerprint(path); got != "absent" {
		t.Fatalf("fingerprint of a missing file = %q, want absent", got)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	before := fingerprint(path)
	if before == "absent" {
		t.Fatal("fingerprint still reports absent after the file was created")
	}
	if err := os.WriteFile(path, []byte("{}\n{}\n"), 0o600); err != nil {
		t.Fatalf("append: %v", err)
	}
	if after := fingerprint(path); after == before {
		t.Errorf("fingerprint unchanged (%s) after the file grew: the guard would miss a write", after)
	}
	if fingerprint("") != "no-home" {
		t.Error("fingerprint of an unresolvable home should say so")
	}

	// And the suite is in fact pointed away from the real home.
	home, err := run.Home()
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if real, err := os.UserHomeDir(); err == nil && home == filepath.Join(real, ".corvex") {
		t.Fatalf("the suite is using the real home %s", home)
	}
}

func helperMain(mode string) int {
	out := os.Getenv(helperOut)
	switch mode {
	case "start": // start a run, mark it done, exit
		id, err := helperStart(out, run.StatusDone)
		if err != nil {
			fmt.Fprintln(os.Stderr, "helper start:", err)
			return 2
		}
		_ = id
	case "hold": // start a run and block, so the parent can SIGKILL it
		if _, err := helperStart(out, ""); err != nil {
			fmt.Fprintln(os.Stderr, "helper hold:", err)
			return 2
		}
		time.Sleep(30 * time.Second) // upper bound; the parent kills it long before
	case "sleep": // a real process the parent can probe and kill
		time.Sleep(30 * time.Second)
	case "list": // read the global index as a foreign process
		views, err := run.Resolver{}.List()
		if err != nil {
			fmt.Fprintln(os.Stderr, "helper list:", err)
			return 2
		}
		if err := writeJSON(out, views); err != nil {
			fmt.Fprintln(os.Stderr, "helper list write:", err)
			return 2
		}
	case "append": // hammer the global index from a separate process
		if err := helperAppend(); err != nil {
			fmt.Fprintln(os.Stderr, "helper append:", err)
			return 2
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown helper mode:", mode)
		return 2
	}
	return 0
}

func helperStart(out string, final run.Status) (string, error) {
	h, err := run.Registry{Repo: os.Getenv(helperRepo)}.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		return "", err
	}
	if err := writeJSON(out, h.Record()); err != nil {
		return "", err
	}
	if final != "" {
		if err := h.SetStatus(final); err != nil {
			return "", err
		}
	}
	return h.RunID(), nil
}

func helperAppend() error {
	home, err := run.Home()
	if err != nil {
		return err
	}
	var count, base int
	if _, err := fmt.Sscanf(os.Getenv(helperCount), "%d", &count); err != nil {
		return fmt.Errorf("count: %w", err)
	}
	if _, err := fmt.Sscanf(os.Getenv(helperBase), "%d", &base); err != nil {
		return fmt.Errorf("base: %w", err)
	}
	for i := 0; i < count; i++ {
		rec := run.Record{
			RunID:     fmt.Sprintf("run_%04x", base+i),
			Repo:      "/repo/concurrent",
			Status:    run.StatusRunning,
			PID:       os.Getpid(),
			StartedAt: time.Now().UTC(),
		}
		if err := run.AppendIndex(home, rec, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	if path == "" {
		return fmt.Errorf("no %s set", helperOut)
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

// helperCmd builds a command re-executing the test binary in helper mode.
// Output goes to a file rather than a pipe: an orphaned child holding the
// parent's stdout can hang `go test`.
func helperCmd(t *testing.T, mode string, env ...string) (*exec.Cmd, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	out := filepath.Join(t.TempDir(), "helper-"+mode+".json")
	logPath := out + ".log"
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("helper log: %v", err)
	}
	t.Cleanup(func() {
		logFile.Close()
		if data, err := os.ReadFile(logPath); err == nil && len(data) > 0 {
			t.Logf("helper %s output: %s", mode, data)
		}
	})

	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), helperEnv+"="+mode, helperOut+"="+out)
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	return cmd, out
}

// runHelper runs a helper to completion and decodes its result file.
func runHelper(t *testing.T, mode string, dst any, env ...string) {
	t.Helper()
	cmd, out := helperCmd(t, mode, env...)
	if err := cmd.Run(); err != nil {
		t.Fatalf("helper %s: %v", mode, err)
	}
	if dst == nil {
		return
	}
	decodeFile(t, out, dst)
}

func decodeFile(t *testing.T, path string, dst any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading helper result %s: %v", path, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("decoding helper result %s: %v (%s)", path, err, data)
	}
}

// waitForFile polls until path exists, so tests synchronise on an observable
// fact instead of on a fixed sleep.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

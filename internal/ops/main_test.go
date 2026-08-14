package ops

// HOME ISOLATION FOR internal/ops (LEI 4c).
//
// ops.NewRunner registers run identity, which appends to the global index at
// `$CORVEX_HOME/runs.jsonl` — `~/.corvex/runs.jsonl` by default. The tests in
// this package inject a scratch home explicitly; this is the belt to that
// braces, so a test added later that forgets still cannot reach the real home,
// and the real index is fingerprinted to prove it.
//
// Same guard as internal/run/main_test.go and cmd/main_test.go: one shape to
// learn, one failure mode.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/run"
)

func TestMain(m *testing.M) {
	realIndex := ""
	if h, err := os.UserHomeDir(); err == nil {
		realIndex = filepath.Join(h, ".corvex", run.IndexFile)
	}
	before := homeFingerprint(realIndex)

	scratch, err := os.MkdirTemp("", "corvex-ops-home-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create scratch corvex home:", err)
		os.Exit(2)
	}
	if err := os.Setenv(run.HomeEnv, scratch); err != nil {
		fmt.Fprintln(os.Stderr, "cannot set", run.HomeEnv, err)
		os.Exit(2)
	}

	code := m.Run()
	_ = os.RemoveAll(scratch)

	if after := homeFingerprint(realIndex); after != before {
		fmt.Fprintf(os.Stderr, "FAIL: an ops test touched the real home index %s (%s -> %s)\n",
			realIndex, before, after)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func homeFingerprint(path string) string {
	if path == "" {
		return "no-home"
	}
	st, err := os.Stat(path)
	if err != nil {
		return "absent"
	}
	return fmt.Sprintf("size=%d mtime=%d", st.Size(), st.ModTime().UnixNano())
}

// TestOpsHomeIsIsolated proves the override took effect, so the guard above is
// not vacuous.
func TestOpsHomeIsIsolated(t *testing.T) {
	home, err := run.Home()
	if err != nil {
		t.Fatalf("run.Home: %v", err)
	}
	if real, err := os.UserHomeDir(); err == nil && home == filepath.Join(real, ".corvex") {
		t.Fatalf("ops suite is using the real corvex home %s", home)
	}
}

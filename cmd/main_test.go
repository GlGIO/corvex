package cmd

// HOME ISOLATION FOR THE WHOLE cmd/ SUITE.
//
// From F1 on, `corvex run` registers run identity: a record under
// `<repo>/.corvex/runs/` and a line in the global index at `$CORVEX_HOME/runs.jsonl`,
// which defaults to `~/.corvex/runs.jsonl`. Every characterization test that
// invokes `run` therefore has a write path out of its temp fixture and into the
// developer's real home. A test suite that pollutes `~/.corvex` is a product
// defect, not an inconvenience, so this is enforced twice:
//
//  1. CORVEX_HOME points at a scratch directory before any test runs, so a test
//     that forgets to override it still cannot reach the real home. Set here
//     rather than in the harness because tests reach production code by paths
//     other than runCLI*.
//  2. The real `~/.corvex/runs.jsonl` is fingerprinted before and after the
//     suite. If it moved, the run fails loudly instead of quietly leaving state
//     on the machine.
//
// The mechanism is copied deliberately from internal/run/main_test.go — same
// guard, same failure mode, so there is one shape to learn.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/run"
)

func TestMain(m *testing.M) {
	realIndex := realHomeIndexPath()
	before := fingerprintPath(realIndex)

	scratch, err := os.MkdirTemp("", "corvex-cmd-home-*")
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

	if after := fingerprintPath(realIndex); after != before {
		fmt.Fprintf(os.Stderr, "FAIL: a cmd/ test touched the real home index %s (%s -> %s)\n",
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

func fingerprintPath(path string) string {
	if path == "" {
		return "no-home"
	}
	st, err := os.Stat(path)
	if err != nil {
		return "absent"
	}
	return fmt.Sprintf("size=%d mtime=%d", st.Size(), st.ModTime().UnixNano())
}

// TestHomeIsolationIsNotVacuous proves the isolation is real: the suite resolves
// a corvex home that is not the user's, and the fingerprint the guard relies on
// actually changes when a file grows. The second half runs against a temp file —
// a guard has to be verifiable without violating what it guards.
func TestHomeIsolationIsNotVacuous(t *testing.T) {
	home, err := run.Home()
	if err != nil {
		t.Fatalf("run.Home: %v", err)
	}
	if real, err := os.UserHomeDir(); err == nil && home == filepath.Join(real, ".corvex") {
		t.Fatalf("cmd/ suite is using the real corvex home %s", home)
	}

	path := filepath.Join(t.TempDir(), run.IndexFile)
	if got := fingerprintPath(path); got != "absent" {
		t.Fatalf("fingerprint of a missing file = %q, want absent", got)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	first := fingerprintPath(path)
	if err := os.WriteFile(path, []byte("{}\n{}\n"), 0o600); err != nil {
		t.Fatalf("append: %v", err)
	}
	if second := fingerprintPath(path); second == first {
		t.Errorf("fingerprint unchanged (%s) after the file grew: the guard would miss a write", second)
	}
}

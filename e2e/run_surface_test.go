package e2e

// The F4 acceptance criterion: every command of the new surface exists and
// answers in a real terminal, before there is any UI to call it.
//
// These have to exec the binary. The whole point of the surface is that a run
// started by one process is addressable by another — `run list` and `run show`
// read the global index precisely so a second process can answer — and a
// function call in the test's own process proves none of that.

import (
	"os/exec"
	"strings"
	"testing"
)

// corvexCLI runs one invocation of the real binary in dir.
func corvexCLI(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestRunSurface_AnotherProcessListsAndShowsARun is invariant 3 and the F4
// acceptance in one run: the LEGACY invocation (`corvex run demo`, spec.md path,
// no recipe anywhere) still works end to end, and the new read surface can then
// address what it left behind.
func TestRunSurface_AnotherProcessListsAndShowsARun(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	dir := setupIdentityRepo(t)
	cmd, logOf := corvexRun(t, dir, stubClaudeBin(t, identityStubPass))
	if err := cmd.Run(); err != nil {
		t.Fatalf("legacy `corvex run demo` failed: %v\n%s", err, logOf())
	}

	list, err := corvexCLI(t, dir, "run", "list")
	if err != nil {
		t.Fatalf("run list: %v\n%s", err, list)
	}
	id := firstRunID(t, list)

	show, err := corvexCLI(t, dir, "run", "show", id)
	if err != nil {
		t.Fatalf("run show %s: %v\n%s", id, err, show)
	}
	for _, want := range []string{id, "S01", "1/1 steps"} {
		if !strings.Contains(show, want) {
			t.Errorf("run show is missing %q:\n%s", want, show)
		}
	}

	step, err := corvexCLI(t, dir, "run", "show", id, "--step", "S01")
	if err != nil {
		t.Fatalf("run show --step: %v\n%s", err, step)
	}
	if !strings.Contains(step, "Events:") {
		t.Errorf("--step showed no event stream:\n%s", step)
	}

	// Addressable from a directory that is not the repository — the property the
	// global index exists for.
	elsewhere, err := corvexCLI(t, t.TempDir(), "run", "show", id)
	if err != nil {
		t.Fatalf("run show from another directory: %v\n%s", err, elsewhere)
	}
	if !strings.Contains(elsewhere, "S01") {
		t.Errorf("a run was not addressable from outside its repository:\n%s", elsewhere)
	}
}

// The legacy commands keep working AND keep quiet: under a pipe they must print
// exactly what they always printed, because that is what keeps the F-1 golden
// network a valid oracle through the rename.
func TestRunSurface_LegacyCommandsStayQuietUnderAPipe(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	dir := setupIdentityRepo(t)
	for _, args := range [][]string{{"status", "demo"}, {"list"}, {"inspect", "demo"}} {
		out, err := corvexCLI(t, dir, args...)
		if err != nil {
			t.Fatalf("corvex %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		if strings.Contains(out, "deprecated") {
			t.Errorf("corvex %s printed a deprecation notice into a pipe:\n%s", strings.Join(args, " "), out)
		}
	}
}

// `corvex` with no argument answers with state, and the state it answers with is
// the run that just happened.
func TestRunSurface_BareCorvexAnswersWithState(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	dir := setupIdentityRepo(t)
	cmd, logOf := corvexRun(t, dir, stubClaudeBin(t, identityStubPass))
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, logOf())
	}

	out, err := corvexCLI(t, dir)
	if err != nil {
		t.Fatalf("bare corvex: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Last 7 day(s):") || !strings.Contains(out, "→ corvex run show run_") {
		t.Errorf("bare corvex did not answer with state:\n%s", out)
	}
}

// firstRunID pulls a run id out of a listing.
func firstRunID(t *testing.T, listing string) string {
	t.Helper()
	for _, field := range strings.Fields(listing) {
		if strings.HasPrefix(field, "run_") {
			return field
		}
	}
	t.Fatalf("no run id in:\n%s", listing)
	return ""
}

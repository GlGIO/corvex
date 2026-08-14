package run_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/run"
)

func TestDefaultIDShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err := run.DefaultIDFunc()
		if err != nil {
			t.Fatalf("DefaultIDFunc: %v", err)
		}
		if !strings.HasPrefix(id, run.IDPrefix) {
			t.Fatalf("id %q lacks prefix %q", id, run.IDPrefix)
		}
		if got, want := len(id)-len(run.IDPrefix), run.IDHexDigits; got != want {
			t.Fatalf("id %q: %d hex digits, want %d", id, got, want)
		}
		if !run.ValidID(id) {
			t.Fatalf("id %q rejected by ValidID", id)
		}
		seen[id] = true
	}
	if len(seen) < 32 {
		t.Errorf("64 draws produced only %d distinct ids; the generator is barely random", len(seen))
	}
}

func TestHexIDIsInjectableAndDeterministic(t *testing.T) {
	// The whole reason IDFunc exists: a test pins the id instead of scrubbing it.
	gen := run.HexID(bytes.NewReader([]byte{0x8f, 0x21}), 4)
	id, err := gen()
	if err != nil {
		t.Fatalf("HexID: %v", err)
	}
	if id != "run_8f21" {
		t.Errorf("id = %q, want run_8f21", id)
	}

	if _, err := run.HexID(bytes.NewReader(nil), 4)(); err == nil {
		t.Error("expected an error when the entropy source is empty")
	}
	if _, err := run.HexID(bytes.NewReader([]byte{1, 2}), 0)(); err == nil {
		t.Error("expected an error for zero digits")
	}
	// An odd width still yields exactly that many digits.
	odd, err := run.HexID(bytes.NewReader([]byte{0xab, 0xcd}), 3)()
	if err != nil {
		t.Fatalf("odd HexID: %v", err)
	}
	if odd != "run_abc" {
		t.Errorf("odd id = %q, want run_abc", odd)
	}
}

func TestValidID(t *testing.T) {
	valid := []string{"run_0", "run_8f21", "run_deadbeef"}
	invalid := []string{
		"", "run_", "run", "8f21", "Run_8f21",
		"run_8F21",      // uppercase hex would make two ids for one run
		"run_../escape", // path traversal: RecordPath must refuse it
		"run_a/b", "run_g123", "run_8f21 ", " run_8f21",
	}
	for _, s := range valid {
		if !run.ValidID(s) {
			t.Errorf("ValidID(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if run.ValidID(s) {
			t.Errorf("ValidID(%q) = true, want false", s)
		}
	}
}

// TestColliedIDIsRedrawn covers the reason a 4-digit id is acceptable: the
// generator is paired with a uniqueness oracle, so a collision costs a redraw
// instead of merging two runs.
func TestCollidedIDIsRedrawn(t *testing.T) {
	t.Setenv(run.HomeEnv, t.TempDir())
	repo := t.TempDir()
	ids := []string{"run_aaaa", "run_aaaa", "run_bbbb"}
	i := 0
	gen := func() (string, error) {
		id := ids[i]
		i++
		return id, nil
	}
	reg := run.Registry{Repo: repo, NewID: gen}

	first, err := reg.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	second, err := reg.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if first.RunID() != "run_aaaa" || second.RunID() != "run_bbbb" {
		t.Fatalf("ids = %q, %q; want run_aaaa, run_bbbb (the collision must be redrawn)",
			first.RunID(), second.RunID())
	}
}

// TestIDsAreUniqueAcrossRepos: the UI addresses a run by bare id, so an id
// taken in one repository must not be handed out in another — otherwise the two
// runs collapse into a single row of the global index and one disappears.
func TestIDsAreUniqueAcrossRepos(t *testing.T) {
	home := t.TempDir()
	ids := []string{"run_aaaa", "run_aaaa", "run_bbbb"}
	i := 0
	gen := func() (string, error) {
		id := ids[i]
		i++
		return id, nil
	}

	first, err := run.Registry{Repo: t.TempDir(), Home: home, NewID: gen}.Start(run.StartOptions{Recipe: "a"})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	second, err := run.Registry{Repo: t.TempDir(), Home: home, NewID: gen}.Start(run.StartOptions{Recipe: "b"})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if first.RunID() == second.RunID() {
		t.Fatalf("two repositories both minted %q", first.RunID())
	}

	views, err := run.Resolver{Home: home, Alive: alwaysAlive}.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("List = %d runs, want 2 — a duplicated id swallowed one", len(views))
	}
}

// TestClaimedIDIsReleasedWhenStartFails: Start either registers a run or leaves
// nothing behind. The failure has to land *after* the record was written, which
// is what a read-only home does: the index is missing (so nothing blocks the id
// claim) and unwritable (so the announcement fails).
func TestClaimedIDIsReleasedWhenStartFails(t *testing.T) {
	requireUnprivileged(t)
	repo := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })

	_, err := run.Registry{Repo: repo, Home: home,
		NewID: func() (string, error) { return "run_8f21", nil }}.Start(run.StartOptions{Recipe: "ship"})
	if err == nil {
		t.Fatal("Start succeeded even though the run could not be announced")
	}
	if !strings.Contains(err.Error(), "run index") {
		t.Fatalf("error = %v, want the failure to come from announcing the run", err)
	}
	if _, err := run.ReadRecord(repo, "run_8f21"); err == nil {
		t.Error("a record survived a failed Start")
	}
	recs, err := run.ReadRecords(repo)
	if err != nil {
		t.Fatalf("ReadRecords: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("ReadRecords = %+v, want none", recs)
	}
}

// TestLeftoverClaimIsNotHandedOutAgain: a record file that the global index
// never heard about — a claim left by a Start that died mid-way, or a record
// from before the index existed — must still block its id. This is the O_EXCL
// half of the oracle, the half that survives a missing index.
func TestLeftoverClaimIsNotHandedOutAgain(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.MkdirAll(run.RecordsDir(repo), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	leftover := filepath.Join(run.RecordsDir(repo), "run_aaaa.json")
	if err := os.WriteFile(leftover, nil, 0o644); err != nil {
		t.Fatalf("write leftover claim: %v", err)
	}

	ids := []string{"run_aaaa", "run_bbbb"}
	i := 0
	h, err := run.Registry{Repo: repo, Home: home, NewID: func() (string, error) {
		id := ids[i]
		i++
		return id, nil
	}}.Start(run.StartOptions{Recipe: "ship"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.RunID() != "run_bbbb" {
		t.Errorf("run id = %q, want run_bbbb; the leftover claim was overwritten", h.RunID())
	}
	if data, err := os.ReadFile(leftover); err != nil || len(data) != 0 {
		t.Errorf("leftover claim was modified: %q, %v", data, err)
	}
}

func TestExhaustedGeneratorFailsInsteadOfLooping(t *testing.T) {
	t.Setenv(run.HomeEnv, t.TempDir())
	repo := t.TempDir()
	stuck := run.Registry{Repo: repo, NewID: func() (string, error) { return "run_cccc", nil }}
	if _, err := stuck.Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	_, err := stuck.Start(run.StartOptions{Project: "demo"})
	if err == nil {
		t.Fatal("expected an error from a generator that can only produce a taken id")
	}
	if !strings.Contains(err.Error(), "attempts exhausted") {
		t.Errorf("error = %v, want it to mention exhausted attempts", err)
	}
}

func TestGeneratorErrorsPropagate(t *testing.T) {
	t.Setenv(run.HomeEnv, t.TempDir())
	boom := errors.New("no entropy")
	reg := run.Registry{Repo: t.TempDir(), NewID: func() (string, error) { return "", boom }}
	if _, err := reg.Start(run.StartOptions{}); !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to wrap %v", err, boom)
	}

	bad := run.Registry{Repo: t.TempDir(), NewID: func() (string, error) { return "nope", nil }}
	if _, err := bad.Start(run.StartOptions{}); err == nil {
		t.Error("expected an error for a generator producing a malformed id")
	}
}

package ops

// Tests for run identity: where it is minted, and how the run's end reaches disk.
//
// Every source of non-determinism is injected — the id generator, the clock, the
// corvex home — so these tests assert on concrete values (`run_8f21`) instead of
// on shapes. Nothing here touches the real `~/.corvex`.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/run"
)

// goroutineCount settles the scheduler first, so a goroutine that is on its way
// out is not miscounted as a leak.
func goroutineCount() int {
	for i := 0; i < 20; i++ {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	return runtime.NumGoroutine()
}

// fixedID returns an IDFunc handing out ids from a list, then failing. A test
// that draws more ids than it declared should say so rather than surprise the
// reader with a random one.
func fixedID(ids ...string) run.IDFunc {
	i := 0
	return func() (string, error) {
		if i >= len(ids) {
			return "", fmt.Errorf("fixedID: only %d id(s) declared", len(ids))
		}
		id := ids[i]
		i++
		return id, nil
	}
}

// identityRequest builds a RunRequest whose identity is fully pinned: scratch
// home, fixed id, frozen clock, fixed pid and host.
func identityRequest(t *testing.T, repo, project string, ids ...string) RunRequest {
	t.Helper()
	if len(ids) == 0 {
		ids = []string{"run_8f21"}
	}
	return RunRequest{
		Config:  config.Default(),
		Project: project,
		WorkDir: repo,
		Repo:    repo,
		Registry: &run.Registry{
			Home:  t.TempDir(),
			NewID: fixedID(ids...),
			Now:   func() time.Time { return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC) },
			PID:   os.Getpid(),
			Host:  "test-host",
		},
	}
}

func TestNewRunnerMintsOneIdentityAndHandsItDown(t *testing.T) {
	repo := t.TempDir()
	r, err := NewRunner(identityRequest(t, repo, "alpha"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if r.RunID != "run_8f21" {
		t.Errorf("RunID = %q, want run_8f21 (the injected generator)", r.RunID)
	}
	if r.Repo != repo {
		t.Errorf("Repo = %q, want %q", r.Repo, repo)
	}
	if r.Project != "alpha" {
		t.Errorf("Project = %q, want alpha", r.Project)
	}

	// The same id the record carries is the id the ledger will stamp: one mint,
	// handed down by value, no second source.
	id := r.Identity()
	if id.RunID != r.RunID {
		t.Errorf("Identity() = %+v, want run %s", id, r.RunID)
	}
	// The repo path is known here and deliberately withheld from the ledger,
	// which is the one artifact that reaches the user's commits. The Runner keeps
	// it (the record and the index need it); the ledger identity must not.
	if strings.Contains(fmt.Sprintf("%+v", id), repo) {
		t.Errorf("Identity() carries the absolute repo path %q: %+v", repo, id)
	}

	rec, err := run.ReadRecord(repo, "run_8f21")
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if rec.Project != "alpha" || rec.Status != run.StatusRunning {
		t.Errorf("record = %+v, want project alpha status running", rec)
	}
}

// The legacy `corvex run <project>` path has no recipe, and F1 does not invent
// one. This is the decision under test, in both places identity is observable.
func TestLegacyRunRecordsNoRecipe(t *testing.T) {
	repo := t.TempDir()
	r, err := NewRunner(identityRequest(t, repo, "alpha"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if got := r.Identity().Recipe; got != "" {
		t.Errorf("ledger identity recipe = %q, want empty on the legacy path", got)
	}
	if got := r.Record().Recipe; got != "" {
		t.Errorf("record recipe = %q, want empty on the legacy path", got)
	}
	if got := r.Record().Project; got != "alpha" {
		t.Errorf("record project = %q, want alpha — the project is where the legacy path is named", got)
	}
	// And the field is absent from the serialised record, not present-and-empty:
	// a reader asking "which runs used no recipe" gets a clean answer.
	raw, err := os.ReadFile(filepath.Join(run.RecordsDir(repo), "run_8f21.json"))
	if err != nil {
		t.Fatalf("reading record: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("parsing record: %v", err)
	}
	if _, ok := fields["recipe"]; ok {
		t.Errorf("record JSON carries a recipe key: %s", raw)
	}
}

func TestExecuteRecordsTheWayTheRunEnded(t *testing.T) {
	boom := errors.New("task S01 failed")

	cases := []struct {
		name    string
		body    func(context.Context) error
		cancel  bool
		want    run.Status
		wantErr error
	}{
		{"success is done", func(context.Context) error { return nil }, false, run.StatusDone, nil},
		{"failure is failed", func(context.Context) error { return boom }, false, run.StatusFailed, boom},
		// Ctrl-C: the context is cancelled and the orchestrator returns whatever
		// the interrupted step produced. Recording that as `failed` would blame
		// the work for the operator's interrupt.
		{"cancelled context wins over the error", func(context.Context) error { return boom }, true, run.StatusCanceled, boom},
		{"cancelled context with no error", func(context.Context) error { return nil }, true, run.StatusCanceled, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			req := identityRequest(t, repo, "alpha")
			r, err := NewRunner(req)
			if err != nil {
				t.Fatalf("NewRunner: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			if got := r.Execute(ctx, tc.body); !errors.Is(got, tc.wantErr) {
				t.Fatalf("Execute error = %v, want %v", got, tc.wantErr)
			}
			if r.StatusErr != nil {
				t.Fatalf("StatusErr: %v", r.StatusErr)
			}

			rec, err := run.ReadRecord(repo, r.RunID)
			if err != nil {
				t.Fatalf("ReadRecord: %v", err)
			}
			if rec.Status != tc.want {
				t.Errorf("record status = %q, want %q", rec.Status, tc.want)
			}
			// And a second process reading the global index sees the same end.
			views, err := run.Resolver{Home: req.Registry.Home}.List()
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(views) != 1 {
				t.Fatalf("index holds %d run(s), want 1", len(views))
			}
			if views[0].Record.Status != tc.want {
				t.Errorf("index status = %q, want %q", views[0].Record.Status, tc.want)
			}
			if views[0].Liveness != run.LivenessFinished {
				t.Errorf("liveness = %q, want finished — a closed run must not read as alive", views[0].Liveness)
			}
		})
	}
}

// A run that ends must not have its heartbeat outlive it: the goroutine has to
// be gone by the time Execute returns, and nothing may keep rewriting the record
// afterwards.
//
// What this test does NOT prove, stated so nobody reads more into it: that Stop
// runs *before* SetStatus rather than just before Execute returns. Measured —
// moving the Stop after the status write keeps this test green, because the
// window a beat would have to hit is nanoseconds wide. The ordering is still the
// right code (a beat landing after `done` would bump updated_at on a finished
// run) but its only consequence is a slightly dirty timestamp: terminal status
// wins in liveness either way. The leak, which is the part that actually breaks
// things, IS caught — verified by deleting the Stop and watching this fail.
func TestExecuteLeavesNoHeartbeatBehind(t *testing.T) {
	repo := t.TempDir()
	req := identityRequest(t, repo, "alpha")
	req.HeartbeatInterval = time.Millisecond // fast enough to beat during body
	r, err := NewRunner(req)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	before := goroutineCount()
	if err := r.Execute(context.Background(), func(context.Context) error {
		// Long enough for several beats at 1ms; the assertion is about ordering,
		// not about the exact count.
		time.Sleep(30 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rec, err := run.ReadRecord(repo, r.RunID)
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if rec.Status != run.StatusDone {
		t.Fatalf("status = %q, want done", rec.Status)
	}

	// Re-read after a settle window: a heartbeat goroutine still alive would keep
	// rewriting the file, and at 1ms it would do so many times over.
	time.Sleep(20 * time.Millisecond)
	again, err := run.ReadRecord(repo, r.RunID)
	if err != nil {
		t.Fatalf("re-reading record: %v", err)
	}
	if again.Status != run.StatusDone {
		t.Errorf("status became %q after the run ended: a heartbeat outlived Execute", again.Status)
	}
	if leaked := goroutineCount() - before; leaked > 0 {
		t.Errorf("%d goroutine(s) leaked by Execute", leaked)
	}
}

// A panicking body must not leave `running` on disk for a process that is
// unwinding, and must not leave a live heartbeat behind either.
func TestExecuteRecordsFailedWhenTheBodyPanics(t *testing.T) {
	repo := t.TempDir()
	r, err := NewRunner(identityRequest(t, repo, "alpha"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not propagate; Execute must not swallow it")
			}
		}()
		_ = r.Execute(context.Background(), func(context.Context) error {
			panic("worker exploded")
		})
	}()

	rec, err := run.ReadRecord(repo, r.RunID)
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if rec.Status != run.StatusFailed {
		t.Errorf("status = %q, want failed after a panic", rec.Status)
	}
}

// Two runs of the same project must be separable — the F1 acceptance criterion,
// at the layer that mints the ids.
func TestTwoRunsOfTheSameProjectGetDistinctIdentities(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	var ids []string
	for _, id := range []string{"run_aaaa", "run_bbbb"} {
		req := identityRequest(t, repo, "alpha", id)
		req.Registry.Home = home
		r, err := NewRunner(req)
		if err != nil {
			t.Fatalf("NewRunner(%s): %v", id, err)
		}
		if err := r.Execute(context.Background(), func(context.Context) error { return nil }); err != nil {
			t.Fatalf("Execute(%s): %v", id, err)
		}
		ids = append(ids, r.RunID)
	}

	if ids[0] == ids[1] {
		t.Fatalf("both runs got id %q", ids[0])
	}
	views, err := run.Resolver{Home: home}.ListRepo(repo)
	if err != nil {
		t.Fatalf("ListRepo: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("repo holds %d run(s), want 2: %+v", len(views), views)
	}
	for _, v := range views {
		if v.Record.Project != "alpha" {
			t.Errorf("run %s project = %q, want alpha", v.Record.RunID, v.Record.Project)
		}
	}
}

// An invocation rejected by validation must not leave a registered run behind:
// nothing would ever close it, and it would sit in the index as `running`
// forever.
func TestRejectedInvocationRegistersNothing(t *testing.T) {
	repo := t.TempDir()
	req := identityRequest(t, repo, "alpha")
	req.ABSpec = "sonnet" // one model: --ab needs exactly two

	if _, err := NewRunner(req); err == nil {
		t.Fatal("NewRunner accepted a malformed --ab spec")
	}
	if entries, err := run.ReadIndex(req.Registry.Home); err != nil {
		t.Fatalf("ReadIndex: %v", err)
	} else if len(entries) != 0 {
		t.Errorf("index holds %d line(s) for a rejected invocation: %+v", len(entries), entries)
	}
	if recs, err := run.ReadRecords(repo); err != nil {
		t.Fatalf("ReadRecords: %v", err)
	} else if len(recs) != 0 {
		t.Errorf("repo holds %d record(s) for a rejected invocation", len(recs))
	}
}

// Identity is not optional, and this is the decision F1 got wrong.
//
// The old rule was "report it, never abort": an unwritable home degraded to a run
// with no id and lines with no run_id, on the argument that a full disk must not
// refuse work the user is about to pay for. The audit showed what that rule buys
// under stress — 13 of 20 real runs silently reverting to the pre-F1 ledger shape
// — and the trade is the wrong way round. A run nobody can attribute destroys the
// premise of the phase precisely when someone is trying to work out what happened;
// a refused run costs one clear error message.
func TestUnregisterableIdentityRefusesToStartTheRun(t *testing.T) {
	repo := t.TempDir()
	req := identityRequest(t, repo, "alpha")
	// A file where the home directory belongs: MkdirAll cannot win.
	blocked := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}
	req.Registry.Home = blocked

	r, err := NewRunner(req)
	if err == nil {
		t.Fatalf("NewRunner started a run whose identity could not be registered (run id %q): "+
			"every ledger line it writes is unattributable", r.RunID)
	}
	if !strings.Contains(err.Error(), "identity") {
		t.Errorf("error = %v, want it to name run identity so the user can act on it", err)
	}
	if r != nil {
		t.Errorf("NewRunner returned a runner alongside the error: %+v", r)
	}
	assertNoLedger(t, repo, "alpha")
}

// TestExhaustedIDSpaceRefusesTheRunAndWritesNoLedgerLine is the ops half of the
// id-space defect: the failure the run package raises must reach the user, not be
// folded into a field nobody has to read.
func TestExhaustedIDSpaceRefusesTheRunAndWritesNoLedgerLine(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	gen := opsHexCycle(1) // a 16-id space, so exhaustion is affordable

	// One project per run, on purpose: the id space is global to the home, so
	// exhausting it does not need them to share a project — and sharing one is
	// now refused before the id is even asked for (two live runs of one project
	// would rewrite each other's tasks.md). The subject here is the id space.
	const space = 16
	for i := 0; i < space; i++ {
		if _, err := (run.Registry{Repo: repo, Home: home, NewID: gen}).Start(
			run.StartOptions{Project: fmt.Sprintf("filler-%02d", i)}); err != nil {
			t.Fatalf("filling the id space, run %d: %v", i+1, err)
		}
	}

	req := identityRequest(t, repo, "alpha")
	req.Registry.Home = home
	req.Registry.NewID = gen

	r, err := NewRunner(req)
	if err == nil {
		t.Fatalf("NewRunner started run %q with the id space full", r.RunID)
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("error = %v, want it to say the id space is exhausted", err)
	}
	assertNoLedger(t, repo, "alpha")
}

// assertNoLedger checks that no ledger exists for a run that never started. A
// ledger line with no run_id is the exact regression being guarded against, and
// the only way to write one is through an orchestrator NewRunner never returned.
func assertNoLedger(t *testing.T, repo, project string) {
	t.Helper()
	path := filepath.Join(repo, ".corvex", "tasks", project, "activity.jsonl")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			continue
		}
		if id, ok := fields["run_id"].(string); !ok || id == "" {
			t.Errorf("ledger line with no run_id after a refused run: %s", line)
		}
	}
}

// opsHexCycle mirrors the run package's tiny-space generator: every id of a
// `digits`-wide hex space, in order, wrapping.
func opsHexCycle(digits int) run.IDFunc {
	n, size := 0, 1
	for i := 0; i < digits; i++ {
		size *= 16
	}
	return func() (string, error) {
		id := fmt.Sprintf("%s%0*x", run.IDPrefix, digits, n%size)
		n++
		return id, nil
	}
}

func TestExecuteRejectsMisuse(t *testing.T) {
	var nilRunner *Runner
	if err := nilRunner.Execute(context.Background(), func(context.Context) error { return nil }); err == nil {
		t.Error("Execute on a nil runner should error, not panic")
	}
	if nilRunner.Record().RunID != "" {
		t.Error("Record on a nil runner should be zero")
	}
	repo := t.TempDir()
	r, err := NewRunner(identityRequest(t, repo, "alpha"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := r.Execute(context.Background(), nil); err == nil {
		t.Error("Execute with no body should error")
	}
}

// The repo a run is recorded against is the git root, not the directory the
// command happened to be invoked from — that is what a second process joins on.
func TestRunRepoResolvesTheGitRootAndFallsBack(t *testing.T) {
	root := t.TempDir()
	gitInitBare(t, root)
	sub := filepath.Join(root, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := runRepo(RunRequest{WorkDir: sub})
	if err != nil {
		t.Fatalf("runRepo: %v", err)
	}
	if resolved, _ := filepath.EvalSymlinks(root); got != root && got != resolved {
		t.Errorf("runRepo from a subdirectory = %q, want the git root %q", got, root)
	}

	// Outside a repository there is nothing to key on; the working directory is
	// the honest answer, and `corvex run` outside a repo keeps working.
	plain := t.TempDir()
	got, err = runRepo(RunRequest{WorkDir: plain})
	if err != nil {
		t.Fatalf("runRepo outside a repo: %v", err)
	}
	if got != plain {
		t.Errorf("runRepo outside a repo = %q, want %q", got, plain)
	}

	// An explicit repo always wins over discovery.
	got, err = runRepo(RunRequest{WorkDir: sub, Repo: plain})
	if err != nil {
		t.Fatalf("runRepo with an explicit repo: %v", err)
	}
	if got != plain {
		t.Errorf("explicit repo ignored: got %q, want %q", got, plain)
	}
}

func gitInitBare(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// Two live runs of one project in one repository are refused — before the second
// one exists.
//
// MEASURED, and the outcome is worse than "both run": the second process reads a
// tasks.md whose step the first is executing, sees it RUNNING, and — correctly
// for the case that rescue exists for — treats it as the leftover of a run that
// died, resets it to PENDING and executes it again. Two agents in one checkout,
// two processes rewriting one file, final state decided by whoever wrote last,
// and nothing anywhere reporting it. With a UI whose dispatch is a button, this
// is one double-click away.
func TestStartIdentity_RefusesASecondLiveRunOfTheSameProject(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	reg := run.Registry{Repo: repo, Home: home, Machine: "test-machine"}
	first, err := reg.Start(run.StartOptions{Project: "pilot"})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	req := identityRequest(t, repo, "pilot")
	req.Registry.Home = home
	req.Registry.Machine = "test-machine"

	_, err = NewRunner(req)
	if err == nil {
		t.Fatal("a second run of a live project was allowed to start")
	}
	for _, want := range []string{first.RunID(), "already running", "run kill"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is missing %q: %v", want, err)
		}
	}

	// A DIFFERENT project in the same repository is untouched: the collision is
	// about sharing one tasks.md and one checkout, not about the repository.
	other := identityRequest(t, repo, "outra")
	other.Registry.Home = home
	other.Registry.Machine = "test-machine"
	if _, err := NewRunner(other); err != nil {
		t.Errorf("a run of another project was refused: %v", err)
	}
}

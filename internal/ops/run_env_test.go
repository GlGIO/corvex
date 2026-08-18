package ops

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/run"
)

// The run environment (F6). Every test here injects StackUp, because the thing
// being asserted is the LIFECYCLE — up before the first step, down exactly once,
// never up at all for `simple` — and none of that needs a docker daemon. The
// real implementation is one call to internal/stack, which `corvex validate`
// has been exercising since before F0.

func TestParseEnvironment_RefusesAnUnknownName(t *testing.T) {
	for _, raw := range []string{"", "simple", "stack"} {
		if _, err := ParseEnvironment(raw); err != nil {
			t.Errorf("ParseEnvironment(%q) = %v, want nil", raw, err)
		}
	}
	_, err := ParseEnvironment("postgres")
	if err == nil {
		t.Fatal("an unknown environment was accepted; it would silently degrade to `simple`")
	}
	if !strings.Contains(err.Error(), "stack") {
		t.Errorf("the error does not name the valid values: %v", err)
	}
}

// envRunner builds a Runner with a real identity handle in scratch dirs, so
// Execute exercises the production path (heartbeat, cancellation watcher,
// terminal status) around the environment.
func envRunner(t *testing.T, kind Environment, up StackUpFn) *Runner {
	t.Helper()
	repo, home := t.TempDir(), t.TempDir()
	reg := run.Registry{Repo: repo, Home: home, Machine: "test-machine"}
	handle, err := reg.Start(run.StartOptions{Project: "alpha", Environment: string(kind)})
	if err != nil {
		t.Fatalf("registering: %v", err)
	}
	return &Runner{
		Project: "alpha", RunID: handle.RunID(), Repo: repo,
		Environment: kind,
		handle:      handle,
		interval:    time.Hour, // no beats during the test
		env:         &runEnvironment{kind: kind},
		stackUp:     up,
	}
}

func TestExecute_SimpleNeverTouchesTheEnvironment(t *testing.T) {
	var ups int32
	r := envRunner(t, EnvSimple, func(context.Context) (func(), error) {
		atomic.AddInt32(&ups, 1)
		return func() {}, nil
	})
	if err := r.Execute(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if ups != 0 {
		t.Errorf("the default environment brought a stack up %d time(s); `simple` must cost nothing", ups)
	}
}

func TestExecute_StackIsUpBeforeTheBodyAndDownAfterIt(t *testing.T) {
	var up, down int32
	var upWhenBodyRan int32
	r := envRunner(t, EnvStack, func(context.Context) (func(), error) {
		atomic.AddInt32(&up, 1)
		return func() { atomic.AddInt32(&down, 1) }, nil
	})

	err := r.Execute(context.Background(), func(context.Context) error {
		upWhenBodyRan = atomic.LoadInt32(&up)
		if atomic.LoadInt32(&down) != 0 {
			t.Error("the environment was torn down before the body ran")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if upWhenBodyRan != 1 {
		t.Errorf("the body ran with %d stack(s) up, want 1", upWhenBodyRan)
	}
	if down != 1 {
		t.Errorf("teardown ran %d time(s), want exactly 1 — twice means `docker rm` on somebody else's container", down)
	}
}

// A failure to bring the environment up must stop the run before it spends
// anything: a suite that needs Postgres, run without Postgres, blames the tests.
func TestExecute_AFailedEnvironmentStopsTheRunBeforeTheBody(t *testing.T) {
	bodyRan := false
	r := envRunner(t, EnvStack, func(context.Context) (func(), error) {
		return nil, errors.New("port 5432 already in use")
	})

	err := r.Execute(context.Background(), func(context.Context) error {
		bodyRan = true
		return nil
	})
	if err == nil {
		t.Fatal("Execute returned nil when the environment could not be brought up")
	}
	if bodyRan {
		t.Error("the body ran without its environment")
	}
	// And the run is recorded as failed, not left running: a reader in another
	// process has to be able to tell.
	views, lerr := run.Resolver{Home: t.TempDir()}.ListRepo(r.Repo)
	if lerr != nil {
		t.Fatalf("ListRepo: %v", lerr)
	}
	if len(views) != 1 || views[0].Record.Status != run.StatusFailed {
		t.Errorf("record says %+v, want a single failed run", views)
	}
}

// The panic path: a stack left standing outlives the process that started it,
// and is the one part of a crashed run that keeps costing money.
func TestExecute_TearsTheEnvironmentDownOnPanic(t *testing.T) {
	var down int32
	r := envRunner(t, EnvStack, func(context.Context) (func(), error) {
		return func() { atomic.AddInt32(&down, 1) }, nil
	})

	func() {
		defer func() { _ = recover() }()
		_ = r.Execute(context.Background(), func(context.Context) error {
			panic("boom")
		})
	}()
	if down != 1 {
		t.Errorf("teardown ran %d time(s) after a panic, want 1", down)
	}
}

// The environment reaches the run record, so a second process can say what stood
// around a run it did not start.
func TestStartIdentity_RecordsTheEnvironment(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	handle, err := startIdentity(RunRequest{
		Project: "alpha", WorkDir: repo, Repo: repo, Environment: "stack",
		Registry: &run.Registry{Repo: repo, Home: home, Machine: "test-machine"},
	})
	if err != nil {
		t.Fatalf("startIdentity: %v", err)
	}
	if got := handle.Record().Environment; got != "stack" {
		t.Errorf("record environment = %q, want stack", got)
	}
}

// A misspelled environment is refused where every other flag is refused: before
// identity exists, so no `running` record is left for nobody to close.
func TestNewRunner_RefusesAnUnknownEnvironmentBeforeRegistering(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	_, err := NewRunner(RunRequest{
		Config: config.Default(), Project: "alpha", WorkDir: repo, Repo: repo,
		Environment: "postgres",
		Registry:    &run.Registry{Repo: repo, Home: home, Machine: "test-machine"},
	})
	if err == nil {
		t.Fatal("NewRunner accepted an unknown environment")
	}
	views, lerr := run.Resolver{Home: home}.ListRepo(repo)
	if lerr == nil && len(views) != 0 {
		t.Errorf("a refused invocation left %d record(s) behind", len(views))
	}
}

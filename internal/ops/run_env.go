package ops

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/stack"
	"github.com/giovannialves/corvex/internal/wizard"
)

// Environment is what a run needs standing before its first step (F6).
//
// Two values, and the second one is the whole feature: "run this with Postgres"
// was the recurring pain the roadmap names, and the machinery for it already
// existed — `corvex validate` has been bringing up a database container, running
// migrations and starting the app since before F0. F6 does not build a second
// one; it lets a RUN ask for the same environment `validate` asks for.
type Environment string

const (
	// EnvSimple is the default: the run gets whatever the shell already had.
	EnvSimple Environment = "simple"
	// EnvStack brings up the `validate:` stack for the life of the run.
	EnvStack Environment = "stack"
)

// ParseEnvironment reads the name a user typed. Empty means the default, which
// is the only spelling that may be omitted: an unknown value is an error rather
// than a silent fallback to `simple`, because falling back would run a Postgres
// test suite against no Postgres and blame the tests.
func ParseEnvironment(raw string) (Environment, error) {
	switch Environment(raw) {
	case "":
		return EnvSimple, nil
	case EnvSimple:
		return EnvSimple, nil
	case EnvStack:
		return EnvStack, nil
	default:
		return "", fmt.Errorf("unknown environment %q: use `simple` (nothing extra) or `stack` (the validate: stack, database included)", raw)
	}
}

// StackUpFn brings an environment up and returns the teardown.
//
// It is a field on the request rather than a direct call to internal/stack so a
// test can assert the lifecycle — brought up before the first step, torn down
// exactly once, and not brought up at all for `simple` — without a docker
// daemon. The production implementation is DefaultStackUp.
type StackUpFn func(ctx context.Context) (func(), error)

// DefaultStackUp is the production environment: exactly what `corvex validate`
// stands up, from exactly the same config block.
//
// Progress reaches the user through the shared logger, which is how
// internal/stack has always reported (a preserved F0 debt: the operation is not
// yet parameterised by an io.Writer, and F7 is where that has to change because
// an HTTP caller has nowhere to read a global logger from).
func DefaultStackUp(workDir, project string, cfg *config.Config, out, errOut io.Writer) StackUpFn {
	return func(ctx context.Context) (func(), error) {
		if !wizard.Configured(cfg.Validate) {
			return nil, fmt.Errorf("--env stack needs the `validate:` stack configured — run `corvex validate %s` once to set it up", project)
		}
		cleanup, err := stack.Setup(ctx, workDir, project, cfg.Validate, stack.Streams{Out: out, Err: errOut})
		if err != nil {
			return nil, fmt.Errorf("bringing up the run environment: %w", err)
		}
		return cleanup, nil
	}
}

// runEnvironment holds one run's environment and guarantees the teardown runs
// once.
//
// Once, not "at most once": both the normal path and the panic path tear down,
// and a stack torn down twice means `docker rm` on a container somebody else's
// run may have just created with the same name.
type runEnvironment struct {
	kind Environment
	down func()
	once sync.Once
}

func (e *runEnvironment) up(ctx context.Context, upFn StackUpFn) error {
	if e == nil || e.kind != EnvStack {
		return nil
	}
	if upFn == nil {
		return fmt.Errorf("--env stack requested but no environment provider was wired")
	}
	down, err := upFn(ctx)
	if err != nil {
		return err
	}
	e.down = down
	return nil
}

func (e *runEnvironment) teardown() {
	if e == nil || e.down == nil {
		return
	}
	e.once.Do(e.down)
}

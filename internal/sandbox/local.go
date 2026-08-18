package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
)

// LocalSandbox executes commands directly on the host machine.
type LocalSandbox struct {
	workDir   string
	prepared  bool
	cmdRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// NewLocalSandbox creates a LocalSandbox. If cfg.WorkDir is empty, the
// current working directory is used.
func NewLocalSandbox(cfg config.SandboxConfig) *LocalSandbox {
	wd := cfg.WorkDir
	if wd == "" {
		wd, _ = os.Getwd()
	}
	return &LocalSandbox{
		workDir:   wd,
		cmdRunner: exec.CommandContext,
	}
}

func (s *LocalSandbox) Prepare(_ context.Context) error {
	s.prepared = true
	return nil
}

func (s *LocalSandbox) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if !s.prepared {
		return nil, ErrNotPrepared
	}

	cmd := s.cmdRunner(ctx, req.Command[0], req.Command[1:]...)
	cmd.Dir = s.workDir
	cmd.Env = mergeEnv(os.Environ(), req.Env, req.DenyEnv)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &RunResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: exitErr.ExitCode(),
			}, nil
		}
		return nil, fmt.Errorf("local sandbox run: %w", err)
	}

	return &RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}, nil
}

func (s *LocalSandbox) Cleanup(_ context.Context) error {
	return nil
}

func (s *LocalSandbox) IsAvailable(_ context.Context) bool {
	return true
}

// mergeEnv builds the child's environment: the host's, minus what custody holds
// back, plus what the caller adds.
//
// The subtraction is not symmetric with the addition, and that is deliberate:
// `extra` is what corvex decided to forward, `deny` is what it decided nobody
// downstream may see. So deny wins even over an explicit addition — otherwise a
// caller could hand back the credential custody just removed.
func mergeEnv(base []string, extra map[string]string, deny ...[]string) []string {
	held := make(map[string]struct{})
	for _, list := range deny {
		for _, name := range list {
			if name = strings.TrimSpace(name); name != "" {
				held[name] = struct{}{}
			}
		}
	}

	env := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if name, _, ok := strings.Cut(kv, "="); ok {
			if _, blocked := held[name]; blocked {
				continue
			}
		}
		env = append(env, kv)
	}
	for k, v := range extra {
		if _, blocked := held[k]; blocked {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}

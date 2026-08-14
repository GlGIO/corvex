package stack

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/config"
)

// StartDBContainer boots the database container for a validation run and
// returns the function that removes it again. Types without a readiness probe
// (anything other than postgres/mysql) are declared ready as soon as
// `docker run` exits.
func StartDBContainer(ctx context.Context, project string, cfg config.ValidateDBConfig) (func(), error) {
	name := "corvex-val-" + project + "-db"

	// Remove any leftover container from a previous run.
	exec.Command("docker", "rm", "-f", name).Run()

	args := []string{"run", "-d", "--rm", "--name", name}
	for k, v := range cfg.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, cfg.Image)

	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("starting %s container: %s: %w", cfg.Type, strings.TrimSpace(string(out)), err)
	}

	stopFn := func() { exec.Command("docker", "rm", "-f", name).Run() }

	// Poll until ready.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var check *exec.Cmd
		switch cfg.Type {
		case "postgres":
			check = exec.Command("docker", "exec", name, "pg_isready")
		case "mysql":
			check = exec.Command("docker", "exec", name, "mysqladmin", "ping", "--silent")
		default:
			return stopFn, nil
		}
		if check.Run() == nil {
			return stopFn, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	stopFn()
	return nil, fmt.Errorf("database did not become ready within 60s")
}

// Package stack brings the validation environment up and down: the database
// container, the migrations, the application process, the health probe and the
// optional headless Chrome.
//
// It carries no cobra dependency and owns no output stream of its own: every
// byte a child process writes lands in the Streams the caller supplies.
// Progress is reported through the shared charmlog logger, exactly as it was
// when this code lived in cmd/validate.go.
package stack

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/config"
)

// Streams are the destinations for the stdout/stderr of the child processes the
// stack starts — the migration command and the application. cmd/ passes the
// process streams; tests pass whatever they capture.
type Streams struct {
	Out io.Writer
	Err io.Writer
}

// CleanupFn tears the stack back down. Cleanups always run in reverse order of
// setup, so Chrome dies before the app and the app before the database.
type CleanupFn func()

// Setup starts the whole validation stack and returns the function that tears
// it down. On any failure it tears down whatever it had already started and
// returns a nil CleanupFn.
func Setup(ctx context.Context, workDir, project string, cfg config.ValidateConfig, streams Streams) (CleanupFn, error) {
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// 1. Database container
	if cfg.Database.Type != "" && cfg.Database.Type != "none" && cfg.Database.Type != "sqlite" {
		dbCleanup, err := StartDBContainer(ctx, project, cfg.Database)
		if err != nil {
			cleanup()
			return nil, err
		}
		cleanups = append(cleanups, dbCleanup)
		log.Info("database ready", "type", cfg.Database.Type)
	}

	// Env file (e.g. backend/.env-stg): sourced into BOTH migrate and app so
	// the stack runs against the chosen environment (STG, etc.).
	appEnv := os.Environ()
	if cfg.Stack.EnvFile != "" {
		vars, err := LoadEnvFileVars(filepath.Join(workDir, cfg.Stack.EnvFile))
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("loading stack env_file %s: %w", cfg.Stack.EnvFile, err)
		}
		appEnv = append(appEnv, vars...)
		log.Info("sourced stack env_file", "file", cfg.Stack.EnvFile, "vars", len(vars))
	}

	// 2. Migrations
	if cfg.Database.MigrateCommand != "" {
		log.Info("running migrations")
		parts := strings.Fields(cfg.Database.MigrateCommand)
		c := exec.CommandContext(ctx, parts[0], parts[1:]...)
		c.Dir = workDir
		c.Env = appEnv
		c.Stdout = streams.Out
		c.Stderr = streams.Err
		if err := c.Run(); err != nil {
			cleanup()
			return nil, fmt.Errorf("migrations failed: %w", err)
		}
	}

	// Preflight: refuse to start if the port is already taken. Otherwise the
	// app fails to bind, the health check passes against whatever is ALREADY
	// listening, and the Validator silently judges the wrong server.
	if cfg.Stack.Port != 0 && PortInUse(cfg.Stack.Port) {
		cleanup()
		return nil, fmt.Errorf("port %d is already in use — stop the process listening there (it would be validated instead of your app), or set a different stack.port", cfg.Stack.Port)
	}

	// 3. Application
	appCmd, err := StartApp(workDir, cfg.Stack, appEnv, streams)
	if err != nil {
		cleanup()
		return nil, err
	}
	cleanups = append(cleanups, func() {
		if appCmd.Process != nil {
			appCmd.Process.Kill()
			appCmd.Wait()
		}
	})

	// 4. Wait for app health
	log.Info("waiting for app to be ready", "port", cfg.Stack.Port)
	if err := WaitForHealth(ctx, cfg.Stack); err != nil {
		cleanup()
		return nil, err
	}
	log.Info("app ready")

	// 5. Chrome (if UI enabled)
	if cfg.UI.Enabled {
		chromeCmd, err := StartChrome(ctx)
		if err != nil {
			cleanup()
			return nil, err
		}
		cleanups = append(cleanups, func() {
			if chromeCmd.Process != nil {
				chromeCmd.Process.Kill()
				chromeCmd.Wait()
			}
		})
		log.Info("chrome CDP ready", "port", 9222)
	}

	return cleanup, nil
}

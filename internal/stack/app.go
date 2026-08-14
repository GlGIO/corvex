package stack

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
)

// StartApp launches the application under validation. The start command is
// split on whitespace, so no quoting is possible: `--flag 'quoted arg'` reaches
// the process as two separate arguments.
func StartApp(workDir string, cfg config.ValidateStackConfig, env []string, streams Streams) (*exec.Cmd, error) {
	parts := strings.Fields(cfg.StartCommand)
	if len(parts) == 0 {
		return nil, fmt.Errorf("start_command is empty")
	}
	c := exec.Command(parts[0], parts[1:]...)
	c.Dir = workDir
	c.Env = env
	c.Stdout = streams.Out
	c.Stderr = streams.Err
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("starting app (%s): %w", cfg.StartCommand, err)
	}
	return c, nil
}

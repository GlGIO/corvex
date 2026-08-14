package stack

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/giovannialves/corvex/internal/config"
)

// WaitForHealth polls the app until it answers with any status below 500 — a
// 404 counts as ready. An empty health_path falls back to "/", a zero
// ready_timeout to 30 seconds.
func WaitForHealth(ctx context.Context, cfg config.ValidateStackConfig) error {
	path := cfg.HealthPath
	if path == "" {
		path = "/"
	}
	url := fmt.Sprintf("http://localhost:%d%s", cfg.Port, path)

	timeout := time.Duration(cfg.ReadyTimeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("app did not become ready at %s within %v", url, timeout)
}

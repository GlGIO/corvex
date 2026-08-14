package stack

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

// StartChrome launches the first Chrome/Chromium binary it finds on PATH in
// headless mode with the CDP endpoint on the fixed port 9222, and waits for that
// endpoint to answer.
func StartChrome(ctx context.Context) (*exec.Cmd, error) {
	binary := ""
	for _, candidate := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if _, err := exec.LookPath(candidate); err == nil {
			binary = candidate
			break
		}
	}
	if binary == "" {
		return nil, fmt.Errorf("no Chrome/Chromium binary found — install chromium or google-chrome")
	}

	c := exec.CommandContext(ctx, binary,
		"--headless",
		"--remote-debugging-port=9222",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
	)
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("starting Chrome (%s): %w", binary, err)
	}

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://localhost:9222/json/version")
		if err == nil {
			resp.Body.Close()
			return c, nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	c.Process.Kill()
	return nil, fmt.Errorf("Chrome CDP did not become ready on port 9222")
}

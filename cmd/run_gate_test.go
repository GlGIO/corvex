package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

func TestDoctorGate(t *testing.T) {
	// Make the provider binary check pass deterministically.
	t.Setenv("CORVEX_CLAUDE_BIN", os.Args[0])

	t.Run("valid config passes", func(t *testing.T) {
		cfg := config.Default()
		if err := doctorGate(cfg, t.TempDir()); err != nil {
			t.Errorf("doctorGate() = %v, want nil for default config", err)
		}
	})

	t.Run("unknown provider fails", func(t *testing.T) {
		cfg := config.Default()
		cfg.Provider.Default = "bogus-provider"
		err := doctorGate(cfg, t.TempDir())
		if err == nil {
			t.Fatal("doctorGate() = nil, want error for unknown provider")
		}
		if !strings.Contains(err.Error(), "preflight failed") || !strings.Contains(err.Error(), "provider") {
			t.Errorf("doctorGate() error = %q, want it to mention the failed provider check", err)
		}
	})
}

func TestRunPreview(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".corvex", "tasks", "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tasks := "---\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — A ✅ PASSED\n\n### O que fazer\nx\n\n---\n\n" +
		"## S02 — B ⬜ PENDING\n\n### O que fazer\ny\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(tasks), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default() // MaxCostUSD 25, per-task 5
	got := runPreview(tmp, "proj", cfg)
	if !strings.Contains(got, "1 pending") {
		t.Errorf("preview = %q, want it to report 1 pending task", got)
	}
	if !strings.Contains(got, "ceilings") {
		t.Errorf("preview = %q, want it to mention ceilings", got)
	}
}

func TestConfirmRun_YesBypass(t *testing.T) {
	orig := runYes
	t.Cleanup(func() { runYes = orig })
	runYes = true
	if !confirmRun() {
		t.Error("confirmRun() with --yes should return true without prompting")
	}
}

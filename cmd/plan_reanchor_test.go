package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/types"
)

func TestReanchorSpec(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.md")
	tasks := filepath.Join(dir, "tasks.md")
	anc := filepath.Join(dir, "anchor.yaml")

	if err := os.WriteFile(spec, []byte("# Spec v2 (edited validation note)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tasks, []byte("## S01 — Only task ✅ PASSED\n\n### O que fazer\n1. x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-existing anchor carries a stale hash.
	if err := anchor.Save(anc, types.AnchorState{Project: "feat1", SpecHash: "stale"}); err != nil {
		t.Fatal(err)
	}

	if err := reanchorSpec("feat1", spec, tasks, anc); err != nil {
		t.Fatalf("reanchorSpec() error = %v", err)
	}

	want, _ := anchor.SpecHash(spec)
	got, err := anchor.Load(anc)
	if err != nil {
		t.Fatal(err)
	}
	if got.SpecHash != want {
		t.Errorf("spec_hash = %q, want %q (current spec)", got.SpecHash, want)
	}
	if got.Project != "feat1" {
		t.Errorf("project = %q, want feat1", got.Project)
	}
}

func TestReanchorSpec_Guards(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.md")
	tasks := filepath.Join(dir, "tasks.md")
	anc := filepath.Join(dir, "anchor.yaml")
	_ = os.WriteFile(spec, []byte("# Spec\n"), 0o644)

	// No tasks.md → refuse.
	if err := reanchorSpec("feat1", spec, tasks, anc); err == nil {
		t.Error("expected error when tasks.md is missing")
	}

	// Corrupted tasks.md (invalid status word with right shape) → refuse.
	if err := os.WriteFile(tasks, []byte("## S01 — Broken ✅ FROBNICATED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reanchorSpec("feat1", spec, tasks, anc); err == nil {
		t.Error("expected error when tasks.md does not parse")
	}
}

package orchestrator

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

func writeSkill(t *testing.T, workDir, name string) {
	t.Helper()
	dir := filepath.Join(workDir, ".corvex", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: x\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMaterializeSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "foo")
	writeSkill(t, dir, "bar")
	// A dir without SKILL.md must be ignored.
	if err := os.MkdirAll(filepath.Join(dir, ".corvex", "skills", "nope"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A pre-existing .claude/skills/bar must NOT be clobbered.
	existing := filepath.Join(dir, ".claude", "skills", "bar")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "SKILL.md"), []byte("user's own"), 0o644); err != nil {
		t.Fatal(err)
	}

	o := New(Options{Config: config.Default(), Provider: &mockProvider{}, WorkDir: dir})
	cleanup := o.materializeSkills()

	// foo was linked.
	fooLink := filepath.Join(dir, ".claude", "skills", "foo")
	info, err := os.Lstat(fooLink)
	if err != nil {
		t.Fatalf("foo not materialized: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("foo should be a symlink")
	}
	// bar is still the user's real directory, not replaced by a symlink.
	barInfo, err := os.Lstat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if barInfo.Mode()&os.ModeSymlink != 0 {
		t.Errorf("pre-existing bar was clobbered by a symlink")
	}
	// nope was not linked.
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "skills", "nope")); err == nil {
		t.Errorf("nope (no SKILL.md) should not be materialized")
	}

	cleanup()
	// our foo link is removed; the user's bar survives.
	if _, err := os.Lstat(fooLink); err == nil {
		t.Errorf("cleanup should remove the foo link")
	}
	if _, err := os.Lstat(existing); err != nil {
		t.Errorf("cleanup must not remove the user's bar: %v", err)
	}
}

func TestRepoSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha")
	writeSkill(t, dir, "beta")
	if err := os.MkdirAll(filepath.Join(dir, ".corvex", "skills", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := RepoSkills(dir)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("RepoSkills() = %v, want [alpha beta]", got)
	}
}

package recovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %s\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func initGitRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.email", "test@corvex.dev")
	gitExec(t, dir, "config", "user.name", "corvex-test")

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	gitExec(t, dir, "add", "-A")
	gitExec(t, dir, "commit", "-m", "initial")

	return dir
}

func TestActionString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action Action
		want   string
	}{
		{Continue, "continue"},
		{RetryTask, "retry"},
		{Action(99), "Action(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := tt.action.String()
			if got != tt.want {
				t.Errorf("Action(%d).String() = %q, want %q", int(tt.action), got, tt.want)
			}
		})
	}
}

func TestGuardCleanRepo(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	result, err := mgr.Guard()
	if err != nil {
		t.Fatalf("Guard() error = %v", err)
	}
	if result.Action != Continue {
		t.Errorf("Action = %v, want Continue", result.Action)
	}
}

func TestGuardDirtyDoesNotDestroy(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("uncommitted work"), 0644); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(untracked, []byte("draft"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.Guard()
	if err != nil {
		t.Fatalf("Guard() error = %v", err)
	}
	if result.Action != AbortDirty {
		t.Errorf("Action = %v, want AbortDirty", result.Action)
	}
	if len(result.DirtyFiles) < 2 {
		t.Errorf("DirtyFiles = %v, want at least 2", result.DirtyFiles)
	}

	// Guard must NOT touch the working tree.
	content, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "uncommitted work" {
		t.Errorf("README.md content = %q, want it preserved (Guard must not reset)", string(content))
	}
	if _, err := os.Stat(untracked); err != nil {
		t.Errorf("untracked file removed by Guard: %v (Guard must be non-destructive)", err)
	}
}

func TestCheckCleanRepo(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	result, err := mgr.Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if result.Action != Continue {
		t.Errorf("Action = %v, want Continue", result.Action)
	}
	if len(result.DirtyFiles) != 0 {
		t.Errorf("DirtyFiles = %v, want empty", result.DirtyFiles)
	}
}

func TestCheckDirtyTrackedFile(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if result.Action != RetryTask {
		t.Errorf("Action = %v, want RetryTask", result.Action)
	}

	found := false
	for _, f := range result.DirtyFiles {
		if f == "README.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DirtyFiles = %v, want to contain README.md", result.DirtyFiles)
	}

	content, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "initial" {
		t.Errorf("file content after Check = %q, want %q (should be reverted)", string(content), "initial")
	}
}

func TestCheckUntrackedFile(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	newFile := filepath.Join(dir, "untracked.txt")
	if err := os.WriteFile(newFile, []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if result.Action != RetryTask {
		t.Errorf("Action = %v, want RetryTask", result.Action)
	}

	found := false
	for _, f := range result.DirtyFiles {
		if f == "untracked.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DirtyFiles = %v, want to contain untracked.txt", result.DirtyFiles)
	}

	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Error("untracked file should be removed after Check, but still exists")
	}
}

func TestCheckDirtyTrackedAndUntracked(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if result.Action != RetryTask {
		t.Errorf("Action = %v, want RetryTask", result.Action)
	}
	if len(result.DirtyFiles) < 2 {
		t.Errorf("DirtyFiles = %v, want at least 2 files", result.DirtyFiles)
	}
}

func TestCheckNotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	_, err := mgr.Check()
	if err == nil {
		t.Fatal("Check() on non-git dir should return error")
	}
}

func TestMarkCheckpointCreatesCommit(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	newFile := filepath.Join(dir, "feature.go")
	if err := os.WriteFile(newFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := mgr.MarkCheckpoint("S01"); err != nil {
		t.Fatalf("MarkCheckpoint() error = %v", err)
	}

	out := gitExec(t, dir, "log", "--oneline", "-1")
	if !strings.Contains(out, "corvex: checkpoint S01") {
		t.Errorf("latest commit = %q, want to contain %q", out, "corvex: checkpoint S01")
	}

	statusOut := gitExec(t, dir, "status", "--porcelain")
	if strings.TrimSpace(statusOut) != "" {
		t.Errorf("repo still dirty after checkpoint: %s", statusOut)
	}
}

// The MCP configs are corvex's own files, written into the project on every
// agent call, and the worker's one carries the declared servers with their env
// ALREADY EXPANDED — credentials, in a file. Whether they stay out of the commit
// used to depend on the repository's `.corvex/.gitignore`, and one written by an
// older `corvex init` lists `mcp.json` but not `mcp-none.json`: the SmartCare
// hotfix for #75993 reached its PR carrying `.corvex/mcp-none.json`. The repo
// here has no ignore file at all, which is the case the checkpoint must survive.
func TestMarkCheckpointNeverCommitsMCPConfigs(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	if err := os.MkdirAll(filepath.Join(dir, ".corvex"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".corvex/mcp.json", ".corvex/mcp-none.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"mcpServers":{}}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := mgr.MarkCheckpoint("S01"); err != nil {
		t.Fatalf("MarkCheckpoint() error = %v", err)
	}

	committed := gitExec(t, dir, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(committed, "feature.go") {
		t.Errorf("checkpoint left out the worker's change; committed:\n%s", committed)
	}
	if strings.Contains(committed, "mcp") {
		t.Errorf("checkpoint committed an MCP config; committed:\n%s", committed)
	}
}

func TestMarkCheckpointNoChanges(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	beforeLog := gitExec(t, dir, "log", "--oneline")

	if err := mgr.MarkCheckpoint("S01"); err != nil {
		t.Fatalf("MarkCheckpoint() error = %v", err)
	}

	afterLog := gitExec(t, dir, "log", "--oneline")
	if beforeLog != afterLog {
		t.Errorf("commit log changed after no-op checkpoint:\nbefore: %s\nafter: %s", beforeLog, afterLog)
	}
}

func TestMarkCheckpointNotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	if err := mgr.MarkCheckpoint("S01"); err == nil {
		t.Fatal("MarkCheckpoint() on non-git dir should return error")
	}
}

func TestMarkCheckpointMultipleFiles(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := mgr.MarkCheckpoint("S03"); err != nil {
		t.Fatalf("MarkCheckpoint() error = %v", err)
	}

	out := gitExec(t, dir, "log", "--oneline", "-1")
	if !strings.Contains(out, "corvex: checkpoint S03") {
		t.Errorf("latest commit = %q, want %q", out, "corvex: checkpoint S03")
	}

	statusOut := gitExec(t, dir, "status", "--porcelain")
	if strings.TrimSpace(statusOut) != "" {
		t.Errorf("repo still dirty: %s", statusOut)
	}
}

func TestNewManager(t *testing.T) {
	t.Parallel()
	mgr := NewManager("/some/path")
	if mgr.WorkDir != "/some/path" {
		t.Errorf("WorkDir = %q, want %q", mgr.WorkDir, "/some/path")
	}
}

func TestChangedFilesCleanRepo(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	created, modified, err := mgr.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	if len(created) != 0 || len(modified) != 0 {
		t.Errorf("clean repo: created=%v modified=%v, want both empty", created, modified)
	}
}

func TestChangedFilesClassifiesCreatedAndModified(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	// Modify existing tracked file.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a new untracked file.
	if err := os.WriteFile(filepath.Join(dir, "newfile.go"), []byte("package x"), 0644); err != nil {
		t.Fatal(err)
	}

	created, modified, err := mgr.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}

	if !contains(modified, "README.md") {
		t.Errorf("modified = %v, want to contain README.md", modified)
	}
	if !contains(created, "newfile.go") {
		t.Errorf("created = %v, want to contain newfile.go", created)
	}
	if contains(created, "README.md") {
		t.Errorf("README.md should not be in created (it was modified)")
	}
	if contains(modified, "newfile.go") {
		t.Errorf("newfile.go should not be in modified (it is new/untracked)")
	}
}

func TestChangedFilesStagedNewFile(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	// Stage a new file so it shows as 'A' in git diff --name-status HEAD.
	if err := os.WriteFile(filepath.Join(dir, "staged.go"), []byte("package x"), 0644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "add", "staged.go")

	created, _, err := mgr.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}

	if !contains(created, "staged.go") {
		t.Errorf("created = %v, want to contain staged.go", created)
	}
}

func TestChangedFilesExcludesCorvexPaths(t *testing.T) {
	dir := initGitRepo(t)
	mgr := NewManager(dir)

	if err := os.MkdirAll(filepath.Join(dir, ".corvex", "tasks"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".corvex", "tasks", "anchor.yaml"), []byte("state: x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.go"), []byte("package x"), 0644); err != nil {
		t.Fatal(err)
	}

	created, modified, err := mgr.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}

	for _, f := range created {
		if isCorvexPath(f) {
			t.Errorf("created contains corvex path %q, want excluded", f)
		}
	}
	for _, f := range modified {
		if isCorvexPath(f) {
			t.Errorf("modified contains corvex path %q, want excluded", f)
		}
	}
	if !contains(created, "real.go") {
		t.Errorf("created = %v, want to contain real.go", created)
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

package server_test

// The checkout half of a dispatch, measured end to end: the button makes a real
// worktree with a real branch, and a run can then be dispatched into it.
//
// Until this existed, the UI could start a run in a checkout somebody else had
// created and nothing else. That is the one manual step in "one worktree per
// piece of work, all of them watched from one screen", and it is the step where
// the branch name is chosen — the name that decides where the PR goes.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/server"
)

// gitRepo makes a repository with one commit on `main`, which is the state every
// base-branch default in this tool assumes.
func gitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@corvex"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "raiz"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	return dir
}

func TestWorktrees_CreatesTheCheckoutOnTheBranchTheCallerNamed(t *testing.T) {
	root := t.TempDir()
	workDir := gitRepo(t, filepath.Join(root, "repo"))
	argv := filepath.Join(root, "argv")
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, argv, "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()

	rec, body := post(t, h, srv, "/api/worktrees", `{"name":"73960","branch":"hotfix/73960-carrinho","base":"main"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	// The path comes back as `git worktree list` spells it, which is the
	// spelling the screen's checkout list is built from. On macOS a temp dir is
	// /var/... to this test and /private/var/... to git; they are one directory,
	// so the comparison goes through EvalSymlinks.
	path, _ := body["path"].(string)
	want, _ := filepath.EvalSymlinks(filepath.Join(root, "repo-73960"))
	if got, _ := filepath.EvalSymlinks(path); got != want {
		t.Errorf("worktree path = %q, want %q", path, want)
	}
	// The branch is the one the caller asked for, not `feat/<name>`: a
	// repository's branch convention is what routes the PR, so a form that
	// silently renamed it would be choosing the target of the merge.
	if branch, _ := body["branch"].(string); branch != "hotfix/73960-carrinho" {
		t.Errorf("branch = %q, want hotfix/73960-carrinho", branch)
	}
	head := gitOut(t, path, "rev-parse", "--abbrev-ref", "HEAD")
	if head != "hotfix/73960-carrinho" {
		t.Errorf("the checkout is on %q, want the branch that was asked for", head)
	}

	// Parity: the recorded line is the git command a person would have typed,
	// including the cd, because that is the whole claim of the action log.
	command, _ := body["command"].(string)
	if !strings.Contains(command, " && git worktree add ") || !strings.Contains(command, "-b hotfix/73960-carrinho main") {
		t.Errorf("recorded command = %q, want the git worktree add it performed", command)
	}

	// And the point of the feature: the checkout it just made is one the
	// dispatcher accepts. It is in no run index — nothing has ever run there —
	// so before worktrees counted as repositories this was refused as unknown,
	// one button after the screen created it.
	rec, dispatch := post(t, h, srv, "/api/runs", `{"target":"incident","repo":"`+path+`"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("dispatching into the checkout it just created: status = %d: %s", rec.Code, rec.Body)
	}
	dir, _ := dispatch["dir"].(string)
	if got, _ := filepath.EvalSymlinks(dir); got != want {
		t.Errorf("the run was dispatched into %q, want the new worktree %q", dir, path)
	}
	waitFor(t, "the child to report its cwd", func() bool {
		data, err := os.ReadFile(argv)
		return err == nil && strings.Contains(string(data), want)
	})
}

// The default branch stays `feat/<name>`, which is what `corvex start` writes:
// the override is an override, not a new convention.
func TestWorktrees_DefaultsToTheStartConvention(t *testing.T) {
	root := t.TempDir()
	workDir := gitRepo(t, filepath.Join(root, "repo"))
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, filepath.Join(root, "argv"), "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	_, body := post(t, srv.Handler(), srv, "/api/worktrees", `{"name":"carrinho"}`)
	if branch, _ := body["branch"].(string); branch != "feat/carrinho" {
		t.Errorf("branch = %q, want feat/carrinho", branch)
	}
}

// A name that is a path is refused before git sees it: the value becomes a
// directory BESIDE the repository, so `../../etc` there is a write nobody asked
// for, and `a/b` is a worktree in a place the convention cannot find again.
func TestWorktrees_RefusesANameThatIsAPath(t *testing.T) {
	root := t.TempDir()
	workDir := gitRepo(t, filepath.Join(root, "repo"))
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, filepath.Join(root, "argv"), "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	for _, name := range []string{"../escape", "a/b", ""} {
		rec, _ := post(t, srv.Handler(), srv, "/api/worktrees", `{"name":"`+name+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name %q: status = %d, want 400: %s", name, rec.Code, rec.Body)
		}
	}
	// And nothing was created outside the repository.
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); err == nil {
		t.Error("a name of ../escape produced a directory outside the repository")
	}
}

// git's own sentence survives the round trip. The exit status alone ("exit
// status 128") is the shape this used to have, and it is the same status for
// "branch exists", "no such ref" and "already checked out" — three different
// things to do next.
func TestWorktrees_CarriesGitsReason(t *testing.T) {
	root := t.TempDir()
	workDir := gitRepo(t, filepath.Join(root, "repo"))
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, filepath.Join(root, "argv"), "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()
	if rec, _ := post(t, h, srv, "/api/worktrees", `{"name":"um","branch":"feat/x"}`); rec.Code != http.StatusCreated {
		t.Fatalf("first create: %d: %s", rec.Code, rec.Body)
	}
	rec, body := post(t, h, srv, "/api/worktrees", `{"name":"dois","branch":"feat/x"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "already exists") {
		t.Errorf("error = %q, want git's own explanation", msg)
	}
}

// GET lists what git knows, which is what the form offers as the checkout to run
// in. The main checkout is marked, because it is the one that means "no separate
// worktree" and it must not read as just another branch.
func TestWorktrees_ListsTheCheckoutsGitKnows(t *testing.T) {
	root := t.TempDir()
	workDir := gitRepo(t, filepath.Join(root, "repo"))
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, filepath.Join(root, "argv"), "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()
	post(t, h, srv, "/api/worktrees", `{"name":"73960","branch":"hotfix/73960"}`)

	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/worktrees", nil)
	req.Header.Set("Authorization", "Bearer "+srv.Token())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("listing is not JSON: %s", rec.Body)
	}
	if len(list) != 2 {
		t.Fatalf("got %d checkouts, want the repo and the worktree: %s", len(list), rec.Body)
	}
	if main, _ := list[0]["main"].(bool); !main {
		t.Error("the repository's own checkout is not marked as the main one")
	}
	if branch, _ := list[1]["branch"].(string); branch != "hotfix/73960" {
		t.Errorf("the worktree lists branch %q, want hotfix/73960", branch)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

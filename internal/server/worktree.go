package server

// Creating the checkout a run needs, from the screen that dispatches the run.
//
// The operating model this tool is for is one worktree per piece of work, all of
// them watched from one corvex in the main repository. Every part of that was
// reachable from the UI except the first: the worktree itself had to be made in
// a terminal, with `git worktree add -b … main`, and only then did the screen
// have somewhere to dispatch into. So the flow the product is named for started
// outside the product — and a person who does that by hand at 2am picks the
// branch name by memory, which is the one part of it that routes the PR later.

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/ops"
)

// worktreeRequest is what the dispatch form sends to make a checkout.
type worktreeRequest struct {
	// Repo is the repository to branch from — the same list a dispatch may
	// name, resolved by the same function, so the form cannot create a checkout
	// somewhere it would then refuse to run.
	Repo string `json:"repo,omitempty"`
	// Name is what the work is called: the directory becomes `<repo>-<name>`,
	// beside the repository, which is the convention `corvex start` already
	// writes and `corvex run` already looks for.
	Name string `json:"name"`
	// Branch overrides the `feat/<name>` default, because a repository's own
	// convention is what decides where the PR goes — `hotfix/73960-…` is not
	// decoration.
	Branch string `json:"branch,omitempty"`
	// Base is the ref the branch starts from. Empty means `main`, the same
	// default `corvex start` offers at its prompt.
	Base string `json:"base,omitempty"`
}

// handleCreateWorktree makes the checkout and reports where it is.
//
// It does NOT dispatch. Creating a checkout and spending money are two different
// decisions, and a form that did both on one button would make the cheap,
// reversible half impossible to do on its own — which is exactly what a person
// wants when they are setting up three of these before starting any.
func (s *Server) handleCreateWorktree(w http.ResponseWriter, r *http.Request) {
	var req worktreeRequest
	if err := decodeBody(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	repo, err := s.dispatchRepo(startRequest{Repo: req.Repo})
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	gitRoot, err := ops.FindGitRoot(repo)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	name, err := worktreeName(req.Name)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = "feat/" + name
	}
	if err := checkBranchName(branch); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	base := strings.TrimSpace(req.Base)
	if base == "" {
		base = "main"
	}

	path := ops.WorktreePath(gitRoot, name)
	command := fmt.Sprintf("cd %s && git worktree add %s -b %s %s", gitRoot, path, branch, base)

	// An existing directory is refused HERE rather than left to git, because the
	// two cases a person hits are different and git's message covers one of
	// them: a live worktree from earlier today (use it) and a directory left
	// behind by a removed one (git says "already exists" and stops).
	if _, statErr := os.Stat(path); statErr == nil {
		err := fmt.Errorf("%s already exists — pick another name, or dispatch into it: it is already on the list", path)
		action := s.actions.Record(command, err)
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "action": action})
		return
	}

	var chatter bytes.Buffer
	res, setupErr := ops.SetupWorktreeOn(gitRoot, path, branch, base, &chatter, &chatter)
	if setupErr != nil {
		// git's own sentence is the useful part — "a branch named X already
		// exists", "invalid reference: main" — and it is on the streams, not in
		// the exit status the error wraps.
		setupErr = fmt.Errorf("%w: %s", setupErr, strings.TrimSpace(chatter.String()))
		action := s.actions.Record(command, setupErr)
		writeJSON(w, http.StatusConflict, map[string]any{"error": setupErr.Error(), "action": action})
		return
	}
	action := s.actions.Record(command, nil)
	writeJSON(w, http.StatusCreated, map[string]any{
		"path":    asGitListsIt(gitRoot, path),
		"branch":  res.Branch,
		"base":    base,
		"warning": staleBase(gitRoot, base),
		"command": command,
		"action":  action,
	})
}

// handleWorktrees lists a repository's checkouts, so the dispatch form can offer
// the ones that already exist instead of asking the person to remember them.
func (s *Server) handleWorktrees(w http.ResponseWriter, r *http.Request) {
	repo, err := s.dispatchRepo(startRequest{Repo: r.URL.Query().Get("repo")})
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	list, err := ops.ListWorktrees(repo)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// worktreeName accepts the slug that names the directory. It is deliberately
// narrower than a branch name: this value becomes a path beside the repository,
// so a slash or a `..` in it is a write somewhere nobody asked for.
func worktreeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("name is required: it is what the checkout is called (the directory becomes <repo>-<name>)")
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("%q cannot name a directory beside the repository: use a plain name like 73960 or carrinho-lento", raw)
	}
	return name, nil
}

// checkBranchName asks git whether the name is one, instead of guessing with a
// regexp: the rules are git's (no `..`, no trailing `.lock`, no control
// characters, no `@{`) and they are already implemented, in git.
func checkBranchName(branch string) error {
	if err := exec.Command("git", "check-ref-format", "--branch", branch).Run(); err != nil {
		return fmt.Errorf("%q is not a valid branch name", branch)
	}
	return nil
}

// staleBase reports, as a sentence or empty, that the base branch is behind the
// remote it tracks.
//
// This is the step the hand-written version of this flow always includes and
// this one cannot: `git checkout main && git pull && git worktree add …`. A
// worktree cut from a local `main` that is four days old starts the work on the
// wrong base, and nothing downstream notices until the merge. It WARNS rather
// than refusing or pulling: a fetch is a network call this endpoint has no right
// to make on its own, and refusing would strand anyone working offline. The
// count comes from what git already knows locally, so it is silent when there is
// no upstream and says nothing about commits fetched since.
func staleBase(gitRoot, base string) string {
	out, err := exec.Command("git", "-C", gitRoot, "rev-list", "--count", base+".."+base+"@{upstream}").Output()
	if err != nil {
		return ""
	}
	behind := strings.TrimSpace(string(out))
	if behind == "" || behind == "0" {
		return ""
	}
	return fmt.Sprintf("%s is %s commit(s) behind its upstream — this checkout starts from the older one. `git -C %s pull` first if that matters.", base, behind, gitRoot)
}

// asGitListsIt answers with the spelling of the path that `git worktree list`
// uses, which is the spelling the checkout list on the screen is built from.
//
// MEASURED in the browser: the form created the worktree, the new branch
// appeared in the list, and the form could not select it — because the create
// answered with the path it had composed (`/var/…/repo-73960`) while git listed
// the same directory resolved (`/private/var/…`). Assigning a value no option
// carries selects nothing, silently, so the next click dispatched into whatever
// was selected before. Two spellings of one directory is enough to send a run to
// the wrong checkout, so the endpoint answers with the one the list speaks.
func asGitListsIt(gitRoot, path string) string {
	list, err := ops.ListWorktrees(gitRoot)
	if err != nil {
		return path
	}
	want := ops.CanonicalRepo(path)
	for _, wt := range list {
		if ops.CanonicalRepo(wt.Path) == want {
			return wt.Path
		}
	}
	return path
}

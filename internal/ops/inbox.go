package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// EscalationItem is one escalation as the inbox shows it: addressable by
// project and step, which is what its file name already encodes
// (`<project>-<step>.md`, internal/step/escalation.go).
type EscalationItem struct {
	Project string        `json:"project"`
	Step    string        `json:"step"`
	Repo    string        `json:"repo"`
	Path    string        `json:"path"`
	Head    []string      `json:"head,omitempty"`
	Waiting time.Duration `json:"waiting_ns"`
}

// Inbox is "what is waiting for me" (canvas 2a) with both things that wait in
// it: human gates and escalations.
//
// F3 (D6) merged them because they are the same question from the user's side —
// something stopped and wants a person — and because keeping `review` alive as a
// fourth noun would spend the growth rule on a listing. They stay two fields
// rather than one flattened list so neither has to pretend to be the other: a
// gate has a live process waiting on the answer, an escalation does not.
type Inbox struct {
	Gates       []GateView       `json:"gates"`
	Escalations []EscalationItem `json:"escalations"`
	// Handoffs are runs that FINISHED and whose declared next leg nobody took.
	//
	// They belong in this box and not in the history for the reason the box
	// exists: it answers "what is blocked on me", and a fix sitting on a branch
	// because the run that publishes it was never started is blocked on a
	// person exactly the way an unapproved gate is.
	Handoffs []Handoff `json:"handoffs,omitempty"`
}

// Empty reports an inbox with nothing in it.
func (i Inbox) Empty() bool {
	return len(i.Gates) == 0 && len(i.Escalations) == 0 && len(i.Handoffs) == 0
}

// inboxWindow is how far back the box looks for a dropped thread. It matches
// the window the run history and the event stream already use; one number, so
// a handoff cannot be inside one and outside the other.
const inboxWindow = 7 * 24 * time.Hour

var escalationName = regexp.MustCompile(`^(.+)-([A-Za-z][A-Za-z0-9_]*)\.md$`)

// LoadInbox gathers gates and escalations.
//
// Gates come from the global index, which is the only file that sees other
// repositories. Escalations are per-repository files with no index of their
// own, so the repositories to look in are exactly the ones the index knows,
// plus the one the caller is standing in (which may have escalations and no
// runs recorded — an index rotated away, or a repo copied from elsewhere).
func (g GateLister) LoadInbox(localRepo string) (Inbox, error) {
	gates, err := g.ListGates()
	if err != nil {
		return Inbox{}, err
	}
	inbox := Inbox{Gates: gates}

	now := g.now()
	for _, repo := range g.reposInScope(localRepo) {
		list, lerr := ListEscalations(repo, "")
		if lerr != nil || !list.Exists {
			continue
		}
		for _, item := range list.Items {
			project, step := splitEscalationName(item.Path)
			esc := EscalationItem{Project: project, Step: step, Repo: repo, Path: item.Path, Head: item.Head}
			if st, serr := os.Stat(item.Path); serr == nil {
				esc.Waiting = now.Sub(st.ModTime())
			}
			inbox.Escalations = append(inbox.Escalations, esc)
		}
	}
	sort.SliceStable(inbox.Escalations, func(i, j int) bool {
		if inbox.Escalations[i].Waiting == inbox.Escalations[j].Waiting {
			return inbox.Escalations[i].Path < inbox.Escalations[j].Path
		}
		return inbox.Escalations[i].Waiting > inbox.Escalations[j].Waiting
	})
	// The same window the gates and the history use: a handoff older than that
	// is not a dropped thread any more, it is history.
	inbox.Handoffs = g.Handoffs(localRepo, inboxWindow)
	return inbox, nil
}

// reposInScope lists every repository a per-repository read has to open: the one
// the caller is standing in, plus the ones the global index knows.
//
// Shared by LoadInbox and LoadGateAudit on purpose. Both answer "what is on this
// machine" from files that have no index of their own, and two copies of this
// walk would drift — one of them would learn about a repository the other never
// looks at, and the difference would surface as an audit that cannot see a gate
// the inbox lists.
//
// An index that cannot be read yields just the local repository rather than an
// error: a broken $CORVEX_HOME must not make the repository under the cursor
// unreadable too. Sorted, so the reads happen in a stable order.
func (g GateLister) reposInScope(localRepo string) []string {
	seen := map[string]bool{}
	if localRepo != "" {
		seen[CanonicalRepo(localRepo)] = true
	}
	if views, err := g.Resolver.List(); err == nil {
		for _, v := range views {
			if v.Record.Repo != "" {
				seen[CanonicalRepo(v.Record.Repo)] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for repo := range seen {
		out = append(out, repo)
	}
	sort.Strings(out)
	return out
}

// Workspace is one repository this machine knows about, in the shape a screen
// needs it.
type Workspace struct {
	Path string `json:"path"`
	// Name is the directory's base name — what a person calls the repository.
	Name string `json:"name"`
	// Current marks the repository the UI was opened in. It is the default for
	// a dispatch, and the only one whose recipes are certain to be readable.
	Current bool `json:"current"`
}

// Workspaces is the repository list the UI offers, built from the SAME walk the
// inbox uses (reposInScope): the repository under the cursor plus every one the
// global run index has seen.
//
// It shares that walk deliberately. The inbox already shows gates from every
// repository — that is what makes one screen an answer to "what is waiting on
// me" rather than "what is waiting on me here" — and a dispatch list built from
// a different source would be able to show a gate for a repository it refuses to
// dispatch into. Shared, the two cannot disagree.
func (g GateLister) Workspaces(localRepo string) []Workspace {
	local := CanonicalRepo(localRepo)
	repos := g.reposInScope(localRepo)
	out := make([]Workspace, 0, len(repos))
	for _, repo := range repos {
		out = append(out, Workspace{Path: repo, Name: filepath.Base(repo), Current: repo == local})
	}
	return out
}

// FindEscalation resolves one escalation in a repository.
func FindEscalation(workDir, project, step string) (EscalationItem, error) {
	if strings.TrimSpace(step) == "" {
		return EscalationItem{}, fmt.Errorf("an escalation is addressed by project AND step: name it with --step (e.g. --step S02).\n"+
			"  → corvex gate list   shows which steps of %s are waiting", project)
	}
	path := filepath.Join(workDir, ".corvex", "escalations", project+"-"+strings.ToUpper(step)+".md")
	if _, err := os.Stat(path); err != nil {
		return EscalationItem{}, fmt.Errorf("no escalation for %s step %s (looked for %s)", project, step, path)
	}
	return EscalationItem{
		Project: project, Step: strings.ToUpper(step), Repo: workDir, Path: path, Head: escalationHead(path),
	}, nil
}

// ResolveEscalation closes an escalation. retry also puts the step back to
// PENDING, which is the difference between the two verdicts: approving says
// "I fixed the underlying problem, try again", rejecting says "I am not going
// to fix it", and only the first should make the step runnable.
//
// Deleting the file is what the escalation itself has always instructed a human
// to do by hand ("Delete this file when resolved"). Giving that instruction a
// command is the whole point of absorbing `review`.
func ResolveEscalation(workDir, project, step string, retry bool) (EscalationItem, error) {
	item, err := FindEscalation(workDir, project, step)
	if err != nil {
		return EscalationItem{}, err
	}
	if retry {
		if rerr := ResetTask(workDir, project, item.Step); rerr != nil {
			return EscalationItem{}, rerr
		}
	}
	if rerr := os.Remove(item.Path); rerr != nil {
		return EscalationItem{}, fmt.Errorf("removing %s: %w", item.Path, rerr)
	}
	return item, nil
}

// splitEscalationName recovers (project, step) from `<project>-<step>.md`.
// A project name may itself contain dashes, so the split is anchored at the
// last one — which is exactly how the writer builds it.
func splitEscalationName(path string) (project, step string) {
	base := filepath.Base(path)
	if m := escalationName.FindStringSubmatch(base); m != nil {
		return m[1], m[2]
	}
	return strings.TrimSuffix(base, ".md"), ""
}

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
}

// Empty reports an inbox with nothing in it.
func (i Inbox) Empty() bool { return len(i.Gates) == 0 && len(i.Escalations) == 0 }

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

	repos := map[string]bool{}
	if localRepo != "" {
		repos[canonicalRepo(localRepo)] = true
	}
	if views, verr := g.Resolver.List(); verr == nil {
		for _, v := range views {
			if v.Record.Repo != "" {
				repos[canonicalRepo(v.Record.Repo)] = true
			}
		}
	}

	now := g.now()
	for repo := range repos {
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
	return inbox, nil
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

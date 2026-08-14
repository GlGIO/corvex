package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Escalation is one open escalation file: where it lives, plus the head of its
// content so a caller can triage without opening it.
type Escalation struct {
	Path string
	// Head is the first few non-blank lines of the file (empty when the file
	// could not be read).
	Head []string
}

// EscalationList is the escalations directory as found on disk. Dir absent and
// Dir empty are kept apart on purpose: a caller may want to say something
// different when nothing was ever escalated than when a filter matched nothing.
type EscalationList struct {
	Dir    string
	Exists bool
	Items  []Escalation
}

// ListEscalations returns the open escalations in a workspace, sorted by file
// name. project filters to one project's files ("" means all). A missing
// escalations directory is not an error: it reports Exists false, because
// nothing has ever been escalated.
func ListEscalations(workDir, project string) (EscalationList, error) {
	dir := filepath.Join(workDir, ".corvex", "escalations")
	list := EscalationList{Dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return list, nil
		}
		return list, fmt.Errorf("reading escalations directory: %w", err)
	}
	list.Exists = true

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if project != "" && !strings.HasPrefix(name, project+"-") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(dir, name)
		list.Items = append(list.Items, Escalation{Path: path, Head: escalationHead(path)})
	}
	return list, nil
}

// escalationHead returns the non-blank lines among the first 4 lines of path.
// Blank lines are dropped, not treated as a terminator — a file whose 2nd line
// is empty still contributes lines 3 and 4.
func escalationHead(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var head []string
	lines := strings.SplitN(string(data), "\n", 6)
	for i, line := range lines {
		if i >= 4 || strings.TrimSpace(line) == "" {
			continue
		}
		head = append(head, line)
	}
	return head
}

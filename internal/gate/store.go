package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/giovannialves/corvex/internal/run"
)

// Open writes a gate as pending, claiming its path exclusively.
//
// O_EXCL rather than a plain create: two runs never share a (run id, step id)
// pair, so a collision means something is wrong — a resumed run re-opening a
// gate that was already answered, or two processes believing they own the same
// step. Failing loudly there is better than silently discarding a decision that
// somebody already made.
func Open(p Pending) error {
	path, err := Path(p.Repo, p.RunID, p.StepID)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("gate dir %s: %w", dir, err)
	}
	// Belt and braces: the run record's guard normally already exists one level
	// up, but a gate store must not depend on that having happened.
	if err := run.EnsureScratchIgnored(run.RecordsDir(p.Repo)); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("gate marshal %s/%s: %w", p.RunID, p.StepID, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("gate open %s: %w", path, err)
	}
	if _, err := f.Write(append(buf, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("gate write %s: %w", path, err)
	}
	return f.Close()
}

// Read loads one gate.
func Read(repo, runID, stepID string) (Pending, error) {
	path, err := Path(repo, runID, stepID)
	if err != nil {
		return Pending{}, err
	}
	return readFile(path)
}

func readFile(path string) (Pending, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Pending{}, err
	}
	var p Pending
	if err := json.Unmarshal(data, &p); err != nil {
		return Pending{}, fmt.Errorf("gate parse %s: %w", path, err)
	}
	return p, nil
}

// Decide writes the answer, atomically, and refuses to overwrite one that is
// already there.
//
// Temp-and-rename for the same reason the run record uses it: an approve killed
// halfway must leave the pending file intact rather than half a JSON object.
// Refusing to overwrite matters because the run may already have acted on the
// first decision — silently replacing it would make the file disagree with what
// actually happened.
func Decide(repo, runID, stepID string, d Decision) (Pending, error) {
	path, err := Path(repo, runID, stepID)
	if err != nil {
		return Pending{}, err
	}
	p, err := readFile(path)
	if err != nil {
		return Pending{}, err
	}
	if p.Decided() {
		return p, fmt.Errorf("gate %s/%s was already %s", runID, stepID, p.Decision.Verdict)
	}
	if !d.Verdict.IsValid() {
		return p, fmt.Errorf("gate %s/%s: unknown verdict %q", runID, stepID, d.Verdict)
	}
	if d.Verdict == Approved {
		if missing := MissingAcks(p.Evidence, d.Acked); len(missing) > 0 {
			return p, &UnreadError{RunID: runID, StepID: stepID, Missing: missing}
		}
	}
	p.Decision = &d
	buf, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return p, fmt.Errorf("gate marshal %s/%s: %w", runID, stepID, err)
	}
	if err := writeAtomic(path, append(buf, '\n')); err != nil {
		return p, err
	}
	return p, nil
}

// UnreadError is the approval lock firing: evidence marked required_reading was
// not acknowledged.
//
// A typed error rather than a string because both the CLI and (F7) the HTTP
// surface have to render the same list of what is missing, and neither should be
// parsing prose to find it.
type UnreadError struct {
	RunID   string
	StepID  string
	Missing []string
}

func (e *UnreadError) Error() string {
	return fmt.Sprintf("gate %s/%s: required reading not acknowledged: %s "+
		"(run `corvex gate show %s --step %s`, then repeat --ack for each)",
		e.RunID, e.StepID, strings.Join(e.Missing, ", "), e.RunID, e.StepID)
}

// List returns every gate recorded in a repository, newest first.
//
// Unreadable files are skipped rather than reported: one mangled gate left by an
// old crash must not hide every other gate that is genuinely waiting. A missing
// directory simply means no gates.
func List(repo string) ([]Pending, error) {
	dir := Dir(repo)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("gate list %s: %w", dir, err)
	}
	var out []Pending
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].OpenedAt.After(out[j].OpenedAt)
		}
		return out[i].StepID < out[j].StepID
	})
	return out, nil
}

// ListOpen returns only the gates still waiting for an answer.
func ListOpen(repo string) ([]Pending, error) {
	all, err := List(repo)
	if err != nil {
		return nil, err
	}
	open := all[:0:0]
	for _, p := range all {
		if !p.Decided() {
			open = append(open, p)
		}
	}
	return open, nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-")
	if err != nil {
		return fmt.Errorf("gate temp file in %s: %w", dir, err)
	}
	tmp := f.Name()
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmp) }
	if _, err := f.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("gate write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("gate sync %s: %w", tmp, err)
	}
	if err := f.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("gate chmod %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("gate close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("gate rename to %s: %w", path, err)
	}
	return nil
}

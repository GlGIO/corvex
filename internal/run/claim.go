package run

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// claimID picks an id and takes it, in one step.
//
// Uniqueness has to hold across the whole machine, not just this repository: the
// UI and the CLI address a run by bare id (`run show run_8f21`), so two runs
// minting the same id in different repositories would collapse into one row in
// the global index and one of them would disappear. Hence two oracles:
//
//  1. the global index — the ids this machine has announced that are still
//     addressable (see retention.go for why "still addressable" and not "ever").
//     Both the live file and the rotation archive: rotation renames one into the
//     other, and a claim that read only the live file during that rename saw
//     nothing and minted the id of a run alive in another repository. See
//     claimIndexEntries;
//  2. an O_EXCL create of the record path — which *claims* the id rather than
//     merely checking it, closing the window where two runs of the same project
//     start at the same moment, both see a free id, and both take it.
//
// The claim leaves a zero-length file behind until WriteRecord renames the real
// record over it. Readers skip it (it does not parse), which is the same
// tolerance that keeps a torn record from breaking a listing.
//
// Both oracles are scoped the same way. Scoping only the index would move the
// ceiling rather than remove it: the record files of a repository with 65,536
// runs behind it would refuse every candidate on their own.
func (r Registry) claimID(home string, now time.Time) (string, error) {
	retention := resolveRetention(r.Retention)
	announced, err := addressableIndexIDs(home, now, retention)
	if err != nil {
		return "", err
	}
	if err := prepareRecordsDir(r.Repo); err != nil {
		return "", err
	}

	var claimErr error
	id, err := uniqueID(r.NewID, func(candidate string) bool {
		path, perr := RecordPath(r.Repo, candidate)
		if perr != nil {
			claimErr = perr
			return true
		}
		if announced[candidate] {
			return true
		}
		free, cerr := claimRecordPath(path, now, retention)
		if cerr != nil {
			claimErr = cerr
			return true
		}
		return !free
	})
	if err != nil {
		if claimErr != nil {
			return "", fmt.Errorf("run registry: claiming a run id: %w", claimErr)
		}
		if errors.Is(err, ErrIDSpaceExhausted) {
			// The number is the actionable part: a handful means a broken
			// generator, tens of thousands means the machine really is full and
			// the retention window is the knob.
			return "", fmt.Errorf("%w (%d ids addressable on this machine; %s sets the retention window)",
				err, len(announced), RetentionEnv)
		}
		return "", err
	}
	// Housekeeping on the way in, not only on the way out: a run SIGKILLed while
	// paused leaves its control file behind, and an id is recyclable (see
	// retention.go). Without this, the next run to draw that id would read a
	// pause request written for a run that died weeks ago and stop at its first
	// wave for no reason anybody could see. The id is ours at this point, so the
	// file cannot belong to a live run.
	_ = ClearPause(r.Repo, id)
	return id, nil
}

// claimRecordPath takes an id by creating its record path exclusively. It reports
// whether the id is now ours.
//
// The retry after removing a recyclable record is what keeps a repository's own
// history from becoming a second ceiling. It is safe under a race: whoever wins
// the O_EXCL create owns the id, and the loser simply draws another candidate.
func claimRecordPath(path string, now time.Time, retention time.Duration) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		_ = f.Close()
		return true, nil
	}
	if !os.IsExist(err) {
		return false, err
	}
	if !recyclableRecord(path, now, retention) {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, nil // somebody else got there first: not our id
	}
	f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false, nil
	}
	_ = f.Close()
	return true, nil
}

// recyclableRecord reports whether a record file is dead weight: nothing has
// refreshed it for longer than the retention window.
//
// The predicate is age, not terminal status. A run killed with SIGKILL says
// `running` on disk forever, so keying on status would leak that id permanently —
// and a freshness a fortnight old means the heartbeat that would have moved it
// stopped a fortnight ago. A genuinely long-running run is refreshed every ten
// seconds and is never in scope.
//
// Records that do not parse (a zero-length claim from a Start that died between
// the claim and the write, a hand-mangled file) have no freshness to read, so the
// file's own mtime stands in, compared against the real clock — an mtime comes
// from the filesystem, and an injected clock has no business deciding whether a
// real file is old. A leftover claim from ten seconds ago still blocks its id; one
// from a fortnight ago does not.
func recyclableRecord(path string, now time.Time, retention time.Duration) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	rec, err := readRecordFile(path)
	if err != nil || !ValidID(rec.RunID) {
		return time.Since(st.ModTime()) > retention
	}
	return now.Sub(rec.Freshness()) > retention
}

// releaseID drops a claim whose run failed to start, so a Start that returns an
// error leaves nothing behind. Safe because the claim was created with O_EXCL:
// the file is ours, never a pre-existing record.
func (r Registry) releaseID(id string) {
	if path, err := RecordPath(r.Repo, id); err == nil {
		_ = os.Remove(path)
	}
}

// addressableIndexIDs is the first oracle: the ids the global index still answers
// for, read from the live file and the archive together so that a rotation in
// flight cannot make a live run invisible (see claimIndexEntries).
func addressableIndexIDs(home string, now time.Time, retention time.Duration) (map[string]bool, error) {
	entries, err := claimIndexEntries(home)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(entries))
	for _, rec := range ConsolidateIndex(entries) {
		if addressable(rec, now, retention) {
			ids[rec.RunID] = true
		}
	}
	return ids, nil
}

func prepareRecordsDir(repo string) error {
	dir := RecordsDir(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("run record dir %s: %w", dir, err)
	}
	return ensureRecordsIgnored(dir)
}

package run

import (
	"fmt"
	"os"
)

// claimID picks an id and takes it, in one step.
//
// Uniqueness has to hold across the whole machine, not just this repository: the
// UI and the CLI address a run by bare id (`run show run_8f21`), so two runs
// minting the same id in different repositories would collapse into one row in
// the global index and one of them would disappear. Hence two oracles:
//
//  1. the global index — every id this machine has ever announced;
//  2. an O_EXCL create of the record path — which *claims* the id rather than
//     merely checking it, closing the window where two runs of the same project
//     start at the same moment, both see a free id, and both take it.
//
// The claim leaves a zero-length file behind until WriteRecord renames the real
// record over it. Readers skip it (it does not parse), which is the same
// tolerance that keeps a torn record from breaking a listing.
func (r Registry) claimID(home string) (string, error) {
	announced, err := indexIDs(home)
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
		f, ferr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if ferr != nil {
			if !os.IsExist(ferr) {
				claimErr = ferr
			}
			return true
		}
		_ = f.Close()
		return false
	})
	if err != nil {
		if claimErr != nil {
			return "", fmt.Errorf("run registry: claiming a run id: %w", claimErr)
		}
		return "", err
	}
	return id, nil
}

// releaseID drops a claim whose run failed to start, so a Start that returns an
// error leaves nothing behind. Safe because the claim was created with O_EXCL:
// the file is ours, never a pre-existing record.
func (r Registry) releaseID(id string) {
	if path, err := RecordPath(r.Repo, id); err == nil {
		_ = os.Remove(path)
	}
}

func indexIDs(home string) (map[string]bool, error) {
	entries, err := ReadIndex(home)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(entries))
	for _, e := range entries {
		ids[e.RunID] = true
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

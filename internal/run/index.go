package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// HomeEnv overrides the corvex home directory. It exists so tests never
	// touch the real `~/.corvex` — a test that pollutes the user's home is a
	// product defect, not an inconvenience — and so a sandboxed run can be
	// pointed at a scratch home.
	HomeEnv = "CORVEX_HOME"

	// IndexFile is the global, cross-repository run index.
	IndexFile = "runs.jsonl"

	// homeDirPerm / indexFilePerm: the index sits in the user's home, which is
	// more exposed than a per-repository ledger, so it is owner-only even
	// though it carries no secret.
	homeDirPerm   os.FileMode = 0o700
	indexFilePerm os.FileMode = 0o600
)

// Home resolves the corvex home directory: $CORVEX_HOME when set, otherwise
// `~/.corvex`.
func Home() (string, error) {
	if v := strings.TrimSpace(os.Getenv(HomeEnv)); v != "" {
		return v, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("corvex home: %w (set %s)", err, HomeEnv)
	}
	return filepath.Join(h, ".corvex"), nil
}

// IndexPath is the index file inside a resolved home.
func IndexPath(home string) string { return filepath.Join(home, IndexFile) }

// IndexEntry is one line of runs.jsonl: a complete snapshot of a Record plus
// the moment the line was appended.
//
// Every line is a full snapshot, never a delta. That is what makes append-only
// enough: the end of a run is reflected by appending a second line, and readers
// consolidate by run_id with last-line-wins. Nothing ever rewrites or truncates
// the file, so a reader and a writer never fight, and a crash mid-append can
// only cost the last line — which ConsolidateIndex will simply not see.
type IndexEntry struct {
	Record
	WrittenAt time.Time `json:"written_at"`
}

// AppendIndex appends one snapshot line to the global index.
//
// Concurrency: the line is marshalled in full and handed to a single Write on a
// file opened O_APPEND, which is where the append-atomicity guarantee lives —
// the kernel resolves the offset and the write together, so two runs appending
// at the same moment produce two whole lines in some order rather than two
// interleaved halves. ReadIndex additionally skips any line that does not
// parse, so even a torn line (a full disk, a foreign writer) costs one run's
// snapshot, not the index.
func AppendIndex(home string, rec Record, at time.Time) error {
	if home == "" {
		return fmt.Errorf("run index: empty home path")
	}
	if !ValidID(rec.RunID) {
		return fmt.Errorf("run index: invalid run id %q", rec.RunID)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	buf, err := json.Marshal(IndexEntry{Record: rec, WrittenAt: at.UTC()})
	if err != nil {
		return fmt.Errorf("run index marshal %s: %w", rec.RunID, err)
	}
	if err := os.MkdirAll(home, homeDirPerm); err != nil {
		return fmt.Errorf("run index dir %s: %w", home, err)
	}
	path := IndexPath(home)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, indexFilePerm)
	if err != nil {
		return fmt.Errorf("run index open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(buf, '\n')); err != nil {
		return fmt.Errorf("run index write %s: %w", path, err)
	}
	return nil
}

// ReadIndex returns every parseable line of the live index, in file order. A
// missing index is not an error: it is a machine that has never started a run.
//
// Rotated lines (`runs.jsonl.1`) are not included. This is the LISTING view: a row
// that has left the live file is a row the cross-repository listing has stopped
// showing, which is the whole point of rotation. The id oracle needs the opposite
// answer and reads both files — see claimIndexEntries.
func ReadIndex(home string) ([]IndexEntry, error) {
	if home == "" {
		return nil, fmt.Errorf("run index: empty home path")
	}
	return readIndexFile(IndexPath(home))
}

// claimIndexEntries is what the id oracle reads: the live index AND the archive,
// always, not only while a rotation is in flight.
//
// Why both. Rotation renames the live file and appends the still-addressable
// snapshots back, so between those steps `runs.jsonl` is absent or partial. A
// claim landing in that gap saw an empty oracle and minted the id of a run that
// was alive in another repository — measured at 207 ms per rotation on a 6 MB
// index, 39 of 40 claims inside the gap, two of them taking the id of a live run.
// The record claim cannot cover it: O_EXCL is per repository, and the collapse
// this prevents is cross-repository by definition.
//
// Reading both files closes the gap BY CONSTRUCTION rather than by narrowing it.
// It does not widen what is addressable, because presence in a file was never the
// predicate: `addressable` is, and it is applied to the union exactly as it was
// applied to the live file. An id that rotation dropped is dropped because
// retention released it, and it stays released whether or not the archive still
// mentions it.
//
// The reads are ordered live-then-archive, and that order is load-bearing. If the
// archive were read first, a rotation could slip between the two reads and the
// second read would return the fresh, still-empty live file — losing exactly the
// ids the archive read was too early to have seen. Live first inverts it: either
// the live read got a complete file (in which case it already held everything
// addressable) or a rotation was in flight, and then the archive read finds the
// pre-rotation content the rename put there. Entries are returned archive-first so
// last-line-wins consolidation still sees the newest snapshot of each run last.
//
// The cost is one extra file read per run start, once, at the point where a run is
// already writing two files.
func claimIndexEntries(home string) ([]IndexEntry, error) {
	live, err := ReadIndex(home)
	if err != nil {
		return nil, err
	}
	archived, err := readArchivedIndex(home)
	if err != nil {
		return nil, err
	}
	return append(archived, live...), nil
}

// readArchivedIndex reads `runs.jsonl.1`, tolerating its absence and tolerating a
// path that is not a regular file.
//
// That second tolerance is not laziness. Something other than a file sitting at
// the archive path (a directory, a mount point) means no rotation ever succeeded
// there — the rename would have failed and been reported through
// Handle.MaintenanceErr — so nothing was ever moved out of the live index and the
// live index is complete on its own. Refusing to start any run on the machine
// because of it would be an outage invented to protect against a state that cannot
// exist. A regular file that cannot be READ is a different matter and is
// propagated: that one could be a real archive holding real ids.
func readArchivedIndex(home string) ([]IndexEntry, error) {
	if home == "" {
		return nil, fmt.Errorf("run index: empty home path")
	}
	path := IndexArchivePath(home)
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("run index archive %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return nil, nil
	}
	entries, err := readIndexFile(path)
	if err != nil {
		// Named as the archive, not just by path: the operator has to know that the
		// file blocking every run on this machine is rotation's own leftover and is
		// safe to move aside, which "run index read …/runs.jsonl.1" does not say.
		return nil, fmt.Errorf("run index archive unreadable: %w", err)
	}
	return entries, nil
}

// readIndexFile parses one index file, live or archived.
func readIndexFile(path string) ([]IndexEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("run index read %s: %w", path, err)
	}
	var out []IndexEntry
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e IndexEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // tolerate a torn or foreign line
		}
		if !ValidID(e.RunID) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ConsolidateIndex collapses the append-only log into one Record per run:
// the last line mentioning a run wins, since each line is a whole snapshot.
// Output is sorted newest-started first.
func ConsolidateIndex(entries []IndexEntry) []Record {
	idx := make(map[string]int, len(entries))
	var out []Record
	for _, e := range entries {
		if pos, ok := idx[e.RunID]; ok {
			out[pos] = e.Record
			continue
		}
		idx[e.RunID] = len(out)
		out = append(out, e.Record)
	}
	SortRecords(out)
	return out
}

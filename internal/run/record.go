package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Status is the state the run itself reports. Unknown values (a status written
// by a newer binary, or one F2 adds for gates) are treated as non-terminal, so
// liveness falls back to pid + heartbeat instead of guessing.
type Status string

const (
	StatusRunning Status = "running"
	StatusParked  Status = "parked" // blocked on a human gate; the process is still up

	// StatusPaused: somebody asked this run to stop between waves and it did.
	//
	// This is the visibility half of `run pause`. The two axes stay what they
	// are: the process is up and beating, so liveness is honestly `alive` — but
	// a run showing `alive` and NOTHING else while it is deliberately doing no
	// work is a lie of surface, because a reader counting capacity or waiting
	// for progress would draw the wrong conclusion from it. What changed is what
	// the run REPORTS about itself, which is exactly what the status axis is
	// for, so `run list --status paused` finds it and the listing says it out
	// loud.
	//
	// It is NOT StatusParked. Parked means a human gate is holding a step and
	// the run cannot proceed until somebody decides; paused means the run could
	// proceed and has been told not to. Merging them would make "waiting on a
	// decision" indistinguishable from "waiting on a person's permission to
	// continue", and the inbox is built on the first.
	StatusPaused Status = "paused"

	// StatusCanceling: a stop has been requested and the run has not closed yet.
	//
	// This state exists because the gap between the two is real and can be long. A
	// Ctrl-C cancels the context; the run then has to unwind — and when the
	// teardown only kills the direct child, a grandchild holding the run's stdout
	// can keep the body blocked indefinitely. Without a state for it, a supervisor
	// reading the record cannot tell "working" from "already asked to stop", so it
	// sees a fresh heartbeat and a live pid and concludes `alive`. That is what the
	// audit measured for twenty seconds and then gave up on.
	//
	// It is NOT a gate state and has nothing to do with StatusParked: nobody is
	// waiting for a human, the run is on its way out.
	StatusCanceling Status = "canceling"

	StatusDone Status = "done"
	// StatusPartial is a run that ended with nothing failing and work still
	// PENDING: `--task S03`, `--single`, a resume that ran the two steps that
	// were left of nine.
	//
	// It exists because `done` is the word a person scanning the history reads
	// as "this recipe finished". MEASURED on the first real run of the incident
	// recipe: `run start incident --task S01` executed one step of nine, wrote
	// `done`, and the history said `incident · done` — the detail screen knew it
	// was 1/9 and the line everybody reads first did not. `partial` was already
	// the word this codebase used for the same idea in the post-run hook
	// (CORVEX_STATUS), so it is a vocabulary the tool already had and the record
	// did not.
	StatusPartial  Status = "partial"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
)

// IsTerminal reports whether the run reached an end state on its own.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusDone, StatusPartial, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

// Record is the on-disk identity of one run. Field set is deliberately closed:
// there is nowhere to put an environment variable, a token or a resolved
// allowlist.
//
// Host and Machine both name where the run lives and only one of them decides
// anything. Machine is the stable id from $CORVEX_HOME (see MachineID) and is
// what liveness keys on; Host is the hostname, kept because it is what a human
// recognises in a listing, and ignored when a machine id is available on both
// sides — the hostname is rewritten by the network (`box.local` becomes
// `box-2.local` on a name collision), and a run's identity cannot depend on that.
// Records written before machine ids existed carry only Host, so the hostname
// remains the fallback.
type Record struct {
	RunID   string `json:"run_id"`
	Repo    string `json:"repo"`              // absolute path of the git root
	Recipe  string `json:"recipe,omitempty"`  // recipe-driven run
	Project string `json:"project,omitempty"` // legacy spec.md path
	PID     int    `json:"pid"`
	Host    string `json:"host,omitempty"` // hostname, for humans to read: it is not stable
	Machine string `json:"machine,omitempty"`
	Status  Status `json:"status"`

	// Environment is what the run needed standing around it (F6): `simple`
	// (nothing) or `stack` (the validate: stack, database included). Empty on
	// every record written before F6, which reads as `simple` — the only value
	// those runs could have had.
	//
	// It lives here rather than in the ledger for the reason the whole file
	// exists: this is machine-local scratch, gitignored with `*`, and "which
	// containers this machine stood up" is a fact about the machine.
	Environment string `json:"environment,omitempty"`

	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Freshness is the newest timestamp the record carries. A record written
// before the first heartbeat has no UpdatedAt yet, and started_at is then the
// honest answer.
func (r Record) Freshness() time.Time {
	if r.UpdatedAt.IsZero() {
		return r.StartedAt
	}
	return r.UpdatedAt
}

// RecordsDir is where per-run records live: `<repo>/.corvex/runs`.
//
// One file per run (rather than one shared file) is what makes the directory
// safe under concurrency: two runs of the same project never write the same
// path, so neither can corrupt the other's record, and a reader listing the
// directory sees each run either at its previous complete state or at its new
// one — never a merge of the two.
func RecordsDir(repo string) string {
	return filepath.Join(repo, ".corvex", "runs")
}

// RecordPath is the record file of one run. It fails on a malformed id rather
// than building a path that escapes RecordsDir.
func RecordPath(repo, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("run record: invalid run id %q", id)
	}
	if repo == "" {
		return "", fmt.Errorf("run record: empty repo path")
	}
	return filepath.Join(RecordsDir(repo), id+".json"), nil
}

// WriteRecord writes rec atomically to its record path.
//
// Atomic means: marshal, write to a temp file in the same directory, fsync,
// close, rename. Rename within a directory is atomic, so a process killed at
// any point either leaves the previous record intact or the new one complete.
// A half-written temp file is never named `<run_id>.json`, and ReadRecords
// ignores dotfiles, so it cannot break a listing either.
func WriteRecord(rec Record) error {
	path, err := RecordPath(rec.Repo, rec.RunID)
	if err != nil {
		return err
	}
	buf, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("run record marshal %s: %w", rec.RunID, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("run record dir %s: %w", dir, err)
	}
	if err := ensureRecordsIgnored(dir); err != nil {
		return err
	}
	return writeFileAtomic(path, append(buf, '\n'))
}

// ReadRecord reads one run's record.
func ReadRecord(repo, id string) (Record, error) {
	path, err := RecordPath(repo, id)
	if err != nil {
		return Record{}, err
	}
	return readRecordFile(path)
}

// ReadRecords returns every readable record in a repository, newest run first.
//
// Unreadable entries are skipped, not reported as errors: a truncated or
// hand-mangled JSON file left by some earlier crash must not take down the
// listing of every other run. Only a failure to list the directory itself is
// an error (and a missing directory is simply "no runs").
func ReadRecords(repo string) ([]Record, error) {
	dir := RecordsDir(repo)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("run records %s: %w", dir, err)
	}
	var out []Record
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		rec, err := readRecordFile(filepath.Join(dir, name))
		if err != nil || !ValidID(rec.RunID) {
			continue
		}
		if rec.Repo == "" {
			rec.Repo = repo
		}
		out = append(out, rec)
	}
	SortRecords(out)
	return out, nil
}

// SortRecords orders records newest-started first, breaking ties by id so the
// output is deterministic (map iteration and ReadDir order are not).
func SortRecords(recs []Record) {
	sort.SliceStable(recs, func(i, j int) bool {
		if !recs[i].StartedAt.Equal(recs[j].StartedAt) {
			return recs[i].StartedAt.After(recs[j].StartedAt)
		}
		return recs[i].RunID < recs[j].RunID
	})
}

func readRecordFile(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("run record read %s: %w", path, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, fmt.Errorf("run record parse %s: %w", path, err)
	}
	return rec, nil
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-")
	if err != nil {
		return fmt.Errorf("run record temp file in %s: %w", dir, err)
	}
	tmp := f.Name()
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmp) }
	if _, err := f.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("run record write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("run record sync %s: %w", tmp, err)
	}
	if err := f.Chmod(0o644); err != nil {
		cleanup()
		return fmt.Errorf("run record chmod %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("run record close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("run record rename to %s: %w", path, err)
	}
	return nil
}

// ensureRecordsIgnored drops a `*` .gitignore inside `.corvex/runs/` the first
// time a record is written there. Records hold a pid and an absolute path of
// this machine: they are per-machine scratch, and committing them would put
// local state into the user's history. `.corvex/.gitignore` is already the
// project's convention for this (doctor checks it for mcp.json).
func ensureRecordsIgnored(dir string) error {
	return EnsureScratchIgnored(dir)
}

// EnsureScratchIgnored is ensureRecordsIgnored for anything else that writes
// per-machine scratch under `.corvex/runs/`.
//
// Exported for internal/gate, which stores gate state in a subdirectory of it.
// The `*` pattern covers subdirectories, so a gate file inherits the property
// without a second .gitignore — but the gate store calls this anyway rather than
// depending on a record having been written first, because "somebody else
// already created the guard" is not something a writer of sensitive scratch
// should assume.
func EnsureScratchIgnored(dir string) error {
	path := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, []byte("*\n"), 0o644); err != nil {
		return fmt.Errorf("run records gitignore %s: %w", path, err)
	}
	return nil
}

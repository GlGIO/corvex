package run

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Registry starts runs: it mints an id, writes the local record and announces
// the run in the global index.
//
// Every source of non-determinism is a field, so a test fixes the id, the clock
// and the pid instead of hoping the observed value is stable.
type Registry struct {
	// Repo is the absolute path of the git root. This package records the
	// repository, it does not discover it — resolving the git root belongs to
	// the caller (ops.FindGitRoot), which keeps internal/run free of
	// internal/ops.
	Repo string
	// Home overrides the corvex home; empty resolves via Home().
	Home string
	// NewID defaults to DefaultIDFunc.
	NewID IDFunc
	// Now defaults to time.Now.
	Now func() time.Time
	// PID defaults to os.Getpid().
	PID int
	// Host defaults to the machine hostname.
	Host string
	// Machine defaults to the id stored in Home (see MachineID).
	Machine string
	// Retention is how long a finished run keeps its id; 0 resolves via
	// $CORVEX_RUN_RETENTION, then DefaultRetention.
	Retention time.Duration
	// IndexMaxBytes is the size at which the global index is rotated; 0 resolves
	// via $CORVEX_RUN_INDEX_MAX_BYTES, then DefaultIndexMaxBytes.
	IndexMaxBytes int64
}

// StartOptions describes the run being started. Exactly one of Recipe or
// Project is expected: Recipe for the recipe-driven path, Project for the
// legacy `corvex run <project>` / spec.md path, which must keep working.
type StartOptions struct {
	Recipe  string
	Project string
	// Status defaults to StatusRunning.
	Status Status
}

// Handle is a started run's writable side. It owns exactly one record file and
// serialises its own writes; it is safe to share with a heartbeat goroutine.
type Handle struct {
	mu   sync.Mutex
	rec  Record
	home string
	now  func() time.Time

	maintErr error
}

// Start mints a run and puts it on disk.
//
// Order is deliberate: the local record lands first, then the index line. If
// the process dies between the two, the run is discoverable in its repository
// but missing from the global list — a listing that lacks a row. The reverse
// order would advertise a run whose record never existed, which every reader
// would then have to treat as a special case.
func (r Registry) Start(opts StartOptions) (*Handle, error) {
	if !filepath.IsAbs(r.Repo) {
		return nil, fmt.Errorf("run registry: repo must be an absolute path, got %q", r.Repo)
	}
	// Refuse a repo that is not there: MkdirAll would happily invent
	// `<typo>/.corvex/runs` and the run would be recorded against a directory
	// tree nobody asked for.
	if st, err := os.Stat(r.Repo); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("run registry: repo %s is not an existing directory", r.Repo)
	}
	home, err := r.home()
	if err != nil {
		return nil, err
	}
	now := r.clock()
	at := now().UTC()

	// Housekeeping first, so the id oracle sees the pruned index. It is
	// best-effort by design: a run must not be refused because the index could not
	// be rotated (nothing is lost, the file just stays big), which is the opposite
	// of the identity failure below. The error is not swallowed either — it travels
	// on the handle for the caller to report.
	maintErr := rotateIndex(home, resolveIndexMax(r.IndexMaxBytes), resolveRetention(r.Retention), at)

	id, err := r.claimID(home, at)
	if err != nil {
		return nil, err
	}

	status := opts.Status
	if status == "" {
		status = StatusRunning
	}
	rec := Record{
		RunID:     id,
		Repo:      r.Repo,
		Recipe:    opts.Recipe,
		Project:   opts.Project,
		PID:       r.pid(),
		Host:      r.hostName(),
		Machine:   r.machineID(home),
		Status:    status,
		StartedAt: at,
		UpdatedAt: at,
	}
	if err := WriteRecord(rec); err != nil {
		r.releaseID(id)
		return nil, err
	}
	if err := AppendIndex(home, rec, at); err != nil {
		// A run that could not be announced is a run nobody can list: fail, and
		// leave no half-registered record or burnt id behind.
		r.releaseID(id)
		return nil, err
	}
	return &Handle{rec: rec, home: home, now: now, maintErr: maintErr}, nil
}

// MaintenanceErr is why index housekeeping failed for this run, or nil. It never
// affects the run; it exists so "the index is not being pruned" can be reported
// instead of discovered a year later.
func (h *Handle) MaintenanceErr() error {
	if h == nil {
		return nil
	}
	return h.maintErr
}

// Record returns a copy of the current record.
func (h *Handle) Record() Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rec
}

// RunID is the run's id.
func (h *Handle) RunID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rec.RunID
}

// Touch refreshes updated_at in the local record. This is the heartbeat's work.
//
// It does not append to the global index: at one line per 10 seconds per run
// the index would grow without bound, and the consolidated view would carry a
// timestamp it does not need. Readers get freshness by overlaying the local
// record (see Resolver.List), which is a read of a small file instead of an
// unbounded append.
func (h *Handle) Touch() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rec.UpdatedAt = h.now().UTC()
	return WriteRecord(h.rec)
}

// SetStatus records a new status locally and appends a snapshot line to the
// global index, which is how the end of a run is reflected in an append-only
// file: a second line, never a rewrite of the first.
func (h *Handle) SetStatus(s Status) error {
	if s == "" {
		return fmt.Errorf("run %s: empty status", h.RunID())
	}
	h.mu.Lock()
	at := h.now().UTC()
	h.rec.Status = s
	h.rec.UpdatedAt = at
	rec := h.rec
	home := h.home
	h.mu.Unlock()

	if err := WriteRecord(rec); err != nil {
		return err
	}
	return AppendIndex(home, rec, at)
}

// StartHeartbeat wires Touch to a ticker. Callers must Stop it — normally with
// a defer next to the call, before recording the terminal status.
func (h *Handle) StartHeartbeat(interval time.Duration) *Heartbeat {
	return StartHeartbeat(HeartbeatOptions{Beat: h.Touch, Interval: interval})
}

func (r Registry) home() (string, error) {
	if r.Home != "" {
		return r.Home, nil
	}
	return Home()
}

func (r Registry) clock() func() time.Time {
	if r.Now != nil {
		return r.Now
	}
	return time.Now
}

func (r Registry) pid() int {
	if r.PID != 0 {
		return r.PID
	}
	return os.Getpid()
}

func (r Registry) hostName() string {
	if r.Host != "" {
		return r.Host
	}
	return hostname()
}

// machineID mints this machine's identity on first use. A home that cannot be
// written to is not fatal here — the record falls back to being identified by
// hostname, which is worse but not nothing, and Start is about to fail on the
// index append anyway if the home is truly unusable.
func (r Registry) machineID(home string) string {
	if r.Machine != "" {
		return r.Machine
	}
	if id, err := MachineID(home); err == nil {
		return id
	}
	return ""
}

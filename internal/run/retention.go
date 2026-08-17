package run

// Retention: what keeps a 16-bit id space from becoming a ceiling.
//
// `run_8f21` is 4 hex digits, 65,536 slots, and the width is UI surface (it is in
// the roadmap, the canvas and every mock) — widening it is not this file's
// business. What was broken is the other end: uniqueness was enforced against
// every id the machine had EVER announced, in a file that never forgot. At 94%
// occupancy the audit measured 13 of 20 runs failing to mint an id at all.
//
// Uniqueness does not need to hold against the whole history of the machine. It
// needs to hold against the ids that can still be ADDRESSED: the runs that have
// not finished, held far longer than a finished one because the index gets no
// heartbeat and a week-long run's line looks ancient while the process is working,
// plus the runs that finished recently enough that somebody might still type their
// id. Both windows have an end — see addressable for why a line that has not moved
// in four months is not a slow run, and for what it cost to pretend otherwise.
//
// # The consequence, stated on purpose
//
// An id can be reused. `run show run_8f21` for a run that closed a month ago may
// find nothing, or find a newer run wearing the same id. That is a real loss, and
// it is the better half of the trade: the alternative is a tool that mints no id
// at all after run 65,536 and lies about identity for thousands of runs before
// that. Runs that leave the live index are not deleted — they are moved to
// `runs.jsonl.1`, so the history is still on disk for anyone debugging, it is just
// no longer LISTED.
//
// # Rotation, not rewriting
//
// Pruning could rewrite the index in place (tmp + rename). It is not done that
// way. Append atomicity — O_APPEND plus a single write — is what lets eight
// concurrent processes share this file without a lock, and a rewrite has a window
// where a line another process appends through an fd it already holds lands in an
// inode that is about to be unlinked: the run is announced and then silently
// forgotten. Renaming has no such window. Every line either stays in the live
// file or moves to the archive, and the snapshots that must remain addressable
// are appended back through the same atomic path everyone else uses.
//
// Only one archive generation is kept: unbounded archives would defeat the point of
// bounding the file.
//
// # What rotation must not do, and how it is stopped
//
// The rename and the carry-forward are not one operation, so there is a stretch —
// measured at 207 ms on a 6 MB index — in which `runs.jsonl` is absent or partial.
// The global index is the only oracle that can see a run in another repository, so
// a claim landing in that stretch could mint the id of a live run: measured, 39 of
// 40 claims inside it, two taking the id of a run that was up.
//
// Two things close it, and neither of them narrows the window — narrowing a window
// is not closing it.
//
//  1. The id oracle reads the live file AND the archive, always (claimIndexEntries).
//     Presence in a particular file was never the predicate; `addressable` is, and
//     it is applied to the union. During the rename the archive holds what the live
//     file held a moment ago, so nothing can disappear.
//  2. A rotation completes the previous one before it renames over the archive
//     (resumeCarryForward), because the rename destroys the only remaining copy of
//     anything a killed rotation left there.
//
// Together they also subsume the two windows the first version of this file
// declared survivable: a line appended through an fd opened just before the rename
// lands in the archive, where the oracle can see it and where the next rotation
// carries it forward rather than erasing it; and a rotation that dies after the
// rename (a full disk mid carry-forward) leaves the addressable set in the archive,
// which is now read and later repaired. The error is still reported rather than
// swallowed — see Handle.MaintenanceErr.
//
// The cost is one extra file read per run start. That is the whole bill.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultRetention is how long a finished run keeps its id.
	//
	// Two weeks. The number is a compromise between two failure modes with very
	// different costs. Too short and a human referring to "that run from last
	// week" gets nothing — cheap to say, annoying to hit. Too long and the
	// addressable set creeps back towards the size of the id space, which is the
	// failure this whole file exists to prevent. Two weeks covers "last week" and
	// "the run before I went away for the weekend" while keeping the set tiny:
	// even at a heavy 200 runs a day it holds ~2,800 ids, 4% of the space, so
	// eight consecutive collisions have a probability around 1e-11. At 500 runs a
	// day it is 11% and still 1e-8. Override it if your volume or your memory
	// differs.
	DefaultRetention = 14 * 24 * time.Hour

	// DefaultIndexMaxBytes is the size at which the index is rotated.
	//
	// 2 MiB. Lines are ~300 bytes and a run costs two of them (start, end), so
	// this holds roughly 3,500 runs — the same order as the retention window at
	// realistic volume, which is what keeps rotation from throwing away runs that
	// retention would have kept. It is also the read cost paid by every run start
	// and every listing: parsing 2 MiB of JSON is a few milliseconds, parsing the
	// 20 MiB the audit produced is not.
	DefaultIndexMaxBytes int64 = 2 << 20

	// RetentionEnv and IndexMaxBytesEnv override the two thresholds. Env vars
	// rather than flags: the command surface is F3's, and these are machine
	// policy, not per-invocation choices.
	RetentionEnv     = "CORVEX_RUN_RETENTION"
	IndexMaxBytesEnv = "CORVEX_RUN_INDEX_MAX_BYTES"

	// staleNonTerminalFactor is how many retention windows a run may go without a
	// single index line before the index stops holding its id for it.
	//
	// Eight, so 112 days at the default fortnight. Derived from the retention window
	// rather than written as an absolute duration for one reason: the error a full
	// machine returns names CORVEX_RUN_RETENTION as the knob, and until this factor
	// existed that was a lie for the case that actually bricks a machine — orphaned
	// `running` lines, which no retention setting could release. Now the knob really
	// does govern both populations, and the message is true.
	//
	// Why eight and not two. The index gets a line at start and at each status
	// change, so the age of a line is the age of the last status change, not the age
	// of the last sign of life. A run that has been working for a month has a
	// month-old line and must keep its id. Four months of total silence in the index
	// is not a slow run: it is a machine that has rebooted, or a process that was
	// killed, or a laptop that was closed. Anything smaller starts trading a real
	// guarantee for a hypothetical one.
	staleNonTerminalFactor = 8

	indexArchiveSuffix = ".1"
	rotateLockSuffix   = ".rotating"

	// rotateLockStale bounds the damage of a process dying mid-rotation: after
	// this long the lock is assumed abandoned, so one crash cannot stop
	// housekeeping forever.
	rotateLockStale = 5 * time.Minute
)

// IndexArchivePath is where rotated index lines go.
//
// The LISTING does not consult it — a row that left the live file is a row the
// cross-repository listing has stopped showing, which is what rotation is for. The
// ID ORACLE does consult it, on every run start, because a line's file says nothing
// about whether its id is free. See claimIndexEntries.
func IndexArchivePath(home string) string { return IndexPath(home) + indexArchiveSuffix }

// resolveRetention: an explicit field wins over the environment, which wins over
// the default. A malformed or non-positive env value is ignored rather than read
// as zero — zero would recycle the id of a run that closed a millisecond ago.
func resolveRetention(field time.Duration) time.Duration {
	if field > 0 {
		return field
	}
	if v := strings.TrimSpace(os.Getenv(RetentionEnv)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return DefaultRetention
}

// resolveIndexMax follows the same precedence.
func resolveIndexMax(field int64) int64 {
	if field > 0 {
		return field
	}
	if v := strings.TrimSpace(os.Getenv(IndexMaxBytesEnv)); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return DefaultIndexMaxBytes
}

// addressable reports whether a snapshot still holds its run id.
//
// A run that never recorded an end keeps its id almost regardless of age. That is
// not generosity: the index gets a line at start and at each status change and
// nothing in between — no heartbeat ever reaches it — so a run that has been
// working for three weeks has a three-week-old line. Ageing it out on the same
// window as a finished run would hand its id to a second run and collapse two live
// runs into one row.
//
// "Almost", because the previous version of this predicate said `true` at any age
// and that was a way to brick the machine. A run SIGKILLed while `running` says
// `running` on disk forever, and its line holds its id forever. Nothing reclaims
// it: worthRotating refuses to rotate an index whose every line is addressable, so
// a machine that accumulated 65,536 orphaned `running` lines was measured with a
// 17.9 MB index that would not rotate, could not mint an id, and refused every
// run — while the error told the operator to adjust CORVEX_RUN_RETENTION, which
// touched none of it. There was no way out but deleting the file by hand.
//
// So a non-terminal line does age out, at staleNonTerminalFactor times the
// retention window. The asymmetry is the point: the factor is not a tolerance for
// slow runs, it is the distance past which "still running" stops being credible.
//
// What it costs, stated plainly. If a run really has been going for that long, its
// id can be minted by a run in ANOTHER repository, and the two collapse into one
// index row. It cannot be taken by a run in its own repository: the record file
// there is refreshed by the heartbeat every ten seconds and claimRecordPath refuses
// a record younger than the retention window. So the exposure is one cross-repo
// mint against a run that has been silent in the index for months, against the
// alternative of a machine that can never start a run again. That is not a close
// call.
func addressable(rec Record, now time.Time, retention time.Duration) bool {
	if !rec.Status.IsTerminal() {
		return now.Sub(rec.Freshness()) < retention*staleNonTerminalFactor
	}
	return now.Sub(rec.Freshness()) < retention
}

// worthRotating reports whether rotation would shrink anything: some run has left
// the addressable set, or some run holds more than one line and consolidation will
// collapse it.
func worthRotating(entries []IndexEntry, now time.Time, retention time.Duration) bool {
	keep := ConsolidateIndex(entries)
	if len(keep) < len(entries) {
		return true
	}
	for _, rec := range keep {
		if !addressable(rec, now, retention) {
			return true
		}
	}
	return false
}

// rotateIndex archives the index and carries the still-addressable snapshots
// forward, when the file has grown past max. It is housekeeping: a no-op when
// there is nothing to do, and never a reason to refuse a run.
func rotateIndex(home string, max int64, retention time.Duration, now time.Time) error {
	if home == "" || max <= 0 {
		return nil
	}
	path := IndexPath(home)
	if st, err := os.Stat(path); err != nil || st.Size() < max {
		return nil
	}

	lock := path + rotateLockSuffix
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, indexFilePerm)
	if err != nil {
		// Another process holds it, or a dead one left it behind. The age of the
		// lock is compared against the real clock rather than the injected one:
		// its mtime comes from the filesystem, and mixing the two would make a
		// test's frozen clock decide whether a real file is old.
		if st, serr := os.Stat(lock); serr == nil && time.Since(st.ModTime()) > rotateLockStale {
			_ = os.Remove(lock)
		}
		return nil
	}
	_ = f.Close()
	defer func() { _ = os.Remove(lock) }()

	// Re-check under the lock: whoever held it may have just rotated.
	if st, err := os.Stat(path); err != nil || st.Size() < max {
		return nil
	}

	// Is there anything to gain? A file over the threshold whose every line is a
	// distinct, still-addressable run cannot be made smaller: rotating it would
	// archive live runs and then write the same content straight back, on every
	// run start from then on. An index that big is a machine with that many live
	// runs, and the honest answer is to leave it alone.
	before, readErr := readIndexFile(path)
	if readErr == nil && !worthRotating(before, now, retention) {
		return nil
	}

	// Only one archive generation is kept, so the rename below DESTROYS whatever is
	// in the archive now. That is safe when the last rotation finished — everything
	// addressable in it was appended back into the live file — and unsafe when the
	// last rotation did not: a process killed between its rename and its
	// carry-forward leaves the addressable set in the archive alone, and overwriting
	// it would be the one way an id could still go missing while its run is alive.
	// So finish that rotation before starting this one, reusing the live snapshot
	// already read above rather than reading a multi-megabyte file twice inside the
	// same lock.
	if err := resumeCarryForward(home, before, now, retention); err != nil {
		return err
	}

	archive := IndexArchivePath(home)
	if err := os.Rename(path, archive); err != nil {
		return fmt.Errorf("run index rotate %s: %w", path, err)
	}

	entries, err := readIndexFile(archive)
	if err != nil {
		return err
	}
	// Consolidate first: one line per run instead of one per status change, which
	// is where most of the compaction comes from. Oldest first, so the live file
	// stays in the chronological order every reader expects.
	keep := ConsolidateIndex(entries)
	for i := len(keep) - 1; i >= 0; i-- {
		if !addressable(keep[i], now, retention) {
			continue
		}
		if err := AppendIndex(home, keep[i], now); err != nil {
			return fmt.Errorf("run index carry forward %s: %w", keep[i].RunID, err)
		}
	}
	return nil
}

// resumeCarryForward completes an interrupted rotation: any snapshot that is still
// addressable, sits in the archive, and has no line of its own in the live file is
// appended back before the archive is overwritten.
//
// After a rotation that ran to completion this appends nothing, which is the normal
// case and the reason it is cheap: the carry-forward already put every addressable
// snapshot in the live file, so every id is found there. It earns its keep in two
// abnormal cases — a process killed between the rename and the carry-forward, and a
// line that a concurrent writer appended through an fd it opened just before the
// rename (that line lands in the archive; the id oracle can see it, and this is
// what stops it being erased by the next rotation).
//
// Presence, not freshness, is the test: a run whose end was recorded after the
// rotation has a newer line in the live file, and re-appending its older snapshot
// would make consolidation report it as running again.
func resumeCarryForward(home string, live []IndexEntry, now time.Time, retention time.Duration) error {
	archived, err := readArchivedIndex(home)
	if err != nil || len(archived) == 0 {
		return err
	}
	known := make(map[string]bool, len(live))
	for _, e := range live {
		known[e.RunID] = true
	}
	keep := ConsolidateIndex(archived)
	for i := len(keep) - 1; i >= 0; i-- {
		if known[keep[i].RunID] || !addressable(keep[i], now, retention) {
			continue
		}
		if err := AppendIndex(home, keep[i], now); err != nil {
			return fmt.Errorf("run index resume carry forward %s: %w", keep[i].RunID, err)
		}
	}
	return nil
}

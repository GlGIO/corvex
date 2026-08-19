package ops

import (
	"math"
	"sort"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/types"
)

// The sensor over the sensors (roadmap risk 1): measuring the gates themselves
// instead of what they guard. A gate approved in four seconds, every time, is
// theatre — and nothing in this tool could see that before this file existed.
//
// # Why the gate files and not the ledger
//
// The obvious cheap move is a column on `inspect --json`, and it was refused
// after measuring what the ledger actually holds. Three reasons, each fatal on
// its own:
//
//  1. `verdictStatus` (internal/step/human_gate.go:114-119) maps rejected AND
//     expired onto the same FAILED. A rejection rate read from the ledger
//     therefore counts "nobody showed up" as "a human judged and said no" —
//     which inverts the finding: an abandoned gate would look like the most
//     rigorous one on the screen.
//  2. The `--approve-gates` path emits gate_decided with NO duration
//     (human_gate.go:59-67, deliberately). A missing duration_ms is an absent
//     key, so in a p50 it is indistinguishable from a human who approved
//     instantly — the exact population this sensor exists to isolate.
//  3. The ledger has no reading mark of any kind. The gap between "read the
//     migration" and "approved it", which is the only number that separates a
//     review from a keystroke, is not expressible there at all.
//
// Two structural reasons on top: InspectReport/InspectTaskStat is a frozen CLI
// contract (inspect.go:16), and BuildInspectReport DISCARDS any ledger entry
// whose task_id is absent from tasks.md (inspect.go:65-68) — which is precisely
// a gate on a fan-out step, whose id (`S06/003/apply`) never appears there. The
// repo's own precedent is RunTaskRow, minted rather than widening
// InspectTaskStat.
//
// So the substrate is the gate FILE: one JSON per gate under
// `<repo>/.corvex/runs/gates/`, written by gate.Open and rewritten atomically by
// gate.Decide/MarkRead. It carries both timestamps and the reading marks.
//
// # What this cannot group, and why it does not pretend to
//
// A gate has no stable identity across executions. The key here is
// `(recipe, step_id)` — `(project, step_id)` when the run had no recipe —
// printed as "<recipe>/<step_id>". Fan-out suffixes are NOT collapsed: reading
// `S06/003/apply` as "the apply gate of S06" needs the grammar of task ids,
// which belongs to another package, and would assert a grouping the disk never
// states. If the question is instead "this gate as DECLARED in the recipe", the
// right key is the gate's index inside its step — and the file does not store
// it. Adding that field is a change to what is written on disk, which is the
// owner's call, not this reader's.
//
// # What this deliberately does not store
//
// Evidence. `gate show --json` embeds gate.Pending whole because a person asked
// to read it; an audit is a different act with a different blast radius, and
// Evidence.Content carries diffs, test output and production schema names. The
// audit keeps `label` — the user's own words — plus counts. Not even the
// rejection reason, which is free text typed at a moment of annoyance.

// P95MinSample is the smallest population allowed to report a 95th percentile.
//
// There is exactly ONE decided human gate on disk in this repository today. A
// p95 over one sample is theatre about theatre: it would print a number with the
// authority of a distribution and the content of a single anecdote. Below the
// threshold the field is absent, the min/median/max are still emitted, and the
// raw rows are always emitted — so the reader can count the population by hand.
const P95MinSample = 20

// GateAuditRow is one gate as the audit sees it: the two timestamps, the verdict,
// and the two counts that say whether reading was ever involved.
type GateAuditRow struct {
	// Key groups rows across executions — see the package comment for what it
	// can and cannot claim.
	Key     string           `json:"key"`
	RunID   string           `json:"run_id"`
	StepID  string           `json:"step_id"`
	Repo    string           `json:"repo"`
	Project string           `json:"project,omitempty"`
	Recipe  string           `json:"recipe,omitempty"`
	Nature  types.GateNature `json:"nature"`
	// Label is the user's own words for this gate, and the only free text here.
	Label string `json:"label,omitempty"`
	// Verdict is empty while the gate is still open.
	Verdict   gate.Verdict `json:"verdict,omitempty"`
	OpenedAt  time.Time    `json:"opened_at"`
	DecidedAt *time.Time   `json:"decided_at,omitempty"`
	// LatencyMs is decided_at − opened_at, from the two stamps on disk and never
	// from now(). It measures lunch, sleep and time zones as well as
	// deliberation: a gate approved in six hours may have been read in two
	// seconds. Absent while the gate is open, and absent when the decision on
	// disk carries no stamp at all (a decision that measured nothing must not
	// enter the distribution as a huge negative).
	LatencyMs *int64 `json:"latency_ms,omitempty"`
	// ReadGapMs is decided_at − the LAST reading mark among the required_reading
	// items. This is the number that separates a stamp from a judgement.
	//
	// gate.Decide dates an inline `--ack` at the decision itself
	// (internal/gate/store.go:100-110), and applyReads never moves an earlier
	// mark, so a gap of exactly 0 identifies the single-keystroke case and a gap
	// above 0 proves a separate `corvex gate ack` happened first.
	//
	// nil, never 0, when the gate carried no required reading, or when a
	// required item has no mark (a rejection needs no acknowledgement) — 0 has
	// its own meaning here and must not be a default.
	ReadGapMs *int64 `json:"read_gap_ms,omitempty"`
	// RequiredReading and ReadMarks are counted from the gate itself
	// (types.Evidence.RequiredReading and Pending.Reads), never from
	// Decision.Acked: in the one real gate on disk Acked is ABSENT even though
	// the lock let the approval through, because the reading had been persisted
	// earlier by `corvex gate ack` and MissingReading accepts an older mark.
	// Counting by Acked reports zero acknowledgement exactly where the human
	// really read.
	RequiredReading int `json:"required_reading"`
	ReadMarks       int `json:"read_marks"`
}

// Judged reports whether a person answered this gate. Expiry is not a judgement:
// nobody appeared.
func (r GateAuditRow) Judged() bool {
	return r.Verdict == gate.Approved || r.Verdict == gate.Rejected
}

// GateAuditGroup aggregates the rows of one key.
//
// Decided counts every answered gate; Judged counts the answers a human gave.
// The two differ by Expired, and the difference is the point: rejection_rate is
// rejected/(approved+rejected), with expiry reported ALONGSIDE rather than
// inside it. Folding expiry into the numerator would make a gate nobody ever
// looked at read as the most discerning one in the pipeline — the exact false
// negative risk 1 asks to avoid.
//
// Decided == Approved+Rejected+Expired is an invariant of the three verdicts
// gate.Verdict allows; if a fourth is ever added upstream, the sum stops
// matching and the discrepancy is visible in the JSON instead of being silently
// bucketed. TestGateVerdict_ThreeBucketsAndNoMore is the tripwire.
type GateAuditGroup struct {
	Key    string `json:"key"`
	Recipe string `json:"recipe,omitempty"`
	StepID string `json:"step_id"`

	Open     int `json:"open"`
	Decided  int `json:"decided"`
	Approved int `json:"approved"`
	Rejected int `json:"rejected"`
	Expired  int `json:"expired"`
	Judged   int `json:"judged"`

	// RejectionRate is nil when nobody judged, rather than 0 — a gate with no
	// judgements has no rejection rate, and printing 0% would read as "always
	// approved".
	RejectionRate *float64 `json:"rejection_rate,omitempty"`

	// The latency distribution covers the JUDGED population only. An expiry's
	// latency is the recipe's expires_after, i.e. a timer, and averaging a timer
	// into human response times poisons the number in the flattering direction.
	LatencyMsMin    *int64 `json:"latency_ms_min,omitempty"`
	LatencyMsMedian *int64 `json:"latency_ms_median,omitempty"`
	LatencyMsMax    *int64 `json:"latency_ms_max,omitempty"`
	LatencyMsP95    *int64 `json:"latency_ms_p95,omitempty"`

	// LatencyMeasured is how many of the Judged gates actually contributed to
	// the distribution above, and it is NOT always Judged: auditRow leaves
	// LatencyMs nil for a decided gate whose file carries no `decided_at` stamp,
	// on purpose (a latency invented from now() would be a measurement of when
	// the audit ran). Reporting the distribution under `n = Judged` therefore
	// claimed a bigger sample than the one that produced it — the same lie of
	// composition ReadGapMeasured exists to prevent one line below.
	LatencyMeasured int `json:"latency_measured"`

	ReadGapMsMedian *int64 `json:"read_gap_ms_median,omitempty"`
	// ReadGapMeasured is how many judged gates had a measurable gap at all, so
	// a median is never read as covering the whole group.
	ReadGapMeasured int `json:"read_gap_measured"`
	// ReadGapZero is how many were read and decided in the same breath.
	ReadGapZero int `json:"read_gap_zero"`
}

// GateAudit is the whole answer: per-key aggregates, and the raw rows they came
// from.
//
// The raw rows are not an optional detail — with a population this small they
// ARE the evidence, and a reader who can see them does not have to trust a
// median over n=1. There is deliberately no boolean verdict field (`theatre`,
// `suspect`): the "four seconds" line in risk 1 is POLICY, and policy written
// into a data field becomes a fact nobody can argue with later. The cut stays
// visible in argv (`--suspect-under`) and the judgement stays in the human
// render.
type GateAudit struct {
	Groups []GateAuditGroup `json:"groups"`
	Rows   []GateAuditRow   `json:"rows"`
	// Repos is how many repositories were scanned. It is printed for the same
	// reason PhaseUnattributed exists in internal/activity: gate files live in
	// gitignored scratch, so a shrunken population must be VISIBLE rather than
	// silent. An audit over one repository when the machine has five is a
	// different claim, and the reader is entitled to know which one they got.
	Repos int `json:"repos"`
	// Since is the window that was applied; zero means "everything on disk".
	Since time.Duration `json:"since_ns,omitempty"`
}

// GateAuditOptions narrows the audit. Zero values mean "everything".
type GateAuditOptions struct {
	// Since drops gates OPENED longer ago than this, the same way
	// RunListOptions.Since drops runs by start time.
	Since time.Duration
	// Repo keeps only one repository. Empty scans every repository in scope.
	Repo string
	// Nature keeps only gates of one nature. Empty keeps all.
	//
	// Today every gate file on disk is `human`, because humanGate is the only
	// writer of one (internal/step/human_gate.go:142). The filter exists so that
	// the day a policy or inferential gate is persisted it cannot silently land
	// in a distribution of human response times — a computational gate decided
	// in 12ms would drag every median it touches. No CLI flag yet, on purpose:
	// a flag whose only legal value is the default is ceremony.
	Nature types.GateNature
}

// LoadGateAudit reads every gate file in scope and aggregates it.
//
// It is the first production caller of gate.List. Both existing consumers use
// gate.ListOpen, which filters decided gates out — which is why, until now, a
// gate that was actually answered was invisible on every surface this tool has.
func (g GateLister) LoadGateAudit(localRepo string, opts GateAuditOptions) (GateAudit, error) {
	repos := g.reposInScope(localRepo)
	if want := canonicalRepo(opts.Repo); want != "" {
		repos = []string{want}
	}
	now := g.now()
	audit := GateAudit{
		Groups: []GateAuditGroup{},
		Rows:   []GateAuditRow{},
		Repos:  len(repos),
		Since:  opts.Since,
	}
	for _, repo := range repos {
		pendings, err := gate.List(repo)
		if err != nil {
			// A repository the index remembers and the filesystem no longer has
			// contributes zero rather than failing the audit — the same rule
			// ListGates already applies, for the same reason: one moved repo
			// must not hide the history of every other one.
			continue
		}
		for _, p := range pendings {
			if opts.Nature != "" && p.Nature != opts.Nature {
				continue
			}
			// The window is the ONLY place now() is allowed in here. Every
			// measurement below comes from two stamps on disk, which is what
			// makes the output reproducible without injecting a clock —
			// GateView.Waiting is the counter-example, and it is exactly why the
			// F2 goldens never print a gate's duration.
			if opts.Since > 0 && now.Sub(p.OpenedAt) > opts.Since {
				continue
			}
			audit.Rows = append(audit.Rows, auditRow(p))
		}
	}
	sortAuditRows(audit.Rows)
	audit.Groups = groupAuditRows(audit.Rows)
	return audit, nil
}

// auditRow projects one gate file onto the audit's shape — dropping evidence,
// prompt, title and reason on the way through.
func auditRow(p gate.Pending) GateAuditRow {
	row := GateAuditRow{
		Key:             auditKey(p),
		RunID:           p.RunID,
		StepID:          p.StepID,
		Repo:            p.Repo,
		Project:         p.Project,
		Recipe:          p.Recipe,
		Nature:          p.Nature,
		Label:           p.Label,
		OpenedAt:        p.OpenedAt,
		RequiredReading: len(gate.RequiredLabels(p.Evidence)),
		ReadMarks:       len(p.Reads),
	}
	if !p.Decided() {
		return row
	}
	row.Verdict = p.Decision.Verdict
	if p.Decision.DecidedAt.IsZero() {
		return row
	}
	decidedAt := p.Decision.DecidedAt
	row.DecidedAt = &decidedAt
	latency := decidedAt.Sub(p.OpenedAt).Milliseconds()
	row.LatencyMs = &latency
	row.ReadGapMs = readGapMs(p, decidedAt)
	return row
}

// readGapMs is decided_at − the last required-reading mark, or nil when the
// question does not apply.
//
// A negative value is not clamped: it means the two stamps on the file disagree
// (two machines, two clocks), and that is information about the record, not
// noise to be tidied into a plausible zero.
func readGapMs(p gate.Pending, decidedAt time.Time) *int64 {
	required := gate.RequiredLabels(p.Evidence)
	if len(required) == 0 {
		return nil
	}
	var last time.Time
	for _, label := range required {
		mark, ok := p.ReadOf(label)
		if !ok {
			// Not every required item was ever marked — normal for a rejection,
			// which the lock does not guard. A max over an incomplete set would
			// be a smaller gap than the truth, i.e. an accusation.
			return nil
		}
		if mark.At.After(last) {
			last = mark.At
		}
	}
	if last.IsZero() {
		return nil
	}
	gap := decidedAt.Sub(last).Milliseconds()
	return &gap
}

// auditKey is the aggregation key. See the package comment for its limits.
func auditKey(p gate.Pending) string {
	base := p.Recipe
	if base == "" {
		base = p.Project
	}
	if base == "" {
		// A gate whose file names neither: still grouped, never dropped, and
		// never quietly merged with a named one.
		base = "?"
	}
	return base + "/" + p.StepID
}

// sortAuditRows puts the sharpest signal first: the smallest read gap, which is
// the keystroke case. Rows with no measurable gap sort last — not innocent, just
// unmeasurable, and putting them at the top would bury the finding.
func sortAuditRows(rows []GateAuditRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if less, decided := lessByOptional(a.ReadGapMs, b.ReadGapMs); decided {
			return less
		}
		if less, decided := lessByOptional(a.LatencyMs, b.LatencyMs); decided {
			return less
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.RunID < b.RunID
	})
}

// lessByOptional orders two optional numbers ascending with nil last, and
// reports whether they decided the comparison at all.
func lessByOptional(a, b *int64) (less, decided bool) {
	switch {
	case a == nil && b == nil:
		return false, false
	case a == nil:
		return false, true
	case b == nil:
		return true, true
	case *a != *b:
		return *a < *b, true
	}
	return false, false
}

func groupAuditRows(rows []GateAuditRow) []GateAuditGroup {
	order := make([]string, 0, len(rows))
	byKey := make(map[string]*GateAuditGroup, len(rows))
	latencies := make(map[string][]int64, len(rows))
	gaps := make(map[string][]int64, len(rows))

	for _, r := range rows {
		grp, ok := byKey[r.Key]
		if !ok {
			grp = &GateAuditGroup{Key: r.Key, Recipe: r.Recipe, StepID: r.StepID}
			byKey[r.Key] = grp
			order = append(order, r.Key)
		}
		if r.Verdict == "" {
			grp.Open++
			continue
		}
		grp.Decided++
		switch r.Verdict {
		case gate.Approved:
			grp.Approved++
		case gate.Rejected:
			grp.Rejected++
		case gate.Expired:
			grp.Expired++
		}
		if !r.Judged() {
			continue
		}
		grp.Judged++
		if r.LatencyMs != nil {
			latencies[r.Key] = append(latencies[r.Key], *r.LatencyMs)
			grp.LatencyMeasured++
		}
		if r.ReadGapMs != nil {
			gaps[r.Key] = append(gaps[r.Key], *r.ReadGapMs)
			grp.ReadGapMeasured++
			if *r.ReadGapMs == 0 {
				grp.ReadGapZero++
			}
		}
	}

	out := make([]GateAuditGroup, 0, len(order))
	for _, key := range order {
		grp := byKey[key]
		if grp.Judged > 0 {
			rate := float64(grp.Rejected) / float64(grp.Judged)
			grp.RejectionRate = &rate
		}
		lat := latencies[key]
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		if len(lat) > 0 {
			grp.LatencyMsMin = ptr(lat[0])
			grp.LatencyMsMedian = ptr(nearestRank(lat, 0.5))
			grp.LatencyMsMax = ptr(lat[len(lat)-1])
			if len(lat) >= P95MinSample {
				grp.LatencyMsP95 = ptr(nearestRank(lat, 0.95))
			}
		}
		gap := gaps[key]
		sort.Slice(gap, func(i, j int) bool { return gap[i] < gap[j] })
		if len(gap) > 0 {
			grp.ReadGapMsMedian = ptr(nearestRank(gap, 0.5))
		}
		out = append(out, *grp)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if less, decided := lessByOptional(a.ReadGapMsMedian, b.ReadGapMsMedian); decided {
			return less
		}
		if less, decided := lessByOptional(a.LatencyMsMedian, b.LatencyMsMedian); decided {
			return less
		}
		return a.Key < b.Key
	})
	return out
}

// nearestRank is the sample at a percentile, without interpolation, and the
// median is the LOWER of two middles.
//
// Interpolating would print a millisecond count nobody ever waited: with two
// samples of 0ms and 4h, an "average median" of 2h describes no decision that
// happened. Every number this file prints is a decision somebody actually made.
func nearestRank(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func ptr(v int64) *int64 { return &v }

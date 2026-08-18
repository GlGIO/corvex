package activity

// Ledger `type` values this file has to recognise by name.
//
// They are literals and not an import of internal/event on purpose: activity is
// the leaf that every other package writes *through*, and importing the event
// vocabulary here would invert that direction for no gain. The strings are the
// on-disk format either way — a ledger written last month spells them out, no Go
// constant does. The drift literals invite (somebody renames event.GateDecided
// and HumanWaitMs quietly becomes zero forever) is caught from the *test*
// package, which may import internal/event without the production package
// depending on it: see TestLedgerTypeLiterals_TrackTheEventVocabulary.
const (
	typeTaskComplete = "task_complete"
	typeGateDecided  = "gate_decided"
	typeToolUse      = "tool_use"
)

// PhaseUnattributed is the bucket for lines that carry no `phase` at all.
//
// That is most of the history on disk today: the column has existed since before
// F1 and nothing ever filled it, so every cost in every ledger written until F5
// lands here. Two alternatives were rejected, and the name is the argument:
//
//   - Dropping the phase-less lines. Then an old project's breakdown is empty
//     while its header still reads $16.54, and the reader concludes the money is
//     missing rather than merely unlabelled.
//   - Folding them into "worker". Convenient, because most of them really are
//     worker lines, and wrong twice over: it asserts as fact something the file
//     does not say, and it makes the un-migrated population invisible — nobody
//     ever goes back to fix a bucket that already looks correct.
//
// The value is deliberately not a legal on-disk phase (the allowlist in
// TestEntry_JSONKeysAreAnAllowlist names worker|review|plan|recovery|gate|
// validate), so a bucket can never be mistaken for a line that carried one.
const PhaseUnattributed = "unattributed"

// TaskMetric carries the per-task metrics needed to seed the TUI when
// resuming a project that has tasks already completed in previous runs.
type TaskMetric struct {
	TaskID     string  `json:"task_id"`
	DurationMs int64   `json:"duration_ms"`
	CostUSD    float64 `json:"cost_usd"`
	TokensIn   int     `json:"tokens_in"`
	TokensOut  int     `json:"tokens_out"`
}

// PhaseMetric is one bar of "where did the effort go" — the 2f breakdown by
// nature (worker / reviewer / deterministic).
//
// Entries counts ledger LINES in the phase, including the ones that carry no
// money and no clock; it answers "how much of this run was review at all",
// which a cost bar alone cannot when the reviewer is cheap and constant.
//
// DurationMs is attributed machine time, NOT wall clock, and the difference is
// not cosmetic: tasks run in parallel, so several phases advance over the same
// seconds and the sum can exceed the run's real elapsed time. The only wall
// clock this package reports is HumanWaitMs.
type PhaseMetric struct {
	Phase      string  `json:"phase"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMs int64   `json:"duration_ms"`
	Entries    int     `json:"entries"`
}

// ToolMetric is the per-tool row behind screens 2d/2f: how often a run reached
// for a tool and how long that tool held it.
//
// Uses counts `tool_use` lines only, never `tool_result`. Both shapes carry the
// tool name, so counting either would report exactly double on the happy path
// and the right number only for a run that crashed mid-tool — a metric that
// lies precisely when nothing is wrong is worse than no metric.
//
// DurationMs takes the clock from whichever line carries one, which in practice
// is the result line: the elapsed time of a tool is only known once it returns.
type ToolMetric struct {
	Tool       string `json:"tool"`
	Uses       int    `json:"uses"`
	DurationMs int64  `json:"duration_ms"`
}

// Summary aggregates activity.jsonl into the shape the TUI needs on
// startup: per-task duration/cost for already-PASSED tasks (so the DAG
// panel doesn't render them as "0s"), and the cumulative cost/token
// totals (so the header doesn't show "$0.00" while $16.54 has actually
// been spent).
//
// # Two reductions, two different keys, on purpose
//
// PerTask (and the totals derived from it) is keyed by TASK: retries produce
// several task_complete lines for one task_id and only the latest PASSED one
// counts, so a task re-run by a later run replaces its own numbers instead of
// adding to them. That is what keeps Summarize project-cumulative without
// double counting across runs — see the Summarize comment for why scoping it to
// the current run instead is the "$0.00 after resume" bug.
//
// PerPhase and PerTool are keyed by LINE: each ledger line lands in exactly one
// phase bucket and is visited exactly once, in ledger order. The new columns
// therefore cannot reintroduce that bug from either side — they are computed by
// the same `aggregate` over the same entry slice, so Summarize still spans every
// run in the file and SummarizeRun still narrows to one, and no line can be
// credited to two buckets because the bucket is read off the line itself.
//
// # The two do not add up to the same number, and must not be rendered as if
//
// Sum(PerPhase[].CostUSD) >= TotalCostUSD, and the gap is the superseded
// attempts: money spent by a task that a later run re-ran is real money the
// phase breakdown reports and the task-keyed total deliberately forgets. So the
// 2f bar renders proportions *within itself* ("of the effort spent, 18% was
// review"), never a slice of the header figure. Making the two agree would mean
// either the header double counting resumes or the breakdown pretending failed
// work was free; neither is worth the tidier arithmetic.
type Summary struct {
	PerTask        map[string]TaskMetric  `json:"per_task"`
	PerPhase       map[string]PhaseMetric `json:"per_phase"`
	PerTool        map[string]ToolMetric  `json:"per_tool"`
	TotalCostUSD   float64                `json:"total_cost_usd"`
	TotalTokensIn  int                    `json:"total_tokens_in"`
	TotalTokensOut int                    `json:"total_tokens_out"`

	// HumanWaitMs is how long the run sat blocked on a person, summed over the
	// `gate_decided` lines, which carry the wait a human gate served.
	//
	// It exists because the run clock and this clock are the same number today,
	// and that number lies: a run that took 4h of which 3h50 was somebody
	// asleep is not a slow run, and reading it as one is how the wrong thing
	// gets optimised. Callers subtract it from the run's elapsed wall time (held
	// by the run record, which owns the start/end instants — this package sees
	// only durations) to get the machine clock.
	//
	// Scope: `gate_decided` is emitted from exactly one place, the human gate in
	// internal/step, and the other three gate natures never reach it — a
	// deterministic gate that refuses emits `gate_failed`. If a future nature
	// starts emitting `gate_decided`, its machine time silently joins this
	// total; the test asserting that is the tripwire, not this comment.
	// The --approve-gates path emits the line with no duration, which is the
	// honest answer: nobody waited.
	HumanWaitMs int64 `json:"human_wait_ms"`
}

// Summarize reads the activity ledger and returns a Summary. Missing
// ledger files are not an error — the caller gets an empty Summary,
// matching the case of a fresh project.
//
// Decided in F1: Summarize stays *project-cumulative*, spanning every run in the
// file. That is not an oversight of run identity, it is the reason the function
// exists — resuming a project must show the $16.54 already spent and the
// durations of tasks that passed in earlier runs, and scoping it to the current
// run would put the "$0.00 after resume" bug back. Cross-run double counting is
// already impossible: metrics are keyed by task and the latest PASSED entry
// wins, so a task re-run in a second run replaces its own numbers instead of
// adding to them. Callers that want one run ask SummarizeRun.
func Summarize(workDir, project string) (Summary, error) {
	entries, err := Read(workDir, project)
	if err != nil {
		return Summary{}, err
	}
	return aggregate(entries), nil
}

// SummarizeRun is Summarize restricted to the lines one run wrote — what a
// per-run view (F7) needs, and what makes two runs of the same project
// distinguishable in a single ledger. Pass "" for the pre-identity lines.
func SummarizeRun(workDir, project, runID string) (Summary, error) {
	entries, err := Read(workDir, project)
	if err != nil {
		return Summary{}, err
	}
	return aggregate(FilterByRun(entries, runID)), nil
}

// Two debts the emitters owe this reduction. Both are in internal/step, which
// this change does not own, and neither is repairable from the reading side:
//
//  1. The `task_complete` cost is a ROLL-UP. internal/step computes it as
//     worker + reviewer for the attempt, while the `review_result` line that
//     same code path emits carries no cost at all. So for a code step the
//     reviewer's money is attributed to the worker bucket, and the 2f bar
//     under-reports review by exactly that. There is no double counting — the
//     roll-up and the leaf never both carry the number — and unrolling it here
//     is not possible: `review_result` carries no attempt, so a retried task's
//     review lines cannot be matched to the attempt that survived. The gate path
//     (an inferential gate) does emit a priced `review_result` and is charged
//     separately, so that money lands in the review bucket correctly. Fix
//     belongs where the number is produced: emit the worker cost and the
//     reviewer cost as their own lines.
//  2. A failed attempt emits `task_complete` with NO cost. The ceiling is
//     charged, so the run knows it spent the money, and the ledger does not.
//     Retried spend is therefore invisible to every column here, phase bar
//     included, and the "superseded attempts" gap this file documents between
//     the breakdown and the header is smaller than the real one.
//
// aggregate is the shared reduction behind Summarize and SummarizeRun: latest
// PASSED completion per task, totals over those winners, and the per-line
// breakdowns by phase and by tool.
//
// One pass, because the per-line buckets accumulate in ledger order — which is
// stable, unlike the map walk the totals use. The frozen anomaly recorded in
// f-1-anomalias.md (the aggregated total lands on a different float64 depending
// on Go's randomised map iteration) is therefore not extended to the new
// columns, and TotalCostUSD is left exactly as it was rather than "fixed" here.
// AggregateEntries summarises entries a caller already has in hand.
//
// It exists because `run show <id>` filters the ledger to ONE execution before
// it renders; making it call Summarize would summarise the whole project and
// quietly answer a different question than the screen is asking.
func AggregateEntries(entries []Entry) Summary { return aggregate(entries) }

func aggregate(entries []Entry) Summary {
	perTask := make(map[string]TaskMetric, len(entries))
	perPhase := make(map[string]PhaseMetric)
	perTool := make(map[string]ToolMetric)
	var humanWaitMs int64

	for _, e := range entries {
		phase := e.Phase
		if phase == "" {
			phase = PhaseUnattributed
		}
		pm := perPhase[phase]
		pm.Phase = phase
		pm.Entries++
		// A tool line's money and clock are NESTED inside the step that invoked
		// the tool, and both land in the same bucket, so adding them here would
		// count the same seconds twice within one phase. The nested numbers are
		// not lost — they are what PerTool reports. The line still counts toward
		// Entries: that is a count of lines, not a sum of a nested quantity.
		if e.Tool == "" {
			pm.CostUSD += e.CostUSD
			pm.DurationMs += e.DurationMs
		}
		perPhase[phase] = pm

		if e.Tool != "" {
			tm := perTool[e.Tool]
			tm.Tool = e.Tool
			if e.Type == typeToolUse {
				tm.Uses++
			}
			tm.DurationMs += e.DurationMs
			perTool[e.Tool] = tm
		}

		if e.Type == typeGateDecided {
			humanWaitMs += e.DurationMs
		}

		if e.Type != typeTaskComplete || e.Status != "PASSED" || e.TaskID == "" {
			continue
		}
		// Last write wins: later PASSED entries override earlier ones from
		// retried attempts. (A task that previously FAILED then PASSED only
		// contributes its PASSED metrics.)
		perTask[e.TaskID] = TaskMetric{
			TaskID:     e.TaskID,
			DurationMs: e.DurationMs,
			CostUSD:    e.CostUSD,
			TokensIn:   e.TokensIn,
			TokensOut:  e.TokensOut,
		}
	}

	s := Summary{PerTask: perTask, PerPhase: perPhase, PerTool: perTool, HumanWaitMs: humanWaitMs}
	for _, m := range perTask {
		s.TotalCostUSD += m.CostUSD
		s.TotalTokensIn += m.TokensIn
		s.TotalTokensOut += m.TokensOut
	}
	return s
}

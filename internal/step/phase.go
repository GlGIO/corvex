package step

import (
	"time"

	"github.com/giovannialves/corvex/internal/gate"
)

// Phase attribution — how every event this package emits gets its `phase`.
//
// The ledger has carried a `phase` column since before F1 and nothing ever
// wrote it, so every cost in every activity.jsonl on disk is unattributed and
// the "cost by nature" bar has no data at all. F5 fills it from here.
//
// The rule is *who produced the work being charged*, not *which function
// emitted the line*:
//
//   - worker   — an AI attempt at the step's actual work: its start, its
//     stream, its retries, its watchdog, its terminal line, and the A/B
//     comparison of two workers.
//   - review   — an independent judge reading that work. Both judges count:
//     the reviewer built into a code step, and the inferential gate, which is
//     charged to the run by chargeGate rather than to the worker's attempts.
//     They are the same nature of spend even though one is declared as a gate,
//     and separating them by declaration site would make "what did judging
//     cost" unanswerable.
//   - gate     — the gate deciding, in every nature: a human answering, a
//     policy refusing a branch, a computational check failing. The refusal
//     line is `gate` even when an inferential gate produced it, because a
//     screen counting "how often do gates refuse" must not have to know which
//     nature computed the evidence. The judging that led there already
//     accounts for itself on its own `review` lines.
//   - validate — a deterministic stage: tool, test, repro. No LLM, no cost.
//     This is the "deterministic" third of the 2f bar; tagging it is what
//     turns "$0" from a missing number into a measured one.
//
// Known limitation (recorded, not fixed here): the `task_complete` line of an
// AI step carries workerCost+reviewerCost as one number, so tagging it `worker`
// over-attributes the reviewer's share. Splitting it means moving cost off
// `task_complete`, and internal/ops/inspect.go and run_show.go both read
// exactly that line as the task's total — a legacy surface this front does not
// own. The inferential gate's spend is already separable because chargeGate
// bills the run directly and the verdict line carries the number.

// humanWaitMs is how long a gate held waiting for a person: from OpenedAt to
// the moment somebody decided.
//
// This is the HUMAN's clock and must never be read as the run's. Every other
// duration_ms in the ledger measures machine time a run actually spent working;
// this one measures a run that was asleep. Summing duration_ms across event
// types therefore invents wall time nobody paid for — the price of reusing the
// column instead of adding a key to a file that lives in the user's git
// history. It exists because the risk it makes visible cannot be seen any other
// way: a gate approved in four seconds, every single time, is theatre, and
// without this number nothing on disk can tell theatre from judgement.
//
// It is measured against the decision's own timestamp rather than the moment
// this process noticed it, because awaitDecision polls: reading the clock here
// would charge the person up to one poll interval of the runner's own latency.
// A decision with no timestamp, or one stamped before the gate opened (clock
// skew between the deciding process and this one), falls back to the observed
// wait. Never negative: a negative wait is not information, it is a broken
// clock, and zero says "no time passed" without pretending to explain why.
func humanWaitMs(openedAt time.Time, d gate.Decision, observed time.Time) int64 {
	end := d.DecidedAt
	if end.IsZero() || end.Before(openedAt) {
		end = observed
	}
	ms := end.Sub(openedAt).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

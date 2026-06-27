# Reviewer verdict robustness & honest escalation

Three changes to make the review/escalation path trustworthy. Keep
`go build ./...` and `go test ./...` green.

## Objective A — Indeterminate verdict (CH-06)
Today `internal/orchestrator/reviewer.go` `parseVerdict` defaults to FAIL when
no `VERDICT:` line is found. A broken/empty reviewer output (provider hiccup,
truncation) is therefore indistinguishable from a genuine task failure, and the
task gets retried blindly or marked failed.

Requirements:
- Introduce a third verdict constant `VerdictIndeterminate` alongside
  `VerdictPass`/`VerdictFail`.
- `parseVerdict` returns `VerdictIndeterminate` when NO recognizable verdict
  line is present in the output (as opposed to an explicit FAIL).
- In `executeTask` (orchestrator.go), an indeterminate verdict is NOT a task
  failure: treat it like a transient error — retry the attempt with a diagnosis
  noting the reviewer produced no parseable verdict — and if it persists through
  all attempts, fail with a clear "reviewer never produced a verdict" error
  rather than silently reporting the task as failed-on-merits. Do NOT count an
  indeterminate result toward escalation category counts.

## Objective B — Tolerant verdict parsing (CH-06)
`parseVerdict` only matches lines exactly equal to `VERDICT: PASS`/`VERDICT: FAIL`
(after upper+trim). Real model output often wraps it: `**VERDICT: PASS**`,
`VERDICT: PASS.`, `> VERDICT: FAIL`, trailing spaces, surrounding markdown.

Requirements:
- Match a verdict line tolerantly: strip leading markdown/quote markers
  (`*`, `_`, `#`, `>`, backticks, spaces) and trailing punctuation/markup
  (`.`, `*`, `_`, backticks, spaces), then check for `VERDICT: PASS` /
  `VERDICT: FAIL` case-insensitively. Same tolerance for the `CATEGORY:` line.
- Add table-driven tests in `reviewer_test.go` covering: bold-wrapped verdict,
  trailing period, blockquote prefix, backtick-wrapped — all parsed correctly;
  and an output with no verdict line → Indeterminate.

## Objective C — Honest spawn-investigation (CH-06)
In `executeTask`, the `ActionSpawnInvestigation` case is a silent stub: it logs
"not yet implemented" and falls through to a normal retry. The config promises a
behavior that does not exist.

Requirements (implement the minimal honest version):
- When escalation resolves to `spawn-investigation`, run a dedicated
  investigation step BEFORE the next retry: invoke the provider (reuse the
  Advisor's model / a read-only request like the Reviewer uses) with a prompt
  that includes the task, the failing reviewer summary, and asks for a concrete
  root-cause diagnosis and a recommended fix approach. Feed that diagnosis into
  the next attempt's `diagnosis` string (so the worker gets richer guidance than
  the raw reviewer summary).
- Emit an event so it is observable (reuse EventRetry with a distinguishing
  message, or add a new event type — your call, keep it consistent with the
  existing event set).
- Add a test proving spawn-investigation produces a diagnosis that is passed to
  the next worker attempt (drive with the stub provider).

## Files (reference)
- internal/orchestrator/reviewer.go — parseVerdict, ReviewVerdict consts
- internal/orchestrator/orchestrator.go — executeTask escalation switch
- internal/orchestrator/escalation.go — resolveEscalation, Action consts
- internal/orchestrator/advisor.go — pattern for a read-only provider call

## Validation
- go build ./... and go test ./... pass.

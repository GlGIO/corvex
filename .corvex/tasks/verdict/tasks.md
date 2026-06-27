## S01 — Indeterminate verdict constant + tolerant parsing ✅ PASSED

```yaml
type: backend
```

### O que fazer
Modify `parseVerdict` and the `ReviewVerdict` constants in
`internal/orchestrator/reviewer.go` to (a) introduce a third verdict and
(b) parse verdict/category lines tolerantly. This task covers Objective A's
parser-side change and the entirety of Objective B.

**(a) New constant — Objective A:**

> [verbatim from spec.md §Objective A]
> - Introduce a third verdict constant `VerdictIndeterminate` alongside
>   `VerdictPass`/`VerdictFail`.
> - `parseVerdict` returns `VerdictIndeterminate` when NO recognizable verdict
>   line is present in the output (as opposed to an explicit FAIL).

Today `parseVerdict` initializes `verdict := VerdictFail` and leaves it FAIL
when no line matches (reviewer.go:160). The new behavior: when no recognizable
verdict line is found, return `VerdictIndeterminate`, NOT FAIL. An explicit
`VERDICT: FAIL` line still returns `VerdictFail`.

**(b) Tolerant parsing — Objective B:**

> [verbatim from spec.md §Objective B]
> - Match a verdict line tolerantly: strip leading markdown/quote markers
>   (`*`, `_`, `#`, `>`, backticks, spaces) and trailing punctuation/markup
>   (`.`, `*`, `_`, backticks, spaces), then check for `VERDICT: PASS` /
>   `VERDICT: FAIL` case-insensitively. Same tolerance for the `CATEGORY:` line.

The current matcher only accepts a line whose upper+trimmed form is **exactly**
`VERDICT: PASS` / `VERDICT: FAIL` (reviewer.go:165-174). Replace the equality
check with: strip the leading set of markers, strip the trailing set of markers,
uppercase, then compare against `VERDICT: PASS` / `VERDICT: FAIL`. Apply the
same leading/trailing stripping to the `CATEGORY:` line detection
(reviewer.go:189-197) so `**CATEGORY: wrong-approach**` etc. still resolve.

This must parse correctly: `**VERDICT: PASS**`, `VERDICT: PASS.`,
`> VERDICT: FAIL`, `` `VERDICT: FAIL` ``, trailing-space variants.

**(c) Tests — Objective B:**

> [verbatim from spec.md §Objective B]
> - Add table-driven tests in `reviewer_test.go` covering: bold-wrapped verdict,
>   trailing period, blockquote prefix, backtick-wrapped — all parsed correctly;
>   and an output with no verdict line → Indeterminate.

Add a new table-driven test in `reviewer_test.go`. Also **update the existing
`TestParseVerdict_NoMarker`** (reviewer_test.go:53-63): it currently asserts the
no-marker case yields `VerdictFail`; it must now assert `VerdictIndeterminate`.
Keep its summary assertion intact.

---

## S02 — Indeterminate verdict handled as transient in executeTask ✅ PASSED

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
In `executeTask` (`internal/orchestrator/orchestrator.go`), handle a
`VerdictIndeterminate` review result distinctly from a FAIL. This is Objective A's
orchestration-side change.

> [verbatim from spec.md §Objective A]
> - In `executeTask` (orchestrator.go), an indeterminate verdict is NOT a task
>   failure: treat it like a transient error — retry the attempt with a diagnosis
>   noting the reviewer produced no parseable verdict — and if it persists through
>   all attempts, fail with a clear "reviewer never produced a verdict" error
>   rather than silently reporting the task as failed-on-merits. Do NOT count an
>   indeterminate result toward escalation category counts.

Current flow: after `EventReviewResult` is emitted (orchestrator.go:478-482),
the code checks `reviewResult.Verdict == VerdictPass` (line 484); anything
else falls through to the FAIL path (diagnosis = summary, OnFailure hook,
`EventTaskComplete` with `StatusFailed`, escalation switch). Insert handling
for `VerdictIndeterminate` BEFORE that FAIL path:

- Treat it like the transient-error retry branch (mirror the `reviewErr != nil`
  branch at orchestrator.go:467-476): set `diagnosis` to a message noting the
  reviewer produced no parseable verdict, then `continue` to the next attempt.
- On the final attempt (`attempt == maxRetries`), mark the task
  `StatusFailed` and return an error whose message clearly states the reviewer
  never produced a verdict (include the literal phrase
  `reviewer never produced a verdict`), distinct from the normal
  "failed review after N attempts" message at orchestrator.go:593.
- Do **NOT** run `categoryCounts[cat]++` / `resolveEscalation` for an
  indeterminate result — escalation category counts must not be incremented.
  An indeterminate result has no `Category` anyway, but the indeterminate
  branch must `continue`/return before reaching the escalation switch
  (orchestrator.go:557-587).

---

## S03 — Honest spawn-investigation ✅ PASSED

```yaml
type: backend
depends_on: [S01, S02]
```

### O que fazer
Replace the silent `ActionSpawnInvestigation` stub in `executeTask`
(orchestrator.go:579-586, currently logs "not yet implemented" and falls
through to retry) with a minimal honest implementation. This is Objective C.

> [verbatim from spec.md §Objective C]
> - When escalation resolves to `spawn-investigation`, run a dedicated
>   investigation step BEFORE the next retry: invoke the provider (reuse the
>   Advisor's model / a read-only request like the Reviewer uses) with a prompt
>   that includes the task, the failing reviewer summary, and asks for a concrete
>   root-cause diagnosis and a recommended fix approach. Feed that diagnosis into
>   the next attempt's `diagnosis` string (so the worker gets richer guidance than
>   the raw reviewer summary).
> - Emit an event so it is observable (reuse EventRetry with a distinguishing
>   message, or add a new event type — your call, keep it consistent with the
>   existing event set).
> - Add a test proving spawn-investigation produces a diagnosis that is passed to
>   the next worker attempt (drive with the stub provider).

Implementation notes:

- Add an investigation step. Reuse the read-only provider-call pattern: a
  `runStep`/`Execute` call with `AllowedTools: []string{"Read", "Glob", "Grep"}`
  (as the Advisor does, advisor.go:91-96) on the **Advisor's model**
  (`o.advisor.model` / `o.cfg.Provider.Models.Planner`, the model the Advisor is
  constructed with — orchestrator.go:89). Keep it read-only — the
  investigation diagnoses, it does not edit.
- Build a prompt that includes: the task (ID/title/description/criteria), the
  failing reviewer summary (`reviewResult.Summary`), and asks for a concrete
  **root-cause diagnosis** and a **recommended fix approach**.
- Assign the investigation's output to `diagnosis` so the **next** worker
  attempt receives it (richer than the raw reviewer summary). The ordering is:
  investigation runs, then the loop continues to the next iteration, where
  `o.worker.Execute(..., diagnosis)` (orchestrator.go:416) consumes it.
- Emit an observable event. Either reuse `EventRetry` with a distinguishing
  `Message`, or add a new `EventType` in `events.go` (e.g.
  `EventInvestigation = "investigation"`) — if adding one, keep it consistent
  with the existing constant set (events.go:15-33) and wire it into the TUI
  switch (`internal/tui/model.go`) and `cmd/run.go` consumers if they exhaustively
  switch on event types. Prefer reusing `EventRetry` with a distinct message to
  minimize surface area unless a new type is clearly cleaner.

---

## S04 — Validation & cross-objective consistency review ⬜ PENDING

```yaml
type: review
depends_on: [S01, S02, S03]
```

### O que fazer
Final validation pass over the three changes. Confirm the build and full test
suite are green and that the three objectives compose correctly (no objective
silently broke another — e.g. tolerant parsing of S01 still feeding the
indeterminate logic of S02, and the spawn-investigation of S03 not colliding
with the indeterminate retry branch).

> [verbatim from spec.md §Validation]
> - go build ./... and go test ./... pass.

Verify specifically:
- `go build ./...` succeeds.
- `go test ./...` succeeds (full suite, not just the orchestrator package).
- The three new/updated behaviors each have a passing test: tolerant parsing +
  Indeterminate (S01), indeterminate transient handling (S02), spawn-investigation
  diagnosis hand-off (S03).
- No leftover `not yet implemented` log line for spawn-investigation.

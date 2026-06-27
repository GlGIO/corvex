---
dag:
    S01: []
    S02: []
    S03:
        - S01
        - S02
    S04:
        - S03
---

## S01 — Add `TransitiveDependents` helper to the DAG ✅ PASSED

```yaml
type: backend
```

### O que fazer
Add an exported helper to `internal/dag/dag.go` that, given a task ID, returns
the set of all tasks transitively reachable via the `dependents` adjacency
(i.e. every task that depends on the given node, directly or indirectly).

The spec leaves the exact name/shape open:

> [verbatim from spec.md §DAG support]
> You may add a helper to `internal/dag/dag.go` such as
> `Dependents(id string) []string` or `TransitiveDependents(id string) []string`
> to compute the set of tasks reachable from a failed node via the dependents
> edges (the `node.dependents` adjacency already exists). Add a unit test for it.

Implement `TransitiveDependents(id string) []string`:
- Traverse `node.dependents` starting from `id` (BFS or DFS).
- Do NOT include `id` itself in the result (only its dependents).
- De-duplicate (diamond shapes reach the same node via two paths).
- Return the result sorted with `sort.Strings`, matching the deterministic
  ordering convention of `NextReady`/`Resolve`/`Levels` in this file.
- If `id` is unknown to the graph, return an empty slice (no panic).

Add a unit test in `internal/dag/dag_test.go` following the existing
table-driven, `t.Parallel()` style (see `TestDAG_NextReady_Progression`). Cover
at least: a linear chain, a diamond (the same node reached via two paths must
appear once), a leaf node (empty result), and an unknown id (empty result).

---

## S02 — Introduce fatal-vs-task-level error classification in `executeTask` ⬜ PENDING

```yaml
type: backend
```

### O que fazer
`executeTask` currently returns a plain `error` for every failure mode, and
`Run` aborts on all of them. Introduce a distinct error type so callers can
tell apart errors that must abort the whole run from errors that should only
skip dependents.

> [verbatim from spec.md §Critical distinction — fatal vs task-level errors]
> - **Task-level failure** (worker failed after maxRetries, review FAIL after
>   maxRetries, reviewer error after maxRetries): skip dependents, continue.
> - **Fatal run errors** that must abort everything immediately: cost ceiling
>   exceeded (`MaxCostUSD`/`MaxCostPerTaskUSD`), human-prompt escalation
>   (ActionHumanPrompt), and context cancellation (ctx.Err()).

> [verbatim from spec.md §Critical distinction — fatal vs task-level errors]
> Implement this by introducing a distinct error type (e.g. a `fatalError`
> wrapper, or a sentinel checked via errors.As/errors.Is) that `executeTask`
> returns for the fatal cases, and a plain task-failure signal for the rest.

Concretely:
1. Define a `fatalError` wrapper type in the orchestrator package implementing
   `Error()` and `Unwrap()`, plus a small constructor (e.g. `fatal(err error)`)
   and a classifier (`isFatal(err error) bool` via `errors.As`).
2. Wrap as **fatal** exactly these return paths inside `executeTask`:
   - the per-task cost ceiling breach (`MaxCostPerTaskUSD`, both occurrences —
     after worker error and after review at lines ~432 and ~461);
   - the cumulative cost ceiling breach (`MaxCostUSD`, both occurrences at
     lines ~435 and ~464);
   - the `ActionHumanPrompt` escalation return (line ~589).
3. Context cancellation (`ctx.Err()`) is already handled as fatal by `Run`'s
   own `if ctx.Err() != nil { return ctx.Err() }` guard before/while iterating
   ready tasks; ensure any `ctx.Err()`-derived error you choose to surface from
   `executeTask` is also treated as fatal (wrap it, or let `Run` keep checking
   `ctx.Err()` directly — document which).
4. Leave all the genuine task-failure returns (worker failed after maxRetries,
   reviewer error after maxRetries, indeterminate verdict after maxRetries, and
   the final "failed review after N attempts") as **plain (non-fatal)** errors.

Do NOT change `Run`'s loop behaviour in this task beyond what is needed to keep
it compiling — the loop rewrite is S03. The point of this task is purely the
error-type plumbing so `Run` can branch on `isFatal`.

---

## S03 — Skip transitive dependents and continue the scheduler loop ⬜ PENDING

```yaml
type: backend
depends_on: [S01, S02]
```

### O que fazer
Rewrite `Run`'s handling of the `executeTask` error so that a task-level
failure no longer aborts the whole run.

> [verbatim from spec.md §Objective]
> When a task FAILS on its merits (worker/review failed after all retries), the
> orchestrator should:
> 1. Mark that task FAILED (already happens inside executeTask).
> 2. Mark every task that transitively depends on it as SKIPPED (it can never
>    become ready), emitting an EventTaskComplete with Status=SKIPPED and a
>    message like "skipped: depends on failed task <id>".
> 3. Continue executing the remaining independent ready tasks.
> 4. After the loop, if any task ended FAILED, return a single summary error
>    listing the failed and skipped task IDs (so the process still exits non-zero),
>    but only AFTER all runnable branches have been attempted.

Implementation in `Run` (the `for { ready := d.NextReady(...) }` loop, around
lines 251–330, and the single `executeTask` call at line 322):

1. Replace `else if err := o.executeTask(...); err != nil { return err }` with:
   - If `isFatal(err)` → `return err` (abort immediately, unchanged behaviour).
   - Else (task-level failure) → record the failed task ID; compute its
     transitive dependents via `d.TransitiveDependents(t.ID)` (S01); for each
     dependent that is not already `completed`, persist `StatusSkipped` via
     `task.UpdateTaskStatus`, add it to `completed` so the scheduler stops
     re-offering it, record it in a skipped set, and emit:
     `Event{Type: EventTaskComplete, TaskID: dep, Status: types.StatusSkipped,
     Message: "skipped: depends on failed task " + t.ID}`.
   - Then `continue` the loop (do NOT mark the failed task itself as
     `completed` only to unblock dependents — it is FAILED, and its dependents
     are explicitly skipped above; but you DO need the loop to stop offering
     the failed task again: it is already FAILED in tasks.md and `NextReady`
     keys off `completed`, so add the failed task to `completed` as well to
     prevent a re-pick. Confirm `NextReady` won't re-run it.).
2. Track accumulated `failed []string` and `skipped []string` ID sets across
   the loop (dedupe; a node can be a dependent of two failed tasks).
3. After the scheduler loop (before the advisor/`EventDone` block), if `failed`
   is non-empty, build and return a single summary error listing the failed and
   skipped task IDs, e.g.
   `fmt.Errorf("run completed with failures: failed=[%s] skipped=[%s]", ...)`.
   This return must come AFTER all runnable branches have been attempted.

> [verbatim from spec.md §Critical distinction — fatal vs task-level errors]
> Be careful with `--task`/`--single` modes: a single targeted task that fails
> should still return an error.

For `--task`/`--single`: when `o.targetTask != "" || o.singleTask`, a
task-level failure should still return an error (do not silently swallow it).
The simplest correct behaviour: in single/target mode, treat the task-level
failure like before and return the summary/error rather than continuing — the
loop already `break`s after one iteration in those modes (lines 327–329), so
ensure the failure is surfaced as a non-nil error from `Run` in that path.

---

## S04 — Orchestrator tests: diamond failure + fatal-abort ⬜ PENDING

```yaml
type: review
depends_on: [S03]
```

### O que fazer
Add orchestrator tests using the existing stub provider that prove the new
resilience behaviour and that fatal errors are NOT swallowed.

> [verbatim from spec.md §Validation]
> - Add an orchestrator test (using the stub provider) with a diamond DAG where
>   one mid task fails: prove the independent branch still runs to PASSED, the
>   dependents of the failed task are SKIPPED, and Run returns a non-nil summary
>   error. Add a test that a cost-ceiling breach still aborts immediately
>   (does not get swallowed as a skippable task failure).

Test 1 — diamond with a failing mid task:
- Build a diamond DAG: `A → {B, C} → D` (D depends on both B and C).
- Configure the stub provider so that `C` (a mid task) fails on its merits
  through all retries, while `A`, `B` run to PASSED.
- Assert: `B` ends PASSED (the independent branch ran), `C` ends FAILED, `D`
  ends SKIPPED (it depends transitively on failed `C`), and `Run` returns a
  non-nil summary error mentioning the failed and skipped IDs.

Test 2 — cost-ceiling breach aborts immediately:
- Set `execution.max_cost_usd` (or `max_cost_per_task_usd`) low enough that a
  task breaches it.
- Assert: `Run` returns the fatal cost-ceiling error directly, and that this
  error is NOT swallowed/converted into a skip-and-continue summary (e.g.
  assert the error message is the cost-ceiling message, and that a later
  independent task did not run / was not marked PASSED).

Inspect existing orchestrator tests first to reuse the established stub provider
setup, event-channel draining, and temp-project/tasks.md fixtures. Match that
style rather than inventing a new harness.

# Dependency-failure resilience (CH-07)

## Problem
In `internal/orchestrator/orchestrator.go`, `Run` calls `executeTask` and on any
error does `return err`, aborting the ENTIRE run. So one failed task kills every
other independent branch of the DAG, even tasks that don't depend on the failure.

## Objective
When a task FAILS on its merits (worker/review failed after all retries), the
orchestrator should:
1. Mark that task FAILED (already happens inside executeTask).
2. Mark every task that transitively depends on it as SKIPPED (it can never
   become ready), emitting an EventTaskComplete with Status=SKIPPED and a
   message like "skipped: depends on failed task <id>".
3. Continue executing the remaining independent ready tasks.
4. After the loop, if any task ended FAILED, return a single summary error
   listing the failed and skipped task IDs (so the process still exits non-zero),
   but only AFTER all runnable branches have been attempted.

## Critical distinction — fatal vs task-level errors
`executeTask` returns errors for TWO different situations. Only one should be
treated as "skip dependents and continue"; the other must still abort the whole
run immediately:

- **Task-level failure** (worker failed after maxRetries, review FAIL after
  maxRetries, reviewer error after maxRetries): skip dependents, continue.
- **Fatal run errors** that must abort everything immediately: cost ceiling
  exceeded (`MaxCostUSD`/`MaxCostPerTaskUSD`), human-prompt escalation
  (ActionHumanPrompt), and context cancellation (ctx.Err()).

Implement this by introducing a distinct error type (e.g. a `fatalError`
wrapper, or a sentinel checked via errors.As/errors.Is) that `executeTask`
returns for the fatal cases, and a plain task-failure signal for the rest. In
`Run`, after calling executeTask: if the error is fatal → return it (abort); if
it is a task failure → record it, mark dependents skipped, and continue the
scheduler loop. Be careful with `--task`/`--single` modes: a single targeted
task that fails should still return an error.

## DAG support
You may add a helper to `internal/dag/dag.go` such as
`Dependents(id string) []string` or `TransitiveDependents(id string) []string`
to compute the set of tasks reachable from a failed node via the dependents
edges (the `node.dependents` adjacency already exists). Add a unit test for it.

## Validation
- `go build ./...` and `go test ./...` pass.
- Add an orchestrator test (using the stub provider) with a diamond DAG where
  one mid task fails: prove the independent branch still runs to PASSED, the
  dependents of the failed task are SKIPPED, and Run returns a non-nil summary
  error. Add a test that a cost-ceiling breach still aborts immediately
  (does not get swallowed as a skippable task failure).

## Files (reference)
- internal/orchestrator/orchestrator.go — Run loop + executeTask
- internal/dag/dag.go — node.dependents, NextReady, add Dependents helper
- internal/types/types.go — TaskStatus (StatusSkipped exists)

# Structured handoff & anchor contract (CH-03)

Today the anchor (`internal/anchor`) has three fields that are declared but
never populated by the orchestrator, so cross-task context is weak:
- `CompletedTask.Decisions` — always empty.
- `AnchorState.NextTaskContext` (via `TaskResult.NextTaskContext`) — always "".
- `CompletedTask.FilesCreated/FilesModified` are filled from the task's
  *planned* files (`t.Files.Create/Modify`), NOT what the worker actually
  changed.

## Objective A — Real changed files from git
After a task passes, populate `FilesCreated`/`FilesModified` from what actually
changed on disk, not the plan.
- Add a helper (e.g. in `internal/recovery` or a small new helper in the
  orchestrator) that runs `git diff --name-status HEAD` in the workDir and
  returns created files (status `A` or untracked `??` via
  `git status --porcelain`) vs modified files (status `M`). Capture this BEFORE
  the checkpoint commit (the working tree still shows the diff vs HEAD).
- Use these real lists when building the `anchor.TaskResult.Completed`.
- Exclude paths under `.corvex/`.

## Objective B — Mandatory structured task report from the worker
The worker must end its response with a structured report block, parsed like the
reviewer's VERDICT. Define the contract in `buildWorkerPrompt`
(`internal/orchestrator/worker.go`):

    TASK-REPORT:
    SUMMARY: <1-3 sentences: what was implemented>
    DECISIONS:
    - <key decision or tradeoff made>
    - <...>
    HANDOFF: <what the next task's worker needs to know: new interfaces,
    invariants, gotchas. One short paragraph.>

- Add a parser (new function, e.g. `parseTaskReport(output string)` returning a
  struct {Summary string; Decisions []string; Handoff string}). Tolerant of
  markdown wrapping / casing like the verdict parser. Put it next to the worker
  or in a new `report.go` in the orchestrator package, with table-driven tests.
- In `executeTask`, after a PASS, parse the worker's output (the final passing
  attempt's `result.Output`). Populate:
  - `CompletedTask.Summary` — prefer the report SUMMARY; fall back to the
    reviewer summary if the report summary is empty.
  - `CompletedTask.Decisions` — from the report DECISIONS list.
  - `TaskResult.NextTaskContext` — from the report HANDOFF.

## Objective C — Missing handoff is a failure, not a silent empty anchor
- If a passing task's worker output has NO parseable TASK-REPORT block (or the
  HANDOFF is empty), do NOT silently record an empty anchor entry. Treat it as a
  failed attempt: set a diagnosis ("your response was missing the required
  TASK-REPORT block with a HANDOFF; re-run and include it") and retry. If it is
  still missing on the last attempt, fail the task with a clear error.
- Exception: the LAST task in the DAG (no dependents / NextTask == "") may have
  an empty HANDOFF — there is nothing to hand off to. Only require HANDOFF when
  there is a next task.

## Validation
- `go build ./...` and `go test ./...` pass.
- Tests: parseTaskReport handles a well-formed block, tolerates markdown, and
  reports missing block; the git changed-files helper classifies created vs
  modified correctly (drive with a temp git repo like recovery_test.go).
- Do not break existing orchestrator tests — the stub provider's output may need
  a TASK-REPORT block added so PASS-path tests still pass; update those stubs.

## Files (reference)
- internal/anchor/anchor.go — TaskResult, Update, GenerateContext, CompletedTask usage
- internal/types/types.go — CompletedTask, AnchorState
- internal/orchestrator/worker.go — buildWorkerPrompt
- internal/orchestrator/orchestrator.go — executeTask PASS branch (anchor.Update call)
- internal/recovery/recovery.go — git helpers pattern

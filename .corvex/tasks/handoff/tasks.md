---
dag:
    S01: []
    S02: []
    S03:
        - S01
        - S02
    S04:
        - S03
    S05:
        - S03
        - S04
---

## S01 — Git changed-files helper (real created vs modified) ✅ PASSED

```yaml
type: backend
```

### O que fazer
Add a helper that inspects the working tree in `workDir` and returns the files
the worker actually changed, split into created vs modified. Follow the git
helper pattern in `internal/recovery/recovery.go`.

The classification rules come straight from the spec and must be implemented
exactly:

> [verbatim from spec.md §Objective A]
> Add a helper (e.g. in `internal/recovery` or a small new helper in the
> orchestrator) that runs `git diff --name-status HEAD` in the workDir and
> returns created files (status `A` or untracked `??` via
> `git status --porcelain`) vs modified files (status `M`). Capture this BEFORE
> the checkpoint commit (the working tree still shows the diff vs HEAD).

> [verbatim from spec.md §Objective A]
> Exclude paths under `.corvex/`.

So:
- Run `git diff --name-status HEAD` in `workDir`.
- Status `A` → created; status `M` → modified.
- Also run `git status --porcelain`; untracked entries (`??`) → created.
- Drop any path under `.corvex/` from both lists.
- This helper is meant to be invoked BEFORE the checkpoint commit, while the
  tree still diffs against HEAD.

---

## S02 — TASK-REPORT contract + parseTaskReport parser ⬜ PENDING

```yaml
type: backend
```

### O que fazer
Define the worker's mandatory structured report contract in `buildWorkerPrompt`
(`internal/orchestrator/worker.go`) and add a tolerant parser, modeled on the
reviewer's VERDICT parsing.

The worker must end its response with this exact block. Quote it into the prompt
verbatim:

> [verbatim from spec.md §Objective B]
> TASK-REPORT:
> SUMMARY: <1-3 sentences: what was implemented>
> DECISIONS:
> - <key decision or tradeoff made>
> - <...>
> HANDOFF: <what the next task's worker needs to know: new interfaces,
> invariants, gotchas. One short paragraph.>

> [verbatim from spec.md §Objective B]
> Add a parser (new function, e.g. `parseTaskReport(output string)` returning a
> struct {Summary string; Decisions []string; Handoff string}). Tolerant of
> markdown wrapping / casing like the verdict parser.

- Add `parseTaskReport(output string) struct{Summary string; Decisions []string; Handoff string}`
  (plus an ok/found signal so callers can detect a missing block — see S04).
- Place it next to the worker or in a new `report.go` in the orchestrator
  package.
- Tolerate markdown wrapping (e.g. ```` ``` ````, leading `#`/`*`) and casing,
  exactly like the existing verdict parser.
- DECISIONS is a list of `-` bullet lines.

---

## S03 — Wire real files + report into anchor on PASS ⬜ PENDING

```yaml
type: backend
depends_on: [S01, S02]
```

### O que fazer
In `executeTask`, on the PASS branch (the `anchor.Update` call), use the S01
helper and S02 parser to populate the anchor with real data instead of plan
data and empty fields.

> [verbatim from spec.md §Objective A]
> Use these real lists when building the `anchor.TaskResult.Completed`.

> [verbatim from spec.md §Objective B]
> In `executeTask`, after a PASS, parse the worker's output (the final passing
> attempt's `result.Output`). Populate:
> - `CompletedTask.Summary` — prefer the report SUMMARY; fall back to the
>   reviewer summary if the report summary is empty.
> - `CompletedTask.Decisions` — from the report DECISIONS list.
> - `TaskResult.NextTaskContext` — from the report HANDOFF.

- Capture created/modified via S01 helper BEFORE the checkpoint commit.
- Build `CompletedTask.FilesCreated`/`FilesModified` from those real lists.
- Parse the final passing attempt's `result.Output` with `parseTaskReport`.
- `CompletedTask.Summary` = report SUMMARY, falling back to reviewer summary
  when the report summary is empty.
- `CompletedTask.Decisions` = report DECISIONS.
- `TaskResult.NextTaskContext` = report HANDOFF.

---

## S04 — Missing handoff is a failure (retry, then hard fail) ⬜ PENDING

```yaml
type: backend
depends_on: [S03]
```

### O que fazer
A passing task with no parseable TASK-REPORT (or empty HANDOFF) must not record
a silent empty anchor entry. Treat it as a failed attempt and retry; if still
missing on the last attempt, fail the task.

> [verbatim from spec.md §Objective C]
> If a passing task's worker output has NO parseable TASK-REPORT block (or the
> HANDOFF is empty), do NOT silently record an empty anchor entry. Treat it as a
> failed attempt: set a diagnosis ("your response was missing the required
> TASK-REPORT block with a HANDOFF; re-run and include it") and retry. If it is
> still missing on the last attempt, fail the task with a clear error.

> [verbatim from spec.md §Objective C]
> Exception: the LAST task in the DAG (no dependents / NextTask == "") may have
> an empty HANDOFF — there is nothing to hand off to. Only require HANDOFF when
> there is a next task.

- When there IS a next task: missing block or empty HANDOFF → set diagnosis
  exactly `your response was missing the required TASK-REPORT block with a HANDOFF; re-run and include it`
  and retry like any failed attempt.
- On the last attempt still missing → fail the task with a clear error.
- When there is NO next task (`NextTask == ""` / no dependents): empty HANDOFF
  is allowed; do not fail.

---

## S05 — Update stubs/tests & validate ⬜ PENDING

```yaml
type: review
depends_on: [S03, S04]
```

### O que fazer
Ensure existing PASS-path tests still pass under the new mandatory contract.

> [verbatim from spec.md §Validation]
> Do not break existing orchestrator tests — the stub provider's output may need
> a TASK-REPORT block added so PASS-path tests still pass; update those stubs.

- Add a valid TASK-REPORT block (with non-empty HANDOFF for non-last tasks) to
  the stub provider output used by PASS-path orchestrator tests.
- Run full build and test suite.

## S01 — Extract doctor checks into a single callable function 🔄 RUNNING

```yaml
type: backend
```

### O que fazer
Refactor `cmd/doctor.go` so the full set of doctor checks can be invoked
programmatically (e.g. from `corvex run`) without printing anything.

> [verbatim from spec.md §Objective B]
> Refactor doctor so the checks are callable as a function returning the results
> ([]checkResult) without printing (extract from runDoctor if needed).

Today `runChecks(cfg)` already returns `[]checkResult` but it **omits**
`checkMCPGitignore`, which `runDoctor` appends separately because it needs
`workDir`. Introduce a single function that returns the complete set, e.g.:

```go
func allChecks(cfg *config.Config, workDir string) []checkResult {
    results := runChecks(cfg)
    results = append(results, checkMCPGitignore(cfg, workDir))
    return results
}
```

Then change `runDoctor` to call `allChecks(cfg, workDir)` instead of building the
slice inline, so there is exactly one source of truth for "all checks".

The returned `checkResult` carries `status checkStatus` where `checkFail`
corresponds to the `✗` prefix and `checkWarn` to `⚠` (see `prefix()`).

---

## S02 — `doctor` pre-run gate + `--skip-doctor` flag ⬜ PENDING

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
In `cmd/run.go` `runRun`, before starting the orchestrator, run the doctor checks
and abort if any FAILED.

> [verbatim from spec.md §Objective B]
> In `corvex run`, before starting, run the checks; if any FAILED (✗), abort with
> the failed check messages and a hint to run `corvex doctor`. Warnings (⚠) do
> not block.

> [verbatim from spec.md §Objective B]
> Add a `--skip-doctor` flag on run to bypass the gate.

Implement the gate as a small standalone function so it can be unit-tested
directly (per S05). Suggested shape:

```go
// returns an error listing the failed checks when any check is checkFail.
func doctorGate(results []checkResult) error
```

Wire it into `runRun`: after `loadConfig()` / `requireCorvexDir()` succeed and
before constructing/starting the orchestrator, call
`allChecks(cfg, workDir)` (from S01) and pass to `doctorGate`. If it returns an
error, return it (which aborts the run).

The abort error message must include the failed check messages and the hint to
run `corvex doctor`.

---

## S03 — Cost preview builder (pure function) + `--yes/-y` flag ⬜ PENDING

```yaml
type: backend
```

### O que fazer
Add a **pure, testable** function that builds the cost-preview text, and register
the `--yes/-y` flag. No prompting yet (that is S04) — this task is just the
building blocks so S05 can unit-test the preview string.

> [verbatim from spec.md §Objective A]
> Before executing (in cmd/run.go runRun, after resolving the project and BEFORE
> starting the orchestrator), print a short preview: number of PENDING tasks that
> will run, and the configured ceilings (execution.max_cost_usd,
> max_cost_per_task_usd). If activity.jsonl has history, optionally show prior
> spend via activity.Summarize.

> [verbatim from spec.md §Objective A]
> Add a persistent `--yes/-y` flag (or run-local) that skips the prompt (for CI
> and scripts).

Implement a function that, given the resolved pending count and the config
ceilings, returns the preview string. Suggested shape:

```go
// buildCostPreview returns the human-readable preview line(s):
// pending task count + configured ceilings (max_cost_usd, max_cost_per_task_usd).
func buildCostPreview(pendingCount int, cfg *config.Config) string
```

- Pending count = number of tasks with `types.StatusPending` (the same notion
  `dryRun` computes when it marks `→` for `t.Status == types.StatusPending`).
- Ceilings come from `cfg.Execution.MaxCostUSD` and
  `cfg.Execution.MaxCostPerTaskUSD` (both `float64`; defaults 25 and 5). When a
  ceiling is `0` it means "no cap" — render it as such (mirror the wording style
  of `checkCostCeilings`, e.g. `max_cost_usd=0 (no cap)`), otherwise format with
  two decimals like `max_cost_usd=%.2f`.
- Register `runCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "skip the
  confirmation prompt (for CI/scripts)")` in `init()`, backed by a package-level
  `var runYes bool`.

---

## S04 — Wire preview + confirmation prompt into `runRun` ⬜ PENDING

```yaml
type: backend
depends_on: [S02, S03]
```

### O que fazer
Wire the preview and the interactive confirmation into `runRun`, after resolving
the project / doctor gate and before starting the orchestrator.

> [verbatim from spec.md §Objective A]
> When stdout is a TTY and neither --yes nor --plain is set, prompt for
> confirmation ("Proceed? [y/N] ") and abort if not confirmed. Reading from
> stdin; default No.

> [verbatim from spec.md §Objective A]
> Non-interactive (no TTY) runs must NOT block on the prompt — treat absence of a
> TTY as auto-yes (so piped/CI runs proceed), but still print the preview line.

> [verbatim from spec.md §Objective A]
> Do not prompt for --dry-run.

Steps in `runRun`, ordered:
1. (S02) doctor gate already ran.
2. Resolve pending tasks (parse `tasks.md`, count `types.StatusPending`).
   Optionally call `activity.Summarize(workDir, project)` and, if it has history,
   include prior spend in the preview.
3. Always **print** the preview via `buildCostPreview(...)` (S03) — including in
   non-TTY runs.
4. Decide whether to prompt:
   - Prompt only when `isInteractive()` (TTY) **and** `!runYes` **and**
     `!runPlain`.
   - On prompt, write exactly `Proceed? [y/N] ` and read a line from stdin.
     Confirm only on an affirmative answer (`y`/`yes`, case-insensitive);
     **default is No** — empty input or anything else aborts.
   - When not prompting (no TTY, or `--yes`, or `--plain`), proceed without
     blocking.
5. `--dry-run` returns earlier (lines 104-108) and must never reach the prompt.

---

## S05 — Tests: preview logic, prompt bypass, doctor gate ⬜ PENDING

```yaml
type: review
depends_on: [S02, S03, S04]
```

### O que fazer
Add tests covering the new behaviour.

> [verbatim from spec.md §Objective A]
> Add a test for the preview-building logic (the pending count + ceilings string),
> and that --yes / non-TTY bypasses the prompt.

> [verbatim from spec.md §Objective B]
> Add a test: a config with a failing check causes run to abort early (you can
> test the gate function directly rather than a full run).

Tests to add (package `cmd`, mirror the style in `cmd/doctor_test.go`):
1. **Preview building** — call `buildCostPreview(pendingCount, cfg)` and assert
   the returned string contains the pending count and both ceilings (cover both
   the `0 (no cap)` case and the `%.2f` formatted case).
2. **Prompt bypass** — assert that the prompt-decision logic treats `--yes` and
   non-TTY (no `isInteractive()`) as auto-proceed without reading stdin. Test the
   decision predicate / gate function directly rather than driving a full
   interactive run.
3. **Doctor gate** — build a `[]checkResult` containing a `checkFail` and assert
   `doctorGate(...)` returns an error whose message references the failed check;
   assert a results slice with only `checkPass`/`checkWarn` returns `nil`.

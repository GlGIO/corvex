# CLI friendliness — cost preview & doctor pre-gate

Keep `go build ./...` and `go test ./...` green. Match cobra style.

## Objective A — Cost preview + confirmation before a run
`corvex run` spends real LLM money with no warning. Make it informed.
- Before executing (in cmd/run.go runRun, after resolving the project and BEFORE
  starting the orchestrator), print a short preview: number of PENDING tasks that
  will run, and the configured ceilings (execution.max_cost_usd,
  max_cost_per_task_usd). If activity.jsonl has history, optionally show prior
  spend via activity.Summarize.
- When stdout is a TTY and neither --yes nor --plain is set, prompt for
  confirmation ("Proceed? [y/N] ") and abort if not confirmed. Reading from
  stdin; default No.
- Add a persistent `--yes/-y` flag (or run-local) that skips the prompt (for CI
  and scripts). Non-interactive (no TTY) runs must NOT block on the prompt —
  treat absence of a TTY as auto-yes (so piped/CI runs proceed), but still print
  the preview line.
- Do not prompt for --dry-run.
- Add a test for the preview-building logic (the pending count + ceilings string),
  and that --yes / non-TTY bypasses the prompt.

## Objective B — `doctor` as a pre-run gate
The doctor checks (cmd/doctor.go runChecks) catch config errors that otherwise
only blow up mid-run.
- Refactor doctor so the checks are callable as a function returning the results
  ([]checkResult) without printing (extract from runDoctor if needed).
- In `corvex run`, before starting, run the checks; if any FAILED (✗), abort with
  the failed check messages and a hint to run `corvex doctor`. Warnings (⚠) do
  not block.
- Add a `--skip-doctor` flag on run to bypass the gate.
- Add a test: a config with a failing check causes run to abort early (you can
  test the gate function directly rather than a full run).

## Files (reference)
- cmd/run.go — runRun
- cmd/doctor.go — runDoctor, runChecks, checkResult, checkStatus
- internal/activity — Summarize (prior spend)
- internal/config — Execution ceilings

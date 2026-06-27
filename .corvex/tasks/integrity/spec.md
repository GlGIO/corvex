# State integrity & cost accounting

Three independent hardening changes to the orchestrator and its state layer.
All must keep `go build ./...` and `go test ./...` green.

## Objective A — No false success from truncated provider output (CH-04)
The Claude CLI emits a final NDJSON line `{"type":"result", ...}` carrying the
cost/tokens and the final result text. If the stream ends WITHOUT a `result`
line (process killed, pipe truncated, network cut), today the provider returns
a success with whatever partial output it accumulated — a silent false success.

Requirements:
- In `internal/provider/claude/claude.go`, both `ExecuteWithProgress` and
  `ParseFullOutput` must track whether a `result` line was seen.
- If the command exited 0 but NO `result` line was observed, return a non-nil
  error (e.g. "claude cli produced no result line (truncated output?)") instead
  of a successful `*types.ExecuteResult`. When the command already exited
  non-zero, keep the existing error behavior.
- Add/extend tests in `internal/provider/claude/claude_test.go` proving: a
  stream with a result line succeeds; a stream that ends without a result line
  (but exit 0) returns an error.

## Objective B — Atomic tasks.md writes (CH-04)
`internal/task/writer.go` `WriteTasksFile` calls `os.WriteFile` directly. A
crash mid-write corrupts tasks.md and a resume then can't parse it.

Requirements:
- Write to a temp file in the SAME directory (so rename is atomic on the same
  filesystem), then `os.Rename` it over the destination. Use a temp name
  derived from the destination (e.g. `<path>.tmp-<pid>` or `os.CreateTemp` in
  `filepath.Dir(path)`). Preserve 0644 perms. On any error, clean up the temp.
- Existing writer tests must still pass; add a test that a successful write
  leaves no leftover temp files in the directory.

## Objective C — Cost ceiling also covers failures (CH-05)
In `internal/orchestrator/orchestrator.go` `executeTask`, the cumulative and
per-task cost ceiling checks (`MaxCostUSD`, `MaxCostPerTaskUSD`) live INSIDE the
`if reviewResult.Verdict == VerdictPass` branch. So retries and failed attempts
burn budget without ever being counted or capped — three $5 retries = $15
invisible.

Requirements:
- Accumulate the cost of EVERY attempt (worker result cost + reviewer result
  cost when available) into a per-task running total and into `*totalCostUSD`,
  regardless of pass/fail. A failed worker attempt may have a nil result —
  guard against nil (cost 0 in that case).
- After each attempt (pass OR fail), check both ceilings against the updated
  totals and return the same actionable error the PASS branch returns today
  when a ceiling is exceeded. Do not double-count the PASS attempt (move the
  accounting so each attempt is counted exactly once).
- Keep the existing behavior that a PASS returns nil after bookkeeping.
- Add a test in `internal/orchestrator/` proving cumulative cost from failed
  attempts triggers the `MaxCostUSD` abort (you can drive this with the existing
  fake/stub provider used by other orchestrator tests).

## Validation
- `go build ./...` passes.
- `go test ./...` passes.
- No regression in existing orchestrator/provider/task tests.

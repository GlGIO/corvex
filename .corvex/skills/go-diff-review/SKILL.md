---
name: go-diff-review
description: How to review a Go diff in this repo. Use for review-type tasks — inspect the git diff and report concrete, prioritized findings on correctness, safety, and house conventions.
---

# Reviewing a Go diff

Use this when a task is to review code (type `review`). Read the actual change,
not the description: run `git diff` (and `git diff --stat`) and read the touched
files in full where context matters.

## What to check, in priority order

1. **Correctness** — does it do what the task/spec requires? Trace the happy path
   AND the edge/error paths. Off-by-one, wrong operator, swapped args, nil deref,
   unchecked map lookups, ignored returns.
2. **Errors** — every error wrapped with `%w` and context; no swallowed errors
   (`_ = err` only with a justified comment); messages actionable. No `panic`/
   `os.Exit` in `internal/...`.
3. **Concurrency** — any shared state touched without holding the orchestrator
   mutex (`o.mu`) or the right helper? Goroutines that can leak or race? Channel
   sends that can block forever (need a `select` on ctx/done)? If the change is
   in the orchestrator, mentally run `go test -race`.
4. **Resource & FS safety** — files that should be written atomically (temp+
   rename) but aren't; missing `defer Close()`; secrets written world-readable
   (must be `0o600`); writes under `.corvex/` from worker-facing paths.
5. **Tests** — is the new behavior covered? Are failure paths tested, not just
   the happy case? Are tests table-driven and hermetic (no network, no real
   `claude`)? A behavioral change with no test is a finding.
6. **Conventions & reuse** — does it match surrounding style and **reuse existing
   helpers** instead of reinventing (e.g. `loadConfig`, `projectNames`,
   `setStatus`, `printJSON`)? New config keys documented in README?
7. **Scope** — unrelated churn, dead code, debug prints, TODOs left behind.

## How to report
- Lead with the verdict-relevant summary: is it correct and safe to ship?
- List findings as `severity (high/med/low) — file:line — what + why + fix`.
- Quote the offending line. Don't invent issues; if the diff is clean, say so
  plainly and note what you verified (built? tests run? race?).
- Prefer a few real, high-signal findings over a long list of nits.

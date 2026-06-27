---
name: go-corvex-conventions
description: House conventions for the Corvex Go codebase. Use this whenever writing or modifying Go code in this repo (backend/general tasks) so the change matches the existing style, testing, and safety patterns.
---

# Corvex Go conventions

Follow these when adding or changing Go code in this repository. They reflect the
existing codebase — match it, don't invent a new style.

## Errors
- Wrap with context: `fmt.Errorf("doing X: %w", err)`. Always `%w` (not `%v`) so
  callers can `errors.Is`/`errors.As`.
- Return errors; don't `log.Fatal`/`os.Exit` outside `cmd.Execute`. Library code
  (`internal/...`) never exits the process.
- Make error messages actionable: say what to do next (e.g. "run `corvex doctor`").

## Tests
- Table-driven with subtests: `for _, tt := range tests { t.Run(tt.name, ...) }`.
- Put tests in the same package (`package foo`), file `<thing>_test.go`.
- Use `t.TempDir()` for filesystem tests; for git, init a repo in the temp dir.
- Assert behavior, not implementation. Cover the failure paths, not just happy.
- Never depend on network or a real `claude` binary — use the existing fakes
  (e.g. `mockProvider` in the orchestrator tests) or `CORVEX_CLAUDE_BIN`.

## CLI commands (cmd/)
- One file per command: `var fooCmd = &cobra.Command{...}` + `func init() {
  rootCmd.AddCommand(fooCmd) }`.
- `RunE` returns an `error` (don't print+exit yourself; `cmd/root.go` `Execute`
  does that). Use `cobra.NoArgs`/`ExactArgs` to validate arity.
- Load config via the shared `loadConfig()` helper and guard with
  `requireCorvexDir(workDir)`. Reuse `projectNames`/`projectDir` helpers.
- A `<project>` argument should set `ValidArgsFunction: completeProjectArg`.

## State & filesystem safety
- Write important files atomically: temp file in the same dir + `os.Rename`
  (see `internal/anchor` and `internal/task/writer.go`).
- Never write under `.corvex/` from worker-facing code paths; the orchestrator
  owns that state. Files with secrets (e.g. mcp.json) are `0o600`.

## Concurrency
- The orchestrator can run tasks in parallel. Anything touching shared state
  (`completed`, cumulative cost, `anchorState`, tasks.md/anchor.yaml writes,
  git checkpoints, the activity ledger) must hold `o.mu` (or use the existing
  `setStatus`/`addCost`/`markStagePassed` helpers). Validate with `go test -race`.

## Before finishing (definition of done)
- `go build ./...` AND `go test ./...` both pass.
- New behavior has a test. New config keys are documented in `README.md`.
- Match the surrounding file's style (imports grouping, comment density, naming).

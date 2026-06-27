# Corvex — project conventions (for AI workers)

Corvex is a Go CLI (cobra) that orchestrates AI agents. Module: `github.com/giovannialves/corvex`.

## Build & test (ALWAYS run before finishing)
- Build: `go build ./...`
- Test: `go test ./...`
- A change is NOT done unless BOTH pass.

## Layout
- `cmd/` — cobra commands (one file per command, registered in init() via rootCmd.AddCommand). `root.go` has rootCmd. `helpers.go` has shared helpers like loadConfig(), requireCorvexDir(), projectDir().
- `internal/config/config.go` — Config struct (Provider, Sandbox, Execution, Review, Context, AgentRouting, Validate), Load(), Default(), applyDefaults().
- `internal/orchestrator/` — Planner, Worker, Reviewer, escalation, events, the Run loop.
- `internal/provider/claude/claude.go` — Claude CLI provider.
- `internal/sandbox/` — docker/local/nix/devcontainer sandboxes.

## Conventions
- Match the surrounding code style. Table-driven tests with `t.Run`. Errors wrapped with `fmt.Errorf("...: %w", err)`.
- New cobra commands: create `cmd/<name>.go`, define `var <name>Cmd = &cobra.Command{...}`, register in `func init()`.
- Do NOT edit files under `.corvex/` — that is orchestrator state.

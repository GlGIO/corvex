# corvex doctor — config preflight

## Objective
Add a new CLI command `corvex doctor` that validates the loaded `.corvex/config.yaml`
and the local environment BEFORE a run, so misconfiguration fails fast with a clear
message instead of breaking deep inside an orchestration run.

## Requirements
- New cobra command in `cmd/doctor.go`, registered like the other commands, runnable as `corvex doctor`.
- It loads the config via the existing `loadConfig()` helper (see `cmd/helpers.go`) and runs a series of independent checks, printing each as a line prefixed with `✓` (pass), `✗` (fail), or `⚠` (warning).
- Checks to perform:
  1. **provider**: `provider.default` is a known provider (currently only `claude-cli`). The provider binary is resolvable on PATH (the claude binary, honoring the `CORVEX_CLAUDE_BIN` env var the same way `internal/provider/claude` does).
  2. **models**: `provider.models.planner`, `.worker`, and `.reviewer` are all non-empty.
  3. **sandbox**: `sandbox.type` is one of `local`/`docker`, or `sandbox.profile` is one of `nix`/`devcontainer`. If `docker`, warn (not fail) when the `docker` binary is not on PATH. If `sandbox.mount` is set, it must be parseable.
  4. **escalation**: every policy under `review.escalation` has a known `action` (`upgrade-model`, `spawn-investigation`, `human-prompt`) and `after >= 1`; `upgrade-model` policies must set `to`.
  5. **cost ceilings**: warn if `execution.max_cost_usd` is 0 (no cap) and warn if `max_cost_per_task_usd` is 0.
- Exit non-zero (return an error from RunE) if ANY check fails (✗). Warnings (⚠) do not cause a non-zero exit.
- Print a final summary line, e.g. `doctor: N checks, X passed, Y warnings, Z failed`.

## Validation
- `go build ./...` passes.
- `go test ./...` passes.
- A new test file `cmd/doctor_test.go` covers: a valid config passes; an unknown provider fails; an `upgrade-model` policy missing `to` fails; a missing model fails. Use table-driven tests in the style of the existing `cmd/*_test.go`.
- Running `corvex doctor` in a project with the default config prints check lines and a summary.

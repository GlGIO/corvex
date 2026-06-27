# corvex version --json

## Objective
Add a `--json` flag to the existing `corvex version` subcommand so tools can read
the version machine-readably.

## Requirements
- Add a `--json` boolean flag to the `version` command (cmd/version.go).
- When `--json` is set, print a single JSON object `{"version":"<v>"}` where `<v>`
  is `github.com/giovannialves/corvex/internal/types`.Version, using encoding/json.
  Print nothing else.
- When `--json` is absent, the existing human output is unchanged.
- This is a backend task.

## Validation
- `go build ./...` and `go test ./...` pass.
- A test in cmd/ covers both the JSON output (contains the version) and that the
  default output is unchanged.

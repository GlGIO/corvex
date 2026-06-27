# CLI friendliness — basics

Three small, independent CLI ergonomics improvements. Keep `go build ./...` and
`go test ./...` green. Match existing cobra command style (see cmd/*.go).

## Objective A — `version` subcommand
`corvex --version` already works (cobra, via rootCmd.Version). But users often
type `corvex version` (no dashes), which currently errors with "unknown command".
- Add a `version` subcommand in `cmd/version.go` that prints the same string the
  version template produces: `corvex <version>` where version is
  `github.com/giovannialves/corvex/internal/types`.Version.
- Args: cobra.NoArgs. Registered via rootCmd.AddCommand in init().
- Add a test in cmd/ asserting the command runs and the output contains the
  version string.

## Objective B — Respect NO_COLOR and add a global --no-color flag
Corvex colorizes output (lipgloss in the TUI, charmbracelet/log in --plain).
Make color suppression a first-class, standard behavior.
- Add a PERSISTENT flag `--no-color` on rootCmd (so it applies to every command).
- In rootCmd's PersistentPreRun (add one if absent), disable color when EITHER
  the `--no-color` flag is set OR the `NO_COLOR` environment variable is non-empty
  (the de-facto standard, https://no-color.org). Disable by setting lipgloss's
  color profile to ascii (e.g. `lipgloss.SetColorProfile(termenv.Ascii)`) and
  turning off the charmbracelet/log default logger's color/styles if applicable.
- Do not break the existing TUI when color IS enabled.
- Add a test that sets NO_COLOR and asserts the disabling path runs without error
  (a light test is fine — exercising the PersistentPreRun logic).

## Objective C — "did you mean" for unknown project names
Commands that take a <project> arg (run, status, logs, reset) currently fail with
a bare error when the project doesn't exist. Make it helpful.
- Add a shared helper in `cmd/helpers.go`, e.g.
  `projectNames(workDir string) []string` that returns the directory names under
  `.corvex/tasks/` that contain a spec.md or tasks.md (reuse the listing logic
  that cmd/list.go already has — refactor list.go to use the shared helper too,
  no behavior change).
- Add a helper `suggestProject(workDir, name string) string` that returns the
  closest existing project name (simple case-insensitive prefix/substring match,
  or Levenshtein distance <= 2) or "" if none is close.
- In `corvex run` (cmd/run.go), when the project's spec.md/tasks.md does not
  exist, return an error that lists available projects and, when a close match
  exists, adds "did you mean <X>?".
- Add a test for projectNames and suggestProject (table-driven).

## Validation
- go build ./... and go test ./... pass.
- `corvex version` prints the version; `NO_COLOR=1 corvex status` runs without color.

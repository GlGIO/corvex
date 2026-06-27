# CLI friendliness — clean output & completion

Keep `go build ./...` and `go test ./...` green. Match existing cobra style.

## Objective A — Clean non-TTY / --plain output + --quiet
Today `corvex run --plain` routes orchestrator events through the default
charmbracelet/log logger (see drainEvents in cmd/run.go), producing noisy lines
like `2026/06/26 23:12:09 INFO task passed task=S02 cost=$0.41`. For humans and
CI this should be a clean, scannable progress format.

Requirements:
- Add a clean plain renderer for orchestrator events used by the `--plain` path
  (or non-interactive auto-fallback). Format suggestions (no timestamps, no
  key=val noise):
    `▶ S02  Implement endpoints` (task start)
    `✓ S02  passed · 1m12s · $0.41` (task complete passed)
    `✗ S02  failed · <message>` (failed)
    `↻ S02  retry 1` (retry)
    `⏭ S04  skipped (depends on failed S02)`
    `■ planning…`, `✓ plan ready (N tasks)`, `✓ done` etc.
  Use the existing tui styles where helpful, but it must degrade to plain ASCII
  when color is disabled (NO_COLOR / not a TTY).
- Add a persistent `-q/--quiet` flag on rootCmd: when set, the plain renderer
  prints only failures/errors and the final summary (suppress per-task progress).
- Keep `--plain` behavior backward compatible enough that scripts still see
  task outcomes; just cleaner. The TUI path (default on a TTY) is unchanged.
- Add a test for the renderer: given a sequence of events, assert the rendered
  lines (and that quiet mode suppresses non-error progress).

## Objective B — Shell completion
- Ensure `corvex completion [bash|zsh|fish|powershell]` works (cobra provides it
  by default unless disabled; verify it is enabled and add a short usage note in
  README under a "Shell completion" heading).
- Add DYNAMIC completion for the <project> argument of the commands that take one
  (run, status, logs, reset): register a ValidArgsFunction that returns the
  project names under .corvex/tasks/ (reuse the projectNames helper from the
  cli-basics batch; if that helper does not exist yet, add it). This makes
  `corvex run <TAB>` complete real project names.
- Add a test that the ValidArgsFunction returns the expected project names for a
  temp .corvex/tasks/ layout.

## Validation
- go build ./... and go test ./... pass.
- `corvex run <proj> --plain` shows clean lines; `-q` suppresses progress;
  `corvex completion zsh` emits a script; `corvex run <TAB>` would complete
  project names.

## Files (reference)
- cmd/run.go — drainEvents (plain event rendering), isInteractive
- cmd/root.go — persistent flags, command registration
- cmd/helpers.go — projectNames helper (shared)
- internal/tui/styles.go — styles/glyphs to reuse (GlyphPassed, etc.)
- internal/orchestrator/events.go — Event types

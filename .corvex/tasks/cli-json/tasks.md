---
dag:
    S01: []
    S02:
        - S01
    S03:
        - S01
    S04:
        - S01
    S05:
        - S01
    S06:
        - S02
        - S03
        - S04
        - S05
---

## S01 — Shared JSON output plumbing + flag helper ✅ PASSED

```yaml
type: backend
```

### O que fazer
Create the shared scaffolding every `--json` command needs so the four command
tasks (S02–S05) can be built independently in parallel.

- Add a reusable way to register a `--json` bool flag on a command (e.g. a small
  helper in `cmd/` such as `addJSONFlag(cmd)` returning a `*bool`, or a package
  var per command following the existing flag pattern in `cmd/`). Inspect the
  existing flag-registration style in `cmd/list.go`, `cmd/status.go`,
  `cmd/inspect.go`, `cmd/doctor.go` and match it.
- Add a helper to marshal+print a value as indented JSON to the command's stdout
  writer, e.g. `printJSON(w io.Writer, v any) error` that uses
  `json.MarshalIndent(v, "", "  ")` and writes the bytes followed by a single
  trailing newline.
- Ensure that when `--json` is set the command path can suppress all
  human/log/color output. Identify how the existing commands emit logs/color
  (logger instance, color writer) and provide the mechanism (e.g. an early
  return branch, or a quiet flag the command checks) so S02–S05 only emit JSON.

---

## S02 — `corvex list --json` ✅ PASSED

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
Add `--json` to `list`. When set, print an array of project objects and nothing
else.

Define a stable Go struct with explicit JSON tags. The shape MUST match the spec
exactly:

> [verbatim from spec.md §Requirements item 1]
> `corvex list --json` → array of projects:
> `[{"name":"doctor","hasSpec":true,"hasTasks":true,"status":"planned"}, ...]`

Reuse the existing list discovery logic (the same source the human output uses)
to populate `name`, `hasSpec`, `hasTasks`, `status`. Use `internal/task`
(`ParseTasksFile`) where the human path derives task presence/status.

---

## S03 — `corvex status <project> --json` ✅ PASSED

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
Add `--json` to `status`. When set, print one object with counts and the task
list. Shape MUST match the spec exactly:

> [verbatim from spec.md §Requirements item 2]
> `corvex status <project> --json` → object with the task list and counts:
> `{"project":"doctor","total":3,"passed":3,"failed":0,"pending":0,
>  "tasks":[{"id":"S01","title":"...","status":"PASSED","dependsOn":[...]}, ...]}`

Define structs with JSON tags for the top-level object and per-task entries.
Reuse `internal/task` (`ParseTasksFile`) — the same parse the human output uses —
to compute `total`, `passed`, `failed`, `pending` and each task's `id`, `title`,
`status`, `dependsOn`.

---

## S04 — `corvex inspect <project> --json` ✅ PASSED

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
Add `--json` to `inspect`. Reuse the existing timeline/metrics aggregation —
do **not** recompute it.

> [verbatim from spec.md §Requirements item 3]
> `corvex inspect <project> --json` → the timeline/metrics inspect already
> computes, as JSON (reuse its existing aggregation; just marshal it).

Identify the value `inspect` already builds via `internal/activity`
(`Summarize`) before rendering the human timeline/metrics, and marshal that same
value (or a struct wrapping it) with `MarshalIndent`. Add JSON struct tags to
the relevant types in `internal/activity` if they lack them, keeping the human
output unaffected.

---

## S05 — `corvex doctor --json` (mantém exit code) ✅ PASSED

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
Add `--json` to `doctor`. When set, print the checks object and still exit
non-zero on any failed check. Shape MUST match the spec exactly:

> [verbatim from spec.md §Requirements item 4]
> `corvex doctor --json` → `{"checks":[{"name":"provider","status":"pass",
>  "message":"..."}],"passed":6,"warnings":0,"failed":0}` and still exit
>  non-zero when any check failed.

Define structs with JSON tags: top level `checks` (array), `passed`,
`warnings`, `failed`; per check `name`, `status`, `message`. Reuse the existing
doctor check execution to populate them.

---

## S06 — Table-driven tests para o output `--json` ✅ PASSED

```yaml
type: review
depends_on: [S02, S03, S04, S05]
```

### O que fazer
Add table-driven tests asserting the JSON output contains the expected fields
for a small fixture project, following the existing cmd test pattern.

> [verbatim from spec.md §Requirements]
> Add table-driven tests asserting the JSON contains the expected fields for a
> small fixture project (use a temp .corvex layout like the existing cmd tests).

- Reuse the existing tempdir + `.corvex` setup helper used by `cmd/*_test.go`.
- For each command (`list`, `status`, `inspect`, `doctor`), run with `--json`,
  capture stdout, `json.Unmarshal` into the corresponding struct (or
  `map[string]any`), and assert the documented fields exist with expected
  values for the fixture.
- For `doctor`, also assert the non-zero exit behavior is preserved when a check
  fails.

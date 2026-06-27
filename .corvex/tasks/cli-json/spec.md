# CLI friendliness — machine-readable --json output

Add `--json` output to the read-only inspection commands so corvex is scriptable
and integrable by other tools/CI. Keep `go build ./...` and `go test ./...` green.

## Requirements
Add a `--json` boolean flag to each of these commands; when set, print a single
JSON document to stdout (no log noise, no color) and nothing else:

1. `corvex list --json` → array of projects:
   `[{"name":"doctor","hasSpec":true,"hasTasks":true,"status":"planned"}, ...]`
2. `corvex status <project> --json` → object with the task list and counts:
   `{"project":"doctor","total":3,"passed":3,"failed":0,"pending":0,
     "tasks":[{"id":"S01","title":"...","status":"PASSED","dependsOn":[...]}, ...]}`
3. `corvex inspect <project> --json` → the timeline/metrics inspect already
   computes, as JSON (reuse its existing aggregation; just marshal it).
4. `corvex doctor --json` → `{"checks":[{"name":"provider","status":"pass",
     "message":"..."}],"passed":6,"warnings":0,"failed":0}` and still exit
     non-zero when any check failed.

Rules:
- When `--json` is set, suppress all human/log output for that command; emit only
  the JSON (use encoding/json with MarshalIndent for readability).
- JSON shape must be stable and documented with Go struct tags.
- Human (non-json) output is unchanged.
- Add table-driven tests asserting the JSON contains the expected fields for a
  small fixture project (use a temp .corvex layout like the existing cmd tests).

## Files (reference)
- cmd/list.go, cmd/status.go, cmd/inspect.go, cmd/doctor.go
- internal/task (ParseTasksFile) for status/list
- internal/activity (Summarize) for inspect metrics
- cmd/*_test.go for the test setup pattern (tempdir + .corvex)

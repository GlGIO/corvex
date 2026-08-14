package cmd

// Characterization of the state-reading commands: list and status.
//
// LEI 1 applies to every golden in this file and its siblings: what the CLI
// prints today IS the specification, misalignment and trailing whitespace
// included. Anything that looked wrong while writing these went into the report
// as an observation, never into a fix.
//
// Rewrite the goldens with:
//   go test ./cmd/ -run TestCharacterizeStatuslogs -update-golden
//
// Esse -run serve para iterar e regravar, NUNCA para julgar regressao. Regra de
// execucao completa no header de characterize_test.go ("COMO RODAR ESTA REDE").
//
// Every test here is sequential on purpose (runCLI* chdirs the process).

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ── shared fixture material ──────────────────────────────────────────────────

// statuslogsRichTasksMD is a three-task DAG that exercises the printing paths
// the two-task harness fixture cannot reach:
//   - S02 is FAILED, so the failed counter and ❌ glyph appear;
//   - S03 depends on two tasks, so the " ← [A, B]" suffix has a comma;
//   - S03's title is long and accented, which is what makes the byte-wise
//     truncation in status.go/inspect.go observable;
//   - S01 carries an "### Arquivos" section so `logs` prints its Files block.
const statuslogsRichTasksMD = "---\ngenerated_by: characterize\ndag:\n  S01: []\n  S02: [S01]\n  S03: [S01, S02]\n---\n\n" +
	"## S01 — First Task ✅ PASSED\n\n" +
	"```yaml\ntype: general\n```\n\n" +
	"### O que fazer\nFirst task does the setup.\n\n" +
	"### Critérios de sucesso\n- [ ] Compiles\n- [ ] Tests pass\n\n" +
	"### Arquivos\n- **Criar:** `internal/foo/foo.go`\n- **Modificar:** `cmd/root.go`\n\n---\n\n" +
	"## S02 — Second Task ❌ FAILED\n\n" +
	"```yaml\ntype: backend\ndepends_on: [S01]\n```\n\n" +
	"### O que fazer\nSecond task wires the backend.\n\n" +
	"### Critérios de sucesso\n- [ ] Endpoint responds\n\n---\n\n" +
	"## S03 — Título muito comprido com acentuação para estourar quarenta ⬜ PENDING\n\n" +
	"```yaml\ntype: frontend\ndepends_on: [S01, S02]\n```\n\n" +
	"### O que fazer\nThird task.\n\n" +
	"### Critérios de sucesso\n- [ ] Renders\n"

// statuslogsAllDoneTasksMD is the "nothing left to do" shape: every task
// PASSED, which is the branch that prints "All tasks complete."
const statuslogsAllDoneTasksMD = "---\ngenerated_by: characterize\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
	"## S01 — Alpha ✅ PASSED\n\n" +
	"```yaml\ntype: general\n```\n\n" +
	"### O que fazer\nAlpha.\n\n---\n\n" +
	"## S02 — Beta ✅ PASSED\n\n" +
	"```yaml\ntype: general\ndepends_on: [S01]\n```\n\n" +
	"### O que fazer\nBeta.\n"

// statuslogsAnchorYAML gives status an Intent line and logs a completion block
// (summary + decisions) for S01. Written by hand rather than via anchor.Save so
// the fixture bytes cannot drift when the struct gains fields.
const statuslogsAnchorYAML = `project: alpha
updated_at: "2026-01-02T03:04:05Z"
spec_hash: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
intent: "Caracterizar os leitores de estado"
completed:
  - id: S01
    title: "First Task"
    summary: "Setup concluído com 2 arquivos"
    files_created:
      - internal/foo/foo.go
    files_modified:
      - cmd/root.go
    decisions:
      - "usar tabela única"
      - "sem cache por enquanto"
current_state:
  total_tasks: 3
  completed_tasks: 1
next_task: S02
next_task_context: "Segue para o backend."
`

// statuslogsTree renders a deterministic listing of everything under root: one
// "<mode> <relpath>" line per entry, sorted, directories suffixed with "/".
// Used to lock which files a writing command created without embedding
// timestamps, sizes or absolute paths in the golden.
func statuslogsTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		suffix := ""
		if d.IsDir() {
			suffix = "/"
		}
		lines = append(lines, info.Mode().String()+" "+rel+suffix)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// statuslogsStat and statuslogsReadFile are thin os wrappers so the sibling
// test files can assert on disk state without importing "os" themselves (and
// without shadowing any helper the other characterization agents add).
func statuslogsStat(path string) (os.FileInfo, error) { return os.Stat(path) }

func statuslogsReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}

// ── list ─────────────────────────────────────────────────────────────────────

// Main path: three projects covering all three status strings runList can
// derive (ready / needs planning / no spec).
func TestCharacterizeStatuslogsListHuman(t *testing.T) {
	f := newFixture(t).
		AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD).
		AddProject("beta", fixtureSpecMD, "").
		AddProject("gamma", "", fixtureTasksMD)

	args := []string{"list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_list_human", scrub(transcript(args, stdout, stderr, err)))
}

// Same fixture through the machine-readable branch: locks the JSON field names
// and the key order MarshalIndent produces from listProject.
func TestCharacterizeStatuslogsListJSON(t *testing.T) {
	f := newFixture(t).
		AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD).
		AddProject("beta", fixtureSpecMD, "").
		AddProject("gamma", "", fixtureTasksMD)

	args := []string{"list", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_list_json", scrub(transcript(args, stdout, stderr, err)))
}

// Empty .corvex/tasks/: the human branch prints the "create a spec" hint.
func TestCharacterizeStatuslogsListEmpty(t *testing.T) {
	f := newFixture(t)

	args := []string{"list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_list_empty", scrub(transcript(args, stdout, stderr, err)))
}

// Empty .corvex/tasks/ with --json: an empty JSON array, not the hint.
func TestCharacterizeStatuslogsListEmptyJSON(t *testing.T) {
	f := newFixture(t)

	args := []string{"list", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_list_empty_json", scrub(transcript(args, stdout, stderr, err)))
}

// No .corvex/ at all — requireCorvexDir's long hint, returned as an error (the
// "Error: %s" prefix belongs to cmd.Execute, which the harness does not call).
func TestCharacterizeStatuslogsListNoCorvexDir(t *testing.T) {
	args := []string{"list"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "statuslogs_list_no_corvex", scrub(transcript(args, stdout, stderr, err)))
}

// list takes no positional args: locks cobra's arity error and the usage dump
// that comes with it.
func TestCharacterizeStatuslogsListRejectsArgs(t *testing.T) {
	f := newFixture(t)

	args := []string{"list", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_list_rejects_args", scrub(transcript(args, stdout, stderr, err)))
}

// ── status ───────────────────────────────────────────────────────────────────

// Main path. Locks the header block, the column padding computed from the
// longest ID/title, the "← [S01, S02]" dependency suffix, the >40 truncation of
// S03's accented title, and the pending footer.
func TestCharacterizeStatuslogsStatusHuman(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"status", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_human", scrub(transcript(args, stdout, stderr, err)))
}

// Same project with an anchor.yaml present: the only difference should be the
// extra "Intent:" line. Everything else must stay byte-identical to
// statuslogs_status_human.
func TestCharacterizeStatuslogsStatusWithAnchor(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)
	f.Write(filepath.Join(".corvex", "tasks", "alpha", "anchor.yaml"), statuslogsAnchorYAML)

	args := []string{"status", "alpha"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_with_anchor", scrub(transcript(args, stdout, stderr, err)))
}

// JSON branch: locks the statusOutput/statusTask field names, the counters, and
// the fact that tasks are emitted in DAG-resolved order with dependsOn never
// null (nil is normalised to []).
func TestCharacterizeStatuslogsStatusJSON(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"status", "alpha", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_json", scrub(transcript(args, stdout, stderr, err)))
}

// Everything PASSED: the footer switches to "All tasks complete." and no run
// hint is printed.
func TestCharacterizeStatuslogsStatusAllDone(t *testing.T) {
	f := newFixture(t).AddProject("done", fixtureSpecMD, statuslogsAllDoneTasksMD)

	args := []string{"status", "done"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_all_done", scrub(transcript(args, stdout, stderr, err)))
}

// tasks.md missing: the error is the wrapped os.ReadFile failure, and nothing
// is printed on either stream.
func TestCharacterizeStatuslogsStatusMissingTasks(t *testing.T) {
	f := newFixture(t).AddProject("beta", fixtureSpecMD, "")

	args := []string{"status", "beta"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_missing_tasks", scrub(transcript(args, stdout, stderr, err)))
}

// A project that does not exist at all takes the same path as a missing
// tasks.md — no "did you mean" suggestion is offered by status today, even
// though suggestProject exists in helpers.go.
func TestCharacterizeStatuslogsStatusUnknownProject(t *testing.T) {
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, statuslogsRichTasksMD)

	args := []string{"status", "alfa"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_unknown_project", scrub(transcript(args, stdout, stderr, err)))
}

// Empty tasks.md: ParseTasksFile returns (nil, nil) rather than an error, so
// status prints "0/0 done" and then nothing at all — no task rows, no footer.
func TestCharacterizeStatuslogsStatusEmptyTasksFile(t *testing.T) {
	f := newFixture(t).AddProject("empty", fixtureSpecMD, "\n\n")

	args := []string{"status", "empty"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_empty_tasks", scrub(transcript(args, stdout, stderr, err)))
}

// Malformed heading (status word the parser does not know): locks the
// multi-line parse error, including the reported line number and the hint.
func TestCharacterizeStatuslogsStatusMalformedTasks(t *testing.T) {
	bad := "---\ngenerated_by: characterize\ndag:\n  S01: []\n---\n\n" +
		"## S01 — Broken ✅ ALMOST\n\n" +
		"### O que fazer\nBroken heading.\n"
	f := newFixture(t).AddProject("broken", fixtureSpecMD, bad)

	args := []string{"status", "broken"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_malformed_tasks", scrub(transcript(args, stdout, stderr, err)))
}

// A dependency cycle makes dag.Resolve fail; status swallows the error and
// falls back to file order instead of reporting it. Locking that fallback is
// the point of this case.
func TestCharacterizeStatuslogsStatusCyclicDAG(t *testing.T) {
	cyclic := "---\ngenerated_by: characterize\ndag:\n  S01: [S02]\n  S02: [S01]\n---\n\n" +
		"## S01 — Alpha ⬜ PENDING\n\n" +
		"```yaml\ntype: general\ndepends_on: [S02]\n```\n\n" +
		"### O que fazer\nAlpha.\n\n---\n\n" +
		"## S02 — Beta ⬜ PENDING\n\n" +
		"```yaml\ntype: general\ndepends_on: [S01]\n```\n\n" +
		"### O que fazer\nBeta.\n"
	f := newFixture(t).AddProject("cyclic", fixtureSpecMD, cyclic)

	args := []string{"status", "cyclic"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_cyclic_dag", scrub(transcript(args, stdout, stderr, err)))
}

// status requires exactly one argument.
func TestCharacterizeStatuslogsStatusNoArgs(t *testing.T) {
	f := newFixture(t)

	args := []string{"status"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "statuslogs_status_no_args", scrub(transcript(args, stdout, stderr, err)))
}

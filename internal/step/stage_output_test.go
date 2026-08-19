package step

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/stepout"
	"github.com/giovannialves/corvex/internal/types"
)

// A failed stage's own words reach the operator.
//
// The dogfood that produced this: a recipe's S03 failed because a CLI wanted a
// flag it had not been given. It said so on stderr. What the operator got was
// `! S03 failed · command exit did not pass after 1 iteration(s)` on the
// terminal, the same phrase in `run show --step S03`, and nothing else — the
// cause was found by re-running the command by hand. runShell had captured the
// message with CombinedOutput all along and the failure path dropped it.
//
// The three things the fix has to be true for:
//
//  1. the failed task_complete event carries the command's own output, so the
//     terminal can print it next to the failure line;
//  2. the same bytes are on disk under `.corvex/runs/output/`, keyed by run and
//     step, so `run show --step` answers without the original terminal;
//  3. the event's Message — the ONLY field that reaches activity.jsonl, which
//     corvex's own auto_commit commits — still holds the count and nothing else.
//     A fix that put the output there would have fixed the screen by leaking
//     whatever the command printed into the user's git history.
func TestCommandStage_FailureCarriesTheCommandsOwnOutput(t *testing.T) {
	const said = "ERROR: --repository is required"

	e, r, events := stageFixture(t)
	tk := &types.Task{
		ID: "S03", Title: "Abre o PR", Kind: string(types.KindTool),
		// stderr, not stdout: the message an operator needs is the one a
		// well-behaved tool writes to the error stream, and the pass path's
		// evidence never looks there.
		Command: fmt.Sprintf("echo %q >&2; exit 3", said),
	}

	if err := e.Execute(context.Background(), r, tk); err == nil {
		t.Fatalf("Execute() = nil, want the stage to fail")
	}

	done := lastCompleteOf(t, *events, tk.ID)
	if !strings.Contains(done.Output, said) {
		t.Errorf("the failed task_complete carries Output %q, want it to contain the command's own %q — the terminal has nothing else to print", done.Output, said)
	}
	if strings.Contains(done.Message, said) {
		t.Errorf("Message = %q carries the command's output; that field is the one that lands in the committed activity.jsonl", done.Message)
	}
	if done.Message == "" {
		t.Errorf("Message is empty: the count is what the ledger reports, and it must survive")
	}

	stored := stepout.Read(r.Identity.Repo, r.Identity.RunID, tk.ID)
	if !strings.Contains(stored, said) {
		t.Errorf("stored output for %s = %q, want the command's own %q — `run show --step` reads this, not the ledger", tk.ID, stored, said)
	}
	if stored != done.Output {
		t.Errorf("the stored bytes (%q) and the event's (%q) differ; two truncations would make the two screens disagree", stored, done.Output)
	}
}

// Where the file lives is the whole argument for it existing at all: it must be
// inside the directory whose `*` .gitignore already exists, and it must be
// readable only by its owner, because nothing redacts what a command prints.
func TestCommandStage_StoredOutputIsGitignoredScratchAndNotWorldReadable(t *testing.T) {
	e, r, _ := stageFixture(t)
	tk := &types.Task{ID: "S03", Kind: string(types.KindTool), Command: "echo 'token=abc' >&2; exit 1"}
	if err := e.Execute(context.Background(), r, tk); err == nil {
		t.Fatalf("Execute() = nil, want the stage to fail")
	}

	path, err := stepout.Path(r.Identity.Repo, r.Identity.RunID, tk.ID)
	if err != nil {
		t.Fatalf("stepout.Path: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the failed stage wrote no output file: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("output file mode = %04o, want 0600: this content is arbitrary and may hold a token", mode)
	}
	// The guard is the `*` .gitignore one level up, in .corvex/runs/. Assert the
	// file it is written by, not the effect, so the test says which mechanism is
	// load-bearing.
	ignore := filepath.Join(r.Identity.Repo, ".corvex", "runs", ".gitignore")
	body, err := os.ReadFile(ignore)
	if err != nil {
		t.Fatalf("no .gitignore guarding the scratch directory: %v", err)
	}
	if strings.TrimSpace(string(body)) != "*" {
		t.Errorf("%s = %q, want \"*\": the store's whole safety argument is that it inherits this", ignore, body)
	}
	if !strings.HasPrefix(path, filepath.Join(r.Identity.Repo, ".corvex", "runs")+string(filepath.Separator)) {
		t.Errorf("output path %q is outside the ignored directory", path)
	}
}

// A stage that PASSES prints and stores nothing new. The bug being fixed is a
// missing diagnostic on failure; a green run whose bytes changed would be a
// second bug, and the goldens would be the ones to notice.
func TestCommandStage_PassingStageAddsNoOutputAnywhere(t *testing.T) {
	e, r, events := stageFixture(t)
	tk := &types.Task{ID: "S01", Kind: string(types.KindTool), Command: "echo tudo bem"}
	if err := e.Execute(context.Background(), r, tk); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if done := lastCompleteOf(t, *events, tk.ID); done.Output != "" {
		t.Errorf("a passing stage's task_complete carries Output %q, want empty", done.Output)
	}
	if stored := stepout.Read(r.Identity.Repo, r.Identity.RunID, tk.ID); stored != "" {
		t.Errorf("a passing stage stored %q; the store is a diagnostic, not a log", stored)
	}
}

// A loop stage fails on its until-condition, and the condition's output is what
// names the culprit. Showing the command's output instead would send the
// operator to read a command that did exactly what it was told.
func TestCommandStage_LoopReportsTheConditionsOutputNotTheCommands(t *testing.T) {
	e, r, events := stageFixture(t)
	tk := &types.Task{
		ID: "S04", Kind: string(types.KindTest),
		Command:   "echo o-comando-rodou-bem",
		LoopUntil: "echo 'a condicao nunca fecha' >&2; exit 1",
		LoopMax:   2,
	}
	if err := e.Execute(context.Background(), r, tk); err == nil {
		t.Fatalf("Execute() = nil, want the loop to give up")
	}
	got := lastCompleteOf(t, *events, tk.ID).Output
	if !strings.Contains(got, "a condicao nunca fecha") {
		t.Errorf("Output = %q, want the until-condition's own message", got)
	}
	if strings.Contains(got, "o-comando-rodou-bem") {
		t.Errorf("Output = %q carries the command's output; the command is not what failed", got)
	}
}

// stageFixture is an Executor over a real git repository with a registered run
// identity, plus the slice every emitted event lands in.
func stageFixture(t *testing.T) (*Executor, *Run, *[]event.Event) {
	t.Helper()
	repo := gitRepoWithOneCommit(t)
	var seen []event.Event
	e := &Executor{
		workDir: repo,
		cfg:     &config.Config{},
		book:    &Bookkeeper{},
		emit:    func(ev event.Event) { seen = append(seen, ev) },
	}
	total := 0.0
	r := &Run{
		TasksPath:    filepath.Join(repo, "tasks.md"),
		AnchorPath:   filepath.Join(repo, "anchor.yaml"),
		Anchor:       &types.AnchorState{},
		Completed:    map[string]bool{},
		DAG:          dag.NewDAG([]types.Task{{ID: "S01"}}),
		TotalCostUSD: &total,
		Identity:     RunIdentity{RunID: "run_d1a6", Repo: repo, Project: "alpha", Recipe: "ship"},
	}
	return e, r, &seen
}

func lastCompleteOf(t *testing.T, events []event.Event, taskID string) event.Event {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == event.TaskComplete && events[i].TaskID == taskID {
			return events[i]
		}
	}
	t.Fatalf("no task_complete emitted for %s", taskID)
	return event.Event{}
}

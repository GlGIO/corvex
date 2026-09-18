package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

// A run has ONE working tree, so two steps that produce their result by editing
// it cannot overlap — whatever the wave says. These two tests pin both halves of
// that rule, because either half alone is a bug: serialising everything throws
// away the parallelism a recipe is written for, and serialising nothing lets two
// workers write over each other with both tasks reporting PASSED.

// overlapWatch records the highest number of callers inside it at once.
type overlapWatch struct {
	mu      sync.Mutex
	now     int
	highest int
}

func (o *overlapWatch) enter() {
	o.mu.Lock()
	o.now++
	if o.now > o.highest {
		o.highest = o.now
	}
	o.mu.Unlock()
}

func (o *overlapWatch) leave() {
	o.mu.Lock()
	o.now--
	o.mu.Unlock()
}

func (o *overlapWatch) peak() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.highest
}

// Three worker steps in one wave run one at a time.
//
// The defect this pins was silent by construction: both tasks pass, the ledger
// records two successes, and the only evidence is a diff missing the edits of
// whichever worker lost the race to the checkpoint commit.
func TestParallelWave_WorkerStepsNeverShareTheTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "tree-lock"
	setupProject(t, dir, project, fanOutTasksMD)
	gitCommitAll(t, dir, "add fan-out tasks")

	events := make(chan Event, 500)
	var warned bool
	var warnMu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			if ev.Type == EventTaskWarn && strings.Contains(ev.Message, "write the working tree") {
				warnMu.Lock()
				warned = true
				warnMu.Unlock()
			}
		}
	}()

	watch := &overlapWatch{}
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Good.\nVERDICT: PASS"}, nil
			}
			// Only the worker leg is watched: the reviewer runs inside the same
			// task, so counting it would measure the task's own two phases
			// rather than two tasks overlapping.
			watch.enter()
			time.Sleep(40 * time.Millisecond)
			watch.leave()
			return &types.ExecuteResult{Output: "done" + taskReportBlock, CostUSD: 0.01}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true
	cfg.Execution.Parallel = true
	cfg.Execution.MaxParallel = 3

	if err := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events}).Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)
	<-done

	if peak := watch.peak(); peak != 1 {
		t.Errorf("%d workers were editing the checkout at once, want 1", peak)
	}
	warnMu.Lock()
	defer warnMu.Unlock()
	if !warned {
		t.Error("the wave serialised its writers and never said so: an operator reading the timings has nothing to explain them")
	}
}

// The other half: steps with a fixed command do not touch the checkout to
// produce their result, and they keep overlapping. Without this, the fix above
// would be indistinguishable from turning parallelism off.
func TestParallelWave_CommandStepsStillOverlap(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "tree-lock-commands"

	marker := filepath.Join(t.TempDir(), "concurrent")
	// Each step announces itself, waits, and announces again; a run in which the
	// three never overlapped would produce three separate open/close pairs.
	step := func(id, deps string) string {
		cmd := fmt.Sprintf("sh -c 'echo open >> %s; sleep 0.4; echo close >> %s'", marker, marker)
		return fmt.Sprintf("## %s — probe ⬜ PENDING\n\n```yaml\ntype: general\nkind: test\ncommand: \"%s\"\n%s```\n\n### O que fazer\nprobe\n\n---\n\n",
			id, cmd, deps)
	}
	tasks := "---\ngenerated_by: test\ndag:\n  S01: []\n  S02: []\n  S03: []\n---\n\n" +
		step("S01", "") + step("S02", "") + strings.TrimSuffix(step("S03", ""), "---\n\n")
	setupProject(t, dir, project, tasks)
	gitCommitAll(t, dir, "add command tasks")

	events := make(chan Event, 500)
	go func() {
		for range events {
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false
	cfg.Execution.Parallel = true
	cfg.Execution.MaxParallel = 3

	start := time.Now()
	if err := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events}).Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	// Three 0.4s steps: overlapping they finish in well under a second, serial
	// they cannot. The wall clock is the assertion because it is the property
	// the user is paying for.
	if elapsed := time.Since(start); elapsed > 1100*time.Millisecond {
		t.Errorf("three command steps took %s — they were serialised, and nothing about a fixed command needs the tree", elapsed)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the probes never ran: %v", err)
	}
	if opens := strings.Count(string(data), "open"); opens != 3 {
		t.Errorf("marker has %d opens, want 3: %q", opens, data)
	}
}

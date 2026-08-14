package ops

import (
	"fmt"
	"os"
	"time"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// RecordSpecHash stores the current hash of spec.md in anchor.yaml, preserving
// whatever else the anchor already holds.
//
// Without this record the drift guard behind `corvex run` finds no anchor
// state, treats the spec as changed and replans on every invocation — which
// wipes manual edits to tasks.md and resets completed tasks to PENDING.
func RecordSpecHash(project, specPath, anchorPath string) error {
	existing, _ := anchor.Load(anchorPath)

	hash, err := anchor.SpecHash(specPath)
	if err != nil {
		return fmt.Errorf("hashing spec: %w", err)
	}

	existing.Project = project
	existing.SpecHash = hash
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if existing.Completed == nil {
		existing.Completed = []types.CompletedTask{}
	}

	if err := anchor.Save(anchorPath, existing); err != nil {
		return fmt.Errorf("saving anchor: %w", err)
	}
	return nil
}

// ReanchorProblem says why a project cannot have its spec hash re-recorded
// without replanning.
type ReanchorProblem int

const (
	// ReanchorOK means spec.md and a parseable tasks.md are both present.
	ReanchorOK ReanchorProblem = iota
	// ReanchorNoSpec means there is no spec.md to hash.
	ReanchorNoSpec
	// ReanchorNoTasks means there is no tasks.md — nothing has been planned yet.
	ReanchorNoTasks
	// ReanchorBrokenTasks means tasks.md exists but does not parse; anchoring it
	// would bless a corrupted plan.
	ReanchorBrokenTasks
)

// ReanchorStatus reports whether a project is safe to re-anchor. The caller
// turns it into a message; ops does not phrase hints.
type ReanchorStatus struct {
	Problem ReanchorProblem
	// SpecPath and TasksPath are the paths that were inspected.
	SpecPath  string
	TasksPath string
	// Err is the parse error behind ReanchorBrokenTasks, nil otherwise.
	Err error
}

// InspectReanchor checks the preconditions for re-recording a spec hash against
// an existing plan: the spec must exist, and the plan must exist and parse.
func InspectReanchor(specPath, tasksPath string) ReanchorStatus {
	st := ReanchorStatus{SpecPath: specPath, TasksPath: tasksPath}

	if _, err := os.Stat(specPath); err != nil {
		st.Problem = ReanchorNoSpec
		return st
	}
	if _, err := os.Stat(tasksPath); err != nil {
		st.Problem = ReanchorNoTasks
		return st
	}
	if _, _, err := task.ParseTasksFile(tasksPath); err != nil {
		st.Problem = ReanchorBrokenTasks
		st.Err = err
		return st
	}
	return st
}

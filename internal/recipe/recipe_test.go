package recipe

import (
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

const sampleRecipe = `
name: build-feature
description: scaffold then implement then review
stages:
  - id: S01
    title: Scaffold
    type: backend
    description: create the skeleton
    criteria:
      - builds
  - id: S02
    title: Implement
    type: backend
    depends_on: [S01]
  - id: S03
    title: Review
    type: review
    depends_on: [S02]
`

func TestParseAndCompile(t *testing.T) {
	r, err := Parse([]byte(sampleRecipe))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	tasks, dag, err := r.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("tasks = %d, want 3", len(tasks))
	}
	if tasks[0].ID != "S01" || tasks[0].Type != types.TypeBackend {
		t.Errorf("S01 = %+v", tasks[0])
	}
	if tasks[0].Status != types.StatusPending {
		t.Errorf("S01 status = %s, want PENDING", tasks[0].Status)
	}
	if len(tasks[0].Criteria) != 1 || tasks[0].Criteria[0] != "builds" {
		t.Errorf("S01 criteria = %v", tasks[0].Criteria)
	}
	if got := dag.Dependencies["S02"]; len(got) != 1 || got[0] != "S01" {
		t.Errorf("S02 deps = %v, want [S01]", got)
	}
	if dag.GeneratedBy != "corvex-recipe:build-feature" {
		t.Errorf("GeneratedBy = %q", dag.GeneratedBy)
	}
}

func TestCompile_CommandStage(t *testing.T) {
	y := "name: pipe\nstages:\n  - id: S01\n    title: Test\n    kind: command\n    command: go test ./...\n"
	r, err := Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	tasks, _, err := r.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if tasks[0].Kind != "command" {
		t.Errorf("Kind = %q, want command", tasks[0].Kind)
	}
	if tasks[0].Command != "go test ./..." {
		t.Errorf("Command = %q", tasks[0].Command)
	}
}

func TestCompile_LoopPolicy(t *testing.T) {
	y := "name: pipe\nstages:\n  - id: S01\n    kind: command\n    command: flaky\n    loop:\n      until: test -f done\n      max: 5\n"
	r, err := Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	tasks, _, err := r.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if tasks[0].LoopUntil != "test -f done" || tasks[0].LoopMax != 5 {
		t.Errorf("loop = until=%q max=%d, want until='test -f done' max=5", tasks[0].LoopUntil, tasks[0].LoopMax)
	}

	// Default max when omitted.
	r2, _ := Parse([]byte("name: p\nstages:\n  - id: S01\n    kind: command\n    command: x\n    loop: {}\n"))
	tasks2, _, err := r2.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if tasks2[0].LoopMax != 3 {
		t.Errorf("default loop max = %d, want 3", tasks2[0].LoopMax)
	}
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"no name", "stages:\n  - id: S01\n", "name is required"},
		{"no stages", "name: x\n", "no stages"},
		{"dup id", "name: x\nstages:\n  - id: S01\n  - id: S01\n", "duplicate stage id"},
		{"unknown dep", "name: x\nstages:\n  - id: S01\n    depends_on: [S99]\n", "unknown stage"},
		{"unknown kind", "name: x\nstages:\n  - id: S01\n    kind: magic\n", "unknown kind"},
		{"cycle", "name: x\nstages:\n  - id: S01\n    depends_on: [S02]\n  - id: S02\n    depends_on: [S01]\n", "cycle"},
		{"command without command", "name: x\nstages:\n  - id: S01\n    kind: command\n", "must set a non-empty"},
		{"command on non-command", "name: x\nstages:\n  - id: S01\n    command: echo hi\n", "not a command stage"},
		{"loop on non-command", "name: x\nstages:\n  - id: S01\n    loop:\n      max: 2\n", "only command stages support loops"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			err = r.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate() error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

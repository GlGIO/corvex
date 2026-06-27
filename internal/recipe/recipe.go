// Package recipe compiles declarative, reusable workflow recipes into a fixed
// task DAG. A recipe describes an opinionated pipeline (its stages and their
// dependencies) up front, so a run skips the AI Planner entirely and executes
// a deterministic graph — the foundation for Corvex's "workflow recipes"
// (CH-12). Advanced stage kinds (command, human-gate) and custom roles build
// on the Stage type defined here.
package recipe

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/giovannialves/corvex/internal/types"
)

// Recipe is a declarative pipeline definition, typically loaded from a YAML
// file under .corvex/recipes/<name>.yaml.
type Recipe struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Stages      []Stage `yaml:"stages"`
}

// Stage is one node of the recipe pipeline. Kind defaults to "task" (an AI
// worker task). Future kinds ("command", "human-gate") are reserved and
// validated here so an unknown kind fails fast rather than silently running as
// a task.
type Stage struct {
	ID          string   `yaml:"id"`
	Title       string   `yaml:"title"`
	Kind        string   `yaml:"kind"`    // "task" (default), "command"; reserved: "human-gate"
	Type        string   `yaml:"type"`    // task type for routing (backend, frontend, ...)
	DependsOn   []string `yaml:"depends_on"`
	Description string   `yaml:"description"`
	Criteria    []string `yaml:"criteria"`
	Command     string   `yaml:"command"` // shell command run when Kind == "command"
	Loop        *Loop    `yaml:"loop"`    // optional loop-with-policy (command stages)
}

// Loop is a command stage's loop-with-policy: re-run the command until Until
// (a shell condition) exits 0, or until Max iterations.
type Loop struct {
	Until string `yaml:"until"` // optional; empty → the command's own exit governs
	Max   int    `yaml:"max"`   // max iterations (default 3)
}

// knownKinds enumerates the stage kinds the compiler accepts today. Only "task"
// is executable so far; the others are reserved so recipes can declare them
// without the compiler silently mis-running them as tasks.
var knownKinds = map[string]bool{
	"":           true, // defaults to task
	"task":       true,
	"command":    true,
	"human-gate": true,
}

// Parse decodes a recipe from YAML bytes.
func Parse(data []byte) (*Recipe, error) {
	var r Recipe
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing recipe: %w", err)
	}
	return &r, nil
}

// Validate checks the recipe is internally consistent: it has stages, every ID
// is unique and non-empty, every dependency references a declared stage, kinds
// are known, and the dependency graph is acyclic.
func (r *Recipe) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("recipe: name is required")
	}
	if len(r.Stages) == 0 {
		return fmt.Errorf("recipe %q: has no stages", r.Name)
	}

	ids := make(map[string]bool, len(r.Stages))
	for _, s := range r.Stages {
		if strings.TrimSpace(s.ID) == "" {
			return fmt.Errorf("recipe %q: a stage is missing an id", r.Name)
		}
		if ids[s.ID] {
			return fmt.Errorf("recipe %q: duplicate stage id %q", r.Name, s.ID)
		}
		if !knownKinds[s.Kind] {
			return fmt.Errorf("recipe %q: stage %q has unknown kind %q (known: task, command, human-gate)", r.Name, s.ID, s.Kind)
		}
		if s.Kind == "command" && strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("recipe %q: command stage %q must set a non-empty `command`", r.Name, s.ID)
		}
		if s.Kind != "command" && strings.TrimSpace(s.Command) != "" {
			return fmt.Errorf("recipe %q: stage %q sets `command` but is not a command stage (kind: %q)", r.Name, s.ID, s.Kind)
		}
		if s.Loop != nil {
			if s.Kind != "command" {
				return fmt.Errorf("recipe %q: stage %q has a `loop` but only command stages support loops (kind: %q)", r.Name, s.ID, s.Kind)
			}
			if s.Loop.Max < 0 {
				return fmt.Errorf("recipe %q: stage %q loop.max must be >= 0", r.Name, s.ID)
			}
		}
		ids[s.ID] = true
	}

	for _, s := range r.Stages {
		for _, dep := range s.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("recipe %q: stage %q depends on unknown stage %q", r.Name, s.ID, dep)
			}
			if dep == s.ID {
				return fmt.Errorf("recipe %q: stage %q depends on itself", r.Name, s.ID)
			}
		}
	}

	return r.checkAcyclic()
}

func (r *Recipe) checkAcyclic() error {
	deps := make(map[string][]string, len(r.Stages))
	inDeg := make(map[string]int, len(r.Stages))
	for _, s := range r.Stages {
		deps[s.ID] = s.DependsOn
		inDeg[s.ID] = len(s.DependsOn)
	}
	queue := make([]string, 0)
	for id, d := range inDeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	// Build reverse edges (dependency → dependents).
	dependents := make(map[string][]string)
	for id, ds := range deps {
		for _, dep := range ds {
			dependents[dep] = append(dependents[dep], id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, dep := range dependents[cur] {
			inDeg[dep]--
			if inDeg[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}
	if visited != len(r.Stages) {
		return fmt.Errorf("recipe %q: dependency cycle detected", r.Name)
	}
	return nil
}

// Compile turns a validated recipe into the task list and DAG spec that the
// orchestrator consumes — the same shapes the AI Planner would produce, but
// deterministic. All tasks start PENDING.
func (r *Recipe) Compile() ([]types.Task, types.DAGSpec, error) {
	if err := r.Validate(); err != nil {
		return nil, types.DAGSpec{}, err
	}

	tasks := make([]types.Task, 0, len(r.Stages))
	dag := types.DAGSpec{
		GeneratedBy:  "corvex-recipe:" + r.Name,
		Dependencies: make(map[string][]string, len(r.Stages)),
	}

	for _, s := range r.Stages {
		typ := types.TaskType(s.Type)
		if typ == "" {
			typ = types.TypeGeneral
		}
		deps := s.DependsOn
		if deps == nil {
			deps = []string{}
		}
		t := types.Task{
			ID:          s.ID,
			Title:       s.Title,
			Status:      types.StatusPending,
			Type:        typ,
			DependsOn:   deps,
			Description: s.Description,
			Criteria:    s.Criteria,
			Kind:        s.Kind,
			Command:     s.Command,
		}
		if s.Loop != nil {
			t.LoopUntil = s.Loop.Until
			t.LoopMax = s.Loop.Max
			if t.LoopMax == 0 {
				t.LoopMax = 3 // default loop cap
			}
		}
		tasks = append(tasks, t)
		dag.Dependencies[s.ID] = deps
	}

	return tasks, dag, nil
}

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
	Kind        string   `yaml:"kind"`        // "task" (default); reserved: "command", "human-gate"
	Type        string   `yaml:"type"`        // task type for routing (backend, frontend, ...)
	DependsOn   []string `yaml:"depends_on"`
	Description string   `yaml:"description"`
	Criteria    []string `yaml:"criteria"`
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
		tasks = append(tasks, types.Task{
			ID:          s.ID,
			Title:       s.Title,
			Status:      types.StatusPending,
			Type:        typ,
			DependsOn:   deps,
			Description: s.Description,
			Criteria:    s.Criteria,
		})
		dag.Dependencies[s.ID] = deps
	}

	return tasks, dag, nil
}

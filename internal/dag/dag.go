// Package dag implements a directed acyclic graph for task dependency resolution.
package dag

import (
	"fmt"
	"sort"

	"github.com/giovannialves/corvex/internal/types"
)

// CycleError indicates that the graph contains a cycle.
type CycleError struct {
	Nodes []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("cycle detected involving nodes: %v", e.Nodes)
}

// MissingDepError indicates that a task depends on a non-existent task.
type MissingDepError struct {
	TaskID string
	DepID  string
}

func (e *MissingDepError) Error() string {
	return fmt.Sprintf("task %q depends on unknown task %q", e.TaskID, e.DepID)
}

type node struct {
	id         string
	edges      []string // predecessors (dependencies)
	dependents []string // successors (who depends on this node)
}

// DAG represents a directed acyclic graph of tasks.
type DAG struct {
	nodes map[string]*node
	order []string
}

// NewDAG builds a DAG from the given tasks. It does not validate the graph;
// call Validate explicitly before use.
func NewDAG(tasks []types.Task) *DAG {
	d := &DAG{nodes: make(map[string]*node, len(tasks))}

	for _, t := range tasks {
		d.nodes[t.ID] = &node{
			id:    t.ID,
			edges: append([]string(nil), t.DependsOn...),
		}
	}

	for _, n := range d.nodes {
		for _, dep := range n.edges {
			if target, ok := d.nodes[dep]; ok {
				target.dependents = append(target.dependents, n.id)
			}
		}
	}

	return d
}

// Validate checks the graph for missing dependencies and cycles.
func (d *DAG) Validate() error {
	for _, n := range d.nodes {
		for _, dep := range n.edges {
			if _, ok := d.nodes[dep]; !ok {
				return &MissingDepError{TaskID: n.id, DepID: dep}
			}
		}
	}

	inDeg := d.computeInDegrees()
	queue := make([]string, 0)
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, dep := range d.nodes[cur].dependents {
			inDeg[dep]--
			if inDeg[dep] == 0 {
				queue = insertSorted(queue, dep)
			}
		}
	}

	if visited != len(d.nodes) {
		var cycleNodes []string
		for id, deg := range inDeg {
			if deg > 0 {
				cycleNodes = append(cycleNodes, id)
			}
		}
		sort.Strings(cycleNodes)
		return &CycleError{Nodes: cycleNodes}
	}

	return nil
}

// Resolve returns a topological ordering of all task IDs. Results are cached
// after the first successful call.
func (d *DAG) Resolve() ([]string, error) {
	if d.order != nil {
		return d.order, nil
	}

	inDeg := d.computeInDegrees()
	queue := make([]string, 0)
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	result := make([]string, 0, len(d.nodes))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		result = append(result, cur)
		for _, dep := range d.nodes[cur].dependents {
			inDeg[dep]--
			if inDeg[dep] == 0 {
				queue = insertSorted(queue, dep)
			}
		}
	}

	if len(result) != len(d.nodes) {
		var cycleNodes []string
		for id, deg := range inDeg {
			if deg > 0 {
				cycleNodes = append(cycleNodes, id)
			}
		}
		sort.Strings(cycleNodes)
		return nil, &CycleError{Nodes: cycleNodes}
	}

	d.order = result
	return d.order, nil
}

// NextReady returns the IDs of tasks whose dependencies are all satisfied
// and that are not yet completed themselves.
func (d *DAG) NextReady(completed map[string]bool) []string {
	var ready []string
	for id, n := range d.nodes {
		if completed[id] {
			continue
		}
		allMet := true
		for _, dep := range n.edges {
			if !completed[dep] {
				allMet = false
				break
			}
		}
		if allMet {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	return ready
}

// Levels groups tasks into BFS layers suitable for parallel execution.
// Each level contains tasks that can run concurrently.
func (d *DAG) Levels() ([][]string, error) {
	inDeg := d.computeInDegrees()
	queue := make([]string, 0)
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	var levels [][]string
	visited := 0

	for len(queue) > 0 {
		level := queue
		queue = nil
		sort.Strings(level)
		levels = append(levels, level)
		visited += len(level)

		for _, cur := range level {
			for _, dep := range d.nodes[cur].dependents {
				inDeg[dep]--
				if inDeg[dep] == 0 {
					queue = insertSorted(queue, dep)
				}
			}
		}
	}

	if visited != len(d.nodes) {
		var cycleNodes []string
		for id, deg := range inDeg {
			if deg > 0 {
				cycleNodes = append(cycleNodes, id)
			}
		}
		sort.Strings(cycleNodes)
		return nil, &CycleError{Nodes: cycleNodes}
	}

	return levels, nil
}

// Size returns the number of nodes in the graph.
func (d *DAG) Size() int {
	return len(d.nodes)
}

func (d *DAG) computeInDegrees() map[string]int {
	inDeg := make(map[string]int, len(d.nodes))
	for id, n := range d.nodes {
		inDeg[id] = len(n.edges)
	}
	return inDeg
}

func insertSorted(s []string, v string) []string {
	i := sort.SearchStrings(s, v)
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

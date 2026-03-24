package dag

import (
	"errors"
	"reflect"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func task(id string, deps ...string) types.Task {
	return types.Task{ID: id, DependsOn: deps}
}

func TestDAG_Empty(t *testing.T) {
	t.Parallel()
	d := NewDAG(nil)

	if d.Size() != 0 {
		t.Errorf("Size() = %d, want 0", d.Size())
	}

	if err := d.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}

	order, err := d.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(order) != 0 {
		t.Errorf("Resolve() = %v, want []", order)
	}

	levels, err := d.Levels()
	if err != nil {
		t.Fatalf("Levels() error = %v", err)
	}
	if len(levels) != 0 {
		t.Errorf("Levels() = %v, want []", levels)
	}

	ready := d.NextReady(map[string]bool{})
	if len(ready) != 0 {
		t.Errorf("NextReady({}) = %v, want []", ready)
	}
}

func TestDAG_SingleTask(t *testing.T) {
	t.Parallel()
	d := NewDAG([]types.Task{task("A")})

	if d.Size() != 1 {
		t.Errorf("Size() = %d, want 1", d.Size())
	}

	if err := d.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}

	order, err := d.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if want := []string{"A"}; !reflect.DeepEqual(order, want) {
		t.Errorf("Resolve() = %v, want %v", order, want)
	}

	levels, err := d.Levels()
	if err != nil {
		t.Fatalf("Levels() error = %v", err)
	}
	if want := [][]string{{"A"}}; !reflect.DeepEqual(levels, want) {
		t.Errorf("Levels() = %v, want %v", levels, want)
	}

	ready := d.NextReady(map[string]bool{})
	if want := []string{"A"}; !reflect.DeepEqual(ready, want) {
		t.Errorf("NextReady({}) = %v, want %v", ready, want)
	}

	ready = d.NextReady(map[string]bool{"A": true})
	if len(ready) != 0 {
		t.Errorf("NextReady({A}) = %v, want []", ready)
	}
}

func TestDAG_Resolve(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		tasks []types.Task
		want  []string
	}{
		{
			name:  "linear chain A→B→C",
			tasks: []types.Task{task("C", "B"), task("B", "A"), task("A")},
			want:  []string{"A", "B", "C"},
		},
		{
			name:  "diamond A→{B,C}→D",
			tasks: []types.Task{task("D", "B", "C"), task("B", "A"), task("C", "A"), task("A")},
			want:  []string{"A", "B", "C", "D"},
		},
		{
			name:  "parallel independent",
			tasks: []types.Task{task("C"), task("A"), task("B")},
			want:  []string{"A", "B", "C"},
		},
		{
			name:  "complex A→{B,C}→D→E",
			tasks: []types.Task{task("E", "D"), task("D", "B", "C"), task("C", "A"), task("B", "A"), task("A")},
			want:  []string{"A", "B", "C", "D", "E"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := NewDAG(tt.tasks)
			got, err := d.Resolve()
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Resolve() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDAG_Levels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		tasks []types.Task
		want  [][]string
	}{
		{
			name:  "linear chain",
			tasks: []types.Task{task("C", "B"), task("B", "A"), task("A")},
			want:  [][]string{{"A"}, {"B"}, {"C"}},
		},
		{
			name:  "diamond",
			tasks: []types.Task{task("D", "B", "C"), task("B", "A"), task("C", "A"), task("A")},
			want:  [][]string{{"A"}, {"B", "C"}, {"D"}},
		},
		{
			name:  "parallel",
			tasks: []types.Task{task("C"), task("A"), task("B")},
			want:  [][]string{{"A", "B", "C"}},
		},
		{
			name:  "complex five-node",
			tasks: []types.Task{task("E", "D"), task("D", "B", "C"), task("C", "A"), task("B", "A"), task("A")},
			want:  [][]string{{"A"}, {"B", "C"}, {"D"}, {"E"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := NewDAG(tt.tasks)
			got, err := d.Levels()
			if err != nil {
				t.Fatalf("Levels() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Levels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDAG_Validate_CycleError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		tasks     []types.Task
		wantNodes []string
	}{
		{
			name:      "simple mutual cycle A↔B",
			tasks:     []types.Task{task("A", "B"), task("B", "A")},
			wantNodes: []string{"A", "B"},
		},
		{
			name:      "cycle in subset with clean node",
			tasks:     []types.Task{task("A"), task("C", "D"), task("D", "C")},
			wantNodes: []string{"C", "D"},
		},
		{
			name:      "three-node cycle",
			tasks:     []types.Task{task("A", "C"), task("B", "A"), task("C", "B")},
			wantNodes: []string{"A", "B", "C"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := NewDAG(tt.tasks)

			err := d.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want CycleError")
			}
			var ce *CycleError
			if !errors.As(err, &ce) {
				t.Fatalf("Validate() error type = %T, want *CycleError", err)
			}
			if !reflect.DeepEqual(ce.Nodes, tt.wantNodes) {
				t.Errorf("CycleError.Nodes = %v, want %v", ce.Nodes, tt.wantNodes)
			}
		})
	}
}

func TestDAG_Validate_MissingDepError(t *testing.T) {
	t.Parallel()
	d := NewDAG([]types.Task{task("A"), task("B", "X")})

	err := d.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want MissingDepError")
	}
	var me *MissingDepError
	if !errors.As(err, &me) {
		t.Fatalf("Validate() error type = %T, want *MissingDepError", err)
	}
	if me.TaskID != "B" || me.DepID != "X" {
		t.Errorf("MissingDepError = {TaskID:%q, DepID:%q}, want {TaskID:\"B\", DepID:\"X\"}", me.TaskID, me.DepID)
	}
}

func TestDAG_Resolve_CycleError(t *testing.T) {
	t.Parallel()
	d := NewDAG([]types.Task{task("A", "B"), task("B", "A")})

	order, err := d.Resolve()
	if err == nil {
		t.Fatalf("Resolve() = %v, want CycleError", order)
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("Resolve() error type = %T, want *CycleError", err)
	}
	if !reflect.DeepEqual(ce.Nodes, []string{"A", "B"}) {
		t.Errorf("CycleError.Nodes = %v, want [A B]", ce.Nodes)
	}
}

func TestDAG_Levels_CycleError(t *testing.T) {
	t.Parallel()
	d := NewDAG([]types.Task{task("A", "B"), task("B", "A")})

	levels, err := d.Levels()
	if err == nil {
		t.Fatalf("Levels() = %v, want CycleError", levels)
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("Levels() error type = %T, want *CycleError", err)
	}
}

func TestDAG_NextReady_Progression(t *testing.T) {
	t.Parallel()
	d := NewDAG([]types.Task{
		task("A"),
		task("B", "A"),
		task("C", "A"),
		task("D", "B", "C"),
	})

	completed := map[string]bool{}

	ready := d.NextReady(completed)
	if want := []string{"A"}; !reflect.DeepEqual(ready, want) {
		t.Errorf("step 0: NextReady = %v, want %v", ready, want)
	}

	completed["A"] = true
	ready = d.NextReady(completed)
	if want := []string{"B", "C"}; !reflect.DeepEqual(ready, want) {
		t.Errorf("step 1: NextReady = %v, want %v", ready, want)
	}

	completed["B"] = true
	ready = d.NextReady(completed)
	if want := []string{"C"}; !reflect.DeepEqual(ready, want) {
		t.Errorf("step 2: NextReady = %v, want %v", ready, want)
	}

	completed["C"] = true
	ready = d.NextReady(completed)
	if want := []string{"D"}; !reflect.DeepEqual(ready, want) {
		t.Errorf("step 3: NextReady = %v, want %v", ready, want)
	}

	completed["D"] = true
	ready = d.NextReady(completed)
	if len(ready) != 0 {
		t.Errorf("step 4: NextReady = %v, want []", ready)
	}
}

func TestDAG_Size(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		tasks []types.Task
		want  int
	}{
		{"zero", nil, 0},
		{"one", []types.Task{task("A")}, 1},
		{"three", []types.Task{task("A"), task("B"), task("C")}, 3},
		{"five", []types.Task{task("A"), task("B", "A"), task("C", "A"), task("D", "B"), task("E", "D")}, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := NewDAG(tt.tasks)
			if got := d.Size(); got != tt.want {
				t.Errorf("Size() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDAG_Resolve_Caching(t *testing.T) {
	t.Parallel()
	d := NewDAG([]types.Task{task("A"), task("B", "A"), task("C", "B")})

	first, err := d.Resolve()
	if err != nil {
		t.Fatalf("Resolve() first call error = %v", err)
	}

	second, err := d.Resolve()
	if err != nil {
		t.Fatalf("Resolve() second call error = %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Errorf("Resolve() caching mismatch: first=%v, second=%v", first, second)
	}

	// Verify same underlying slice (cached, not recomputed)
	if &first[0] != &second[0] {
		t.Error("Resolve() returned different slice on second call; expected cached result")
	}
}

func TestDAG_ErrorMessages(t *testing.T) {
	t.Parallel()

	t.Run("CycleError message", func(t *testing.T) {
		t.Parallel()
		e := &CycleError{Nodes: []string{"X", "Y"}}
		got := e.Error()
		want := "cycle detected involving nodes: [X Y]"
		if got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})

	t.Run("MissingDepError message", func(t *testing.T) {
		t.Parallel()
		e := &MissingDepError{TaskID: "B", DepID: "X"}
		got := e.Error()
		want := `task "B" depends on unknown task "X"`
		if got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})
}

func TestDAG_NextReady_NilCompleted(t *testing.T) {
	t.Parallel()
	d := NewDAG([]types.Task{task("A"), task("B", "A")})

	ready := d.NextReady(nil)
	if want := []string{"A"}; !reflect.DeepEqual(ready, want) {
		t.Errorf("NextReady(nil) = %v, want %v", ready, want)
	}
}

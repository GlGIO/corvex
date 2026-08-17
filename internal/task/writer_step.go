package task

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/giovannialves/corvex/internal/types"
)

// stepFields is the F2 half of a task's inline YAML block: gates, evidence,
// fan-out and the two scalars that bind a step to another one.
//
// It is marshalled as a unit and appended *after* the hand-written lines rather
// than replacing them. That is deliberate: the pre-F2 lines are byte-compared by
// the F-1 golden net, and re-emitting them through yaml.Marshal would reorder
// keys and requote strings for no gain. A task that declares none of these
// fields appends nothing, so its block is byte-identical to before.
type stepFields struct {
	Gates      []types.Gate     `yaml:"gates,omitempty"`
	Evidence   []types.Evidence `yaml:"evidence,omitempty"`
	Fanout     *types.Fanout    `yaml:"fanout,omitempty"`
	Produces   string           `yaml:"produces,omitempty"`
	FixedBy    string           `yaml:"fixed_by,omitempty"`
	ExpectFail bool             `yaml:"expect_fail,omitempty"`
	Item       string           `yaml:"item,omitempty"`
	Items      []string         `yaml:"items,omitempty"`
	Expanded   bool             `yaml:"expanded,omitempty"`
	FanoutOf   string           `yaml:"fanout_of,omitempty"`
}

func collectStepFields(t types.Task) stepFields {
	return stepFields{
		Gates:      t.Gates,
		Evidence:   t.Evidence,
		Fanout:     t.Fanout,
		Produces:   t.Produces,
		FixedBy:    t.FixedBy,
		ExpectFail: t.ExpectFail,
		Item:       t.Item,
		Items:      t.Items,
		Expanded:   t.Expanded,
		FanoutOf:   t.FanoutOf,
	}
}

// hasStepFields reports whether the task carries any F2 field, so writeTask
// knows to open the inline YAML block for a task that has nothing else in it.
func hasStepFields(t types.Task) bool {
	f := collectStepFields(t)
	return len(f.Gates) > 0 || len(f.Evidence) > 0 || f.Fanout != nil ||
		f.Produces != "" || f.FixedBy != "" || f.Item != "" || f.ExpectFail ||
		len(f.Items) > 0 || f.Expanded || f.FanoutOf != ""
}

// writeStepFields appends the marshalled F2 fields to an already-open block.
// A marshalling failure writes nothing: the alternative is corrupting a
// tasks.md that is otherwise fine, and these are plain data structs with no
// channels or funcs, so the error is unreachable in practice.
func writeStepFields(b *strings.Builder, t types.Task) {
	if !hasStepFields(t) {
		return
	}
	data, err := yaml.Marshal(collectStepFields(t))
	if err != nil {
		return
	}
	b.Write(data)
}

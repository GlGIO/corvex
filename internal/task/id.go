package task

import (
	"regexp"

	"github.com/giovannialves/corvex/internal/types"
)

var (
	// looseHeadingRe matches any "## S<digits>" line — used to detect task
	// headings that almost match `headingRe` but have malformed status, missing
	// emoji, or other formatting errors. Lines that match this but NOT
	// `headingRe` are reported as parse errors instead of silently dropped.
	//
	// Deliberately NOT widened alongside headingRe. "Looks like a task heading"
	// stops being decidable once ids are arbitrary: a plain `## Notes` inside a
	// description would start failing the parse. The right place to catch an
	// unreadable id is where the user wrote it — recipe.Validate — not here,
	// guessing at the sink.
	looseHeadingRe = regexp.MustCompile(`^##\s+S\d+\b`)

	// taskIDValidRe anchors taskIDPattern for standalone validation.
	taskIDValidRe = regexp.MustCompile(`^(?:` + taskIDPattern + `)$`)
)

// taskIDPattern is the shape of a task id that survives a round trip through
// tasks.md: it may not contain whitespace (the heading is whitespace-delimited)
// and may not start with a character that would be read as the id/title
// separator. Slashes are allowed because fan-out mints `S06/003/apply`.
const taskIDPattern = `[A-Za-z0-9_][A-Za-z0-9_./\-]*`

// ValidTaskID reports whether an id can be written to tasks.md and read back.
//
// Exported so the recipe compiler enforces exactly this rule instead of a
// second one that drifts: a stage id that fails here is a stage that would be
// written to disk and then silently disappear.
func ValidTaskID(id string) bool {
	return taskIDValidRe.MatchString(id)
}

var statusEmoji = map[types.TaskStatus]string{
	types.StatusPending: "⬜",
	types.StatusRunning: "🔄",
	types.StatusPassed:  "✅",
	types.StatusFailed:  "❌",
	types.StatusSkipped: "⏭" + "\uFE0F",
}

// statusWord maps the textual status token in a task heading to a canonical
// status. Beyond the canonical words it accepts the synonyms LLMs tend to emit
// when regenerating tasks.md (e.g. a replan writing "✅ COMPLETED" or "✅ DONE"
// instead of "✅ PASSED"). Being lenient here keeps a benign wording drift from
// failing the whole run; truly unknown words still fall through to the
// "unrecognized task heading" error. Lookups are upper-cased by the caller.
var statusWord = map[string]types.TaskStatus{
	"PENDING":     types.StatusPending,
	"TODO":        types.StatusPending,
	"PLANNED":     types.StatusPending,
	"RUNNING":     types.StatusRunning,
	"INPROGRESS":  types.StatusRunning,
	"IN-PROGRESS": types.StatusRunning,
	"WIP":         types.StatusRunning,
	"PASSED":      types.StatusPassed,
	"PASS":        types.StatusPassed,
	"COMPLETE":    types.StatusPassed,
	"COMPLETED":   types.StatusPassed,
	"DONE":        types.StatusPassed,
	"FAILED":      types.StatusFailed,
	"FAIL":        types.StatusFailed,
	"SKIPPED":     types.StatusSkipped,
	"SKIP":        types.StatusSkipped,
}

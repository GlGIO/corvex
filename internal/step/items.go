package step

import (
	"encoding/json"
	"strings"
)

// ParseItems reads a discovery step's output as a fan-out work list.
//
// Two shapes, because the two real cases want different things:
//
//   - one item per line, which is what every shell pipeline already produces
//     (`ls`, `git diff --name-only`, a `jq -r` at the end of an api call);
//   - a JSON array, for when items carry structure — which is what `wave_by`
//     needs, since a plain line has no field to group on.
//
// Structured items are kept as their compact JSON text rather than parsed into
// some item type. That keeps the persisted work list a []string, so tasks.md
// stays greppable and a resumed run reads back exactly what was discovered.
func ParseItems(out string) []string {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var raw []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &raw); err == nil {
			items := make([]string, 0, len(raw))
			for _, r := range raw {
				// A JSON string item is unquoted, so `["a","b"]` and a
				// two-line output describe the same work list.
				var s string
				if err := json.Unmarshal(r, &s); err == nil {
					if s = strings.TrimSpace(s); s != "" {
						items = append(items, s)
					}
					continue
				}
				items = append(items, string(r))
			}
			return items
		}
		// Not valid JSON after all: fall through and read it as lines rather
		// than refusing. A shell that prints a bracket is not an error.
	}
	var items []string
	for _, line := range strings.Split(trimmed, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			items = append(items, l)
		}
	}
	return items
}

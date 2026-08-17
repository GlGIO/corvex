package gate

import (
	"fmt"
	"strings"

	"github.com/giovannialves/corvex/internal/types"
)

// MaxEvidenceBytes caps one piece of evidence.
//
// Nothing about 256 KiB is principled; what is principled is that the ceiling
// exists. `from: "cat migrations/*.sql"` in a large repository writes megabytes
// into a JSON file that F7 will load whole, and the moment to discover that is
// not while building the UI. Truncation is marked in the content itself as well
// as in the flag, so a reader who only sees the text still knows.
const MaxEvidenceBytes = 256 << 10

const truncationNote = "\n\n[... truncated by corvex: evidence exceeds 256 KiB ...]"

// Cap trims one evidence item to the ceiling and records that it did.
func Cap(e types.Evidence) types.Evidence {
	if len(e.Content) <= MaxEvidenceBytes {
		return e
	}
	// Cut on a rune boundary so the JSON stays valid UTF-8 text rather than
	// ending in half a multi-byte character.
	cut := MaxEvidenceBytes - len(truncationNote)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !isRuneStart(e.Content[cut]) {
		cut--
	}
	e.Content = e.Content[:cut] + truncationNote
	e.Truncated = true
	return e
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// CapAll applies Cap to every item.
func CapAll(items []types.Evidence) []types.Evidence {
	if len(items) == 0 {
		return items
	}
	out := make([]types.Evidence, len(items))
	for i, e := range items {
		out[i] = Cap(e)
	}
	return out
}

// RequiredLabels lists the evidence a decider has to acknowledge.
func RequiredLabels(items []types.Evidence) []string {
	var out []string
	for _, e := range items {
		if e.RequiredReading {
			out = append(out, e.Label)
		}
	}
	return out
}

// MissingAcks reports which required labels were not acknowledged.
//
// Matching is case-insensitive and whitespace-trimmed but otherwise exact: the
// labels come from `gate show`, so a decider who has not looked cannot guess
// them, and one who has can copy them. This is friction, not proof — typing a
// label is not reading it, and the real sensor (approval latency, rejection
// rate per gate) is F5. What it buys today is that approving stops being a
// single keystroke.
func MissingAcks(items []types.Evidence, acked []string) []string {
	seen := make(map[string]bool, len(acked))
	for _, a := range acked {
		seen[normalizeLabel(a)] = true
	}
	var missing []string
	for _, label := range RequiredLabels(items) {
		if !seen[normalizeLabel(label)] {
			missing = append(missing, label)
		}
	}
	return missing
}

func normalizeLabel(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// FromCommandOutput builds the evidence a computational check produced.
func FromCommandOutput(label, output string, passed bool) types.Evidence {
	status := types.EvidenceFail
	if passed {
		status = types.EvidencePass
	}
	return Cap(types.Evidence{
		Kind:    types.EvidenceTestOutput,
		Label:   label,
		Status:  status,
		Content: strings.TrimSpace(output),
	})
}

// FromVerdict builds the evidence an inferential gate produced.
func FromVerdict(label, verdict, category, summary string, passed bool) types.Evidence {
	status := types.EvidenceFail
	if passed {
		status = types.EvidencePass
	}
	var b strings.Builder
	fmt.Fprintf(&b, "VERDICT: %s\n", verdict)
	if strings.TrimSpace(category) != "" {
		fmt.Fprintf(&b, "CATEGORY: %s\n", category)
	}
	if s := strings.TrimSpace(summary); s != "" {
		b.WriteString("\n")
		b.WriteString(s)
	}
	return Cap(types.Evidence{
		Kind:    types.EvidenceVerdict,
		Label:   label,
		Status:  status,
		Content: b.String(),
	})
}

// FromDiff builds the evidence a code step produced.
//
// Status is warn rather than pass on purpose: a diff is not a verdict. Painting
// it green would make a gate screen look like something passed when all that
// happened is that code was written.
func FromDiff(label, diff string) types.Evidence {
	return Cap(types.Evidence{
		Kind:    types.EvidenceDiff,
		Label:   label,
		Status:  types.EvidenceWarn,
		Content: strings.TrimSpace(diff),
	})
}

// Note builds a plain warn-level note — used for things like "the fan-out
// discovered zero items", where silence would read as success.
func Note(label, text string) types.Evidence {
	return Cap(types.Evidence{
		Kind:    types.EvidenceVerdict,
		Label:   label,
		Status:  types.EvidenceWarn,
		Content: strings.TrimSpace(text),
	})
}

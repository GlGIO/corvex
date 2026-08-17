package gate

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/giovannialves/corvex/internal/types"
)

func TestCapLeavesSmallEvidenceAlone(t *testing.T) {
	e := types.Evidence{Kind: types.EvidenceDiff, Label: "L", Content: "small"}
	got := Cap(e)
	if got.Content != "small" || got.Truncated {
		t.Fatalf("Cap altered content under the ceiling: %+v", got)
	}
}

func TestCapTruncatesAndSaysSo(t *testing.T) {
	e := types.Evidence{Kind: types.EvidenceSQL, Label: "L", Content: strings.Repeat("x", MaxEvidenceBytes*2)}
	got := Cap(e)
	if !got.Truncated {
		t.Error("Truncated flag not set")
	}
	if len(got.Content) > MaxEvidenceBytes {
		t.Errorf("content is %d bytes, ceiling is %d", len(got.Content), MaxEvidenceBytes)
	}
	// The note is in the content too: a reader who only sees the text must
	// still know something was cut.
	if !strings.Contains(got.Content, "truncated by corvex") {
		t.Error("truncation is invisible in the content itself")
	}
}

// TestCapCutsOnRuneBoundary: cutting mid-character would leave invalid UTF-8 in
// a JSON file that F7 loads.
func TestCapCutsOnRuneBoundary(t *testing.T) {
	// "é" is two bytes, so a byte-aligned cut lands mid-rune for half of them.
	e := types.Evidence{Kind: types.EvidenceDiff, Label: "L", Content: strings.Repeat("é", MaxEvidenceBytes)}
	got := Cap(e)
	if !utf8.ValidString(got.Content) {
		t.Fatal("Cap produced invalid UTF-8")
	}
}

func TestRequiredLabelsAndMissingAcks(t *testing.T) {
	items := []types.Evidence{
		{Label: "Migration", RequiredReading: true},
		{Label: "Testes"},
		{Label: "Plano de query", RequiredReading: true},
	}
	if got := RequiredLabels(items); len(got) != 2 {
		t.Fatalf("RequiredLabels = %v, want 2 items", got)
	}
	if got := MissingAcks(items, nil); len(got) != 2 {
		t.Fatalf("MissingAcks(nil) = %v, want both", got)
	}
	// Case and surrounding whitespace do not matter; the label does.
	if got := MissingAcks(items, []string{"  migration ", "PLANO DE QUERY"}); len(got) != 0 {
		t.Fatalf("MissingAcks = %v, want none", got)
	}
	// Acknowledging something that is not required does not satisfy what is.
	if got := MissingAcks(items, []string{"Testes"}); len(got) != 2 {
		t.Fatalf("MissingAcks = %v, want both still missing", got)
	}
}

func TestProducersSetStatusHonestly(t *testing.T) {
	if got := FromCommandOutput("Testes", "312/312", true); got.Status != types.EvidencePass {
		t.Errorf("passing command → %q", got.Status)
	}
	if got := FromCommandOutput("Testes", "boom", false); got.Status != types.EvidenceFail {
		t.Errorf("failing command → %q", got.Status)
	}
	v := FromVerdict("Review", "FAIL", "wrong-approach", "it reimplements X", false)
	if v.Status != types.EvidenceFail || !strings.Contains(v.Content, "CATEGORY: wrong-approach") {
		t.Errorf("verdict evidence lost its category: %+v", v)
	}
	// A diff is not a verdict: painting it green would make a gate screen look
	// like something passed when all that happened is code was written.
	if got := FromDiff("Diff", "1 file changed"); got.Status != types.EvidenceWarn {
		t.Errorf("diff status = %q, want warn", got.Status)
	}
}

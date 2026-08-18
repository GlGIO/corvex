package ops

import (
	"testing"
	"time"
)

// The spelling every document in this repository uses — and that the tool itself
// prints back ("No runs in the last 7d") — was the one time.ParseDuration
// rejects. An audit found `--since 2d` failing against the roadmap's own command
// table.
func TestParseWindow_AcceptsTheSpellingTheToolTeaches(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"":     0,
		"0":    0,
		"90m":  90 * time.Minute,
		"36h":  36 * time.Hour,
		"7d":   7 * 24 * time.Hour,
		"2d":   2 * 24 * time.Hour,
		"0.5d": 12 * time.Hour,
	} {
		got, err := ParseWindow(raw)
		if err != nil {
			t.Errorf("ParseWindow(%q): %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("ParseWindow(%q) = %s, want %s", raw, got, want)
		}
	}
}

func TestParseWindow_RefusesNonsenseWithTheSpellingsThatWork(t *testing.T) {
	for _, raw := range []string{"2 days", "yesterday", "xd", "7w"} {
		if _, err := ParseWindow(raw); err == nil {
			t.Errorf("ParseWindow(%q) was accepted", raw)
		}
	}
}

package ops

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseWindow reads a time window the way every document in this repository
// writes one: `7d`, `2d`, `36h`, `90m`.
//
// Go's time.ParseDuration has no day unit, so `--since 2d` was rejected by the
// flag the roadmap's own command table spells `run list --since 2d`, and by the
// `?since=` of the UI API. The tool also PRINTS days ("No runs in the last 7d"),
// so the rejected spelling is the one the tool teaches.
//
// Days are the only extension: a day is 24h here, with no calendar in it —
// nothing in this product is scheduled, so DST and leap seconds have nothing to
// disagree with. Weeks and months are deliberately absent, because a month is
// where "24h × N" stops being obviously true.
func ParseWindow(raw string) (time.Duration, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, nil
	}
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseFloat(rest, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid window %q: %s is not a number of days", raw, rest)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid window %q: use a duration like 90m, 36h or 7d", raw)
	}
	return d, nil
}

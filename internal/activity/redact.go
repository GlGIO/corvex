package activity

import (
	"os"
	"regexp"
	"strings"
)

// Path redaction on the way into the ledger.
//
// # Why this exists even though the producers were fixed
//
// The package comment promises that nothing describing the machine reaches this
// file, and the file is committed. F1 broke that promise with a `repo` field and
// fixed it by removing the field. F5 broke it again through `message`: the
// watchdog's "where it hung" line embedded the provider's summary of a tool
// INPUT — an absolute path for Read/Write/Edit, a whole command line for Bash —
// and an adversarial audit walked a canary token from there into a git commit
// that corvex's own auto_commit created.
//
// Those producers are fixed at the source, which is the real repair. This is the
// second layer, and it is deliberately narrow: it redacts ABSOLUTE PATHS and
// nothing else. It does not pretend to find secrets — a regex that claimed to
// would be worse than nothing, because the next person would trust it and stop
// thinking. What it buys is that the class of leak that has now happened twice
// (a machine path arriving inside free text) cannot reach disk through a
// producer nobody audited yet.
//
// # What it deliberately leaves alone
//
// Relative paths (`internal/step/foo.go`) are the user's own repository, which
// is the whole point of the message. URLs keep their host and path: a URL is not
// a fact about this machine, and mangling it would break the one thing a reader
// would want to click.
var (
	// A run of at least two slash-separated segments starting at the root, not
	// preceded by "//" (which would be the authority part of a URL).
	absolutePath = regexp.MustCompile(`(^|[^:/\w])(/[\w.@+-]+){2,}/?`)
	schemePrefix = regexp.MustCompile(`\w+://`)
)

// RedactPaths replaces absolute filesystem paths with a placeholder.
func RedactPaths(s string) string {
	if s == "" {
		return s
	}
	// Protect URLs first: their scheme is put back after the path pass, so an
	// http://host/a/b in a message survives whole.
	holes := map[string]string{}
	i := 0
	s = schemePrefix.ReplaceAllStringFunc(s, func(m string) string {
		i++
		key := "\x00url" + string(rune('a'+i)) + "\x00"
		holes[key] = m
		return key
	})

	s = absolutePath.ReplaceAllStringFunc(s, func(m string) string {
		lead := ""
		if len(m) > 0 && m[0] != '/' {
			lead = m[:1]
		}
		return lead + "<path>"
	})

	for key, val := range holes {
		s = strings.ReplaceAll(s, key, val)
	}
	return s
}

// redactHome collapses the user's home directory even when it appears without a
// second segment (`/Users/alice`), which the general pattern would keep.
func redactHome(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return s
	}
	return strings.ReplaceAll(s, strings.TrimRight(home, "/"), "<home>")
}

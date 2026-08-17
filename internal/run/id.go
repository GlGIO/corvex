package run

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	// IDPrefix marks a run id as a run id when it shows up bare in a log line,
	// a terminal prompt or a URL path.
	IDPrefix = "run_"

	// IDHexDigits is the default id width. Four hex digits (16 bits) is what
	// the roadmap and the design canvas use (`run_8f21`): short enough to type
	// from memory and to fit in a table column, wide enough that collisions are
	// rare at real volume. Randomness alone would not be enough — 4 digits hit
	// a 50% birthday collision around 300 ids — so the generator is paired with
	// a uniqueness oracle in uniqueID: a candidate that is already taken is
	// discarded and re-drawn. Width buys readability, the retry buys
	// correctness.
	IDHexDigits = 4

	// idAttempts bounds the retry loop. With 65536 slots and a repository
	// holding even a thousand runs, 8 draws failing is not bad luck, it is a
	// broken generator (a stub returning a constant, an exhausted entropy
	// source) and deserves an error rather than an infinite loop.
	idAttempts = 8
)

// ErrIDSpaceExhausted is returned when no free id could be drawn.
//
// It is a sentinel because callers act on it: this is the one identity failure a
// user can do something about (see RetentionEnv), and it must be distinguishable
// from a broken generator or an unreadable index. What no caller may do is
// continue without an id — a ledger line nobody can attribute is worse than a run
// that refused to start, and it appears exactly when someone is trying to work
// out what went wrong.
var ErrIDSpaceExhausted = errors.New("run id: no free id available")

// IDFunc produces a candidate run id. It is a function, not a call to
// crypto/rand inside the rule, so tests can pin the id: a raw clock or a raw
// random source reaching golden output makes the network flaky.
type IDFunc func() (string, error)

// HexID returns an IDFunc drawing `digits` hex characters from src.
func HexID(src io.Reader, digits int) IDFunc {
	return func() (string, error) {
		if digits <= 0 {
			return "", fmt.Errorf("run id: digits must be positive, got %d", digits)
		}
		buf := make([]byte, (digits+1)/2)
		if _, err := io.ReadFull(src, buf); err != nil {
			return "", fmt.Errorf("run id: reading entropy: %w", err)
		}
		return IDPrefix + hex.EncodeToString(buf)[:digits], nil
	}
}

// DefaultIDFunc is the production generator: crypto/rand, IDHexDigits wide.
func DefaultIDFunc() (string, error) { return HexID(rand.Reader, IDHexDigits)() }

// ValidID reports whether s is a well-formed run id: the prefix followed by at
// least one lowercase hex digit and nothing else.
//
// This is not cosmetic. Record paths are built by joining the id onto a
// directory, so an id carrying `/` or `..` would let a caller write outside
// `.corvex/runs/`. Every path helper validates first.
func ValidID(s string) bool {
	if len(s) <= len(IDPrefix) || s[:len(IDPrefix)] != IDPrefix {
		return false
	}
	for _, c := range s[len(IDPrefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// uniqueID draws from gen until it gets a valid id that taken() rejects
// nothing about. A nil taken means "everything is free".
func uniqueID(gen IDFunc, taken func(string) bool) (string, error) {
	if gen == nil {
		gen = DefaultIDFunc
	}
	last := ""
	for i := 0; i < idAttempts; i++ {
		id, err := gen()
		if err != nil {
			return "", err
		}
		if !ValidID(id) {
			return "", fmt.Errorf("run id: generator produced invalid id %q", id)
		}
		if taken == nil || !taken(id) {
			return id, nil
		}
		last = id
	}
	return "", fmt.Errorf("%w: %d attempts exhausted, last candidate %q already taken",
		ErrIDSpaceExhausted, idAttempts, last)
}

package orchestrator

import "errors"

// fatalError wraps an error that must abort the entire run immediately.
// Task-level failures (worker/review exhausted retries) are returned as plain
// errors; only cost-ceiling breaches and human-escalation cases use this
// wrapper so the Run loop (S03) can tell them apart.
type fatalError struct{ cause error }

func (e *fatalError) Error() string { return e.cause.Error() }
func (e *fatalError) Unwrap() error { return e.cause }

// fatal wraps err so callers can detect it with isFatal.
func fatal(err error) error { return &fatalError{cause: err} }

// isFatal reports whether err (or anything in its chain) is a fatalError.
func isFatal(err error) bool {
	var f *fatalError
	return errors.As(err, &f)
}

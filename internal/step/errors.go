package step

import "errors"

// fatalError wraps an error that must abort the entire run immediately.
// Task-level failures (worker/review exhausted retries) are returned as plain
// errors; only cost-ceiling breaches and human-escalation cases use this
// wrapper so the scheduler loop can tell them apart.
type fatalError struct{ cause error }

func (e *fatalError) Error() string { return e.cause.Error() }
func (e *fatalError) Unwrap() error { return e.cause }

// Fatal wraps err so callers can detect it with IsFatal.
func Fatal(err error) error { return &fatalError{cause: err} }

// IsFatal reports whether err (or anything in its chain) is a fatalError.
func IsFatal(err error) bool {
	var f *fatalError
	return errors.As(err, &f)
}

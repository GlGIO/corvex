// Package ops holds corvex operations: the business rules that both the CLI and
// the HTTP surface call. An operation resolves, validates or mutates workspace
// state and returns (value, error).
//
// Hard rules for everything added here:
//
//   - no cobra — flag parsing belongs to cmd/;
//   - nothing from the fmt print family, no process exit — an operation
//     returns, it does not write to stdout or terminate. Progress is reported
//     through an io.Writer, channel or callback supplied by the caller;
//   - no terminal UI package — presentation never leaks in;
//   - no terminal opinions in error values. When a rule fails in a way the CLI
//     wants to explain with a hint ("pass --here", "run 'corvex init'"), the
//     operation returns the facts (paths, names, underlying error) and cmd/
//     writes the sentence.
package ops

package stepout_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/stepout"
)

// Truncation keeps the END, because a command's verdict is its last words: `go
// test` prints FAIL last, a CLI prints its usage error last, a stack trace ends
// at the frame the caller cares about. A head would reliably keep the banner and
// drop the answer, which is the failure mode this whole store exists to fix.
func TestTail_KeepsTheLastLinesAndSaysHowManyItDropped(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= stepout.MaxLines+7; i++ {
		fmt.Fprintf(&b, "linha %d\n", i)
	}
	got := stepout.Tail(b.String())
	lines := strings.Split(got, "\n")

	if lines[0] != "[7 earlier line(s) dropped]" {
		t.Errorf("first line = %q, want the exact count that was dropped — a truncation that does not admit itself makes the reader believe the command said only this", lines[0])
	}
	if len(lines) != stepout.MaxLines+1 {
		t.Fatalf("kept %d lines (marker included), want %d", len(lines), stepout.MaxLines+1)
	}
	last := fmt.Sprintf("linha %d", stepout.MaxLines+7)
	if lines[len(lines)-1] != last {
		t.Errorf("last kept line = %q, want %q: the tail is the point", lines[len(lines)-1], last)
	}
	if strings.Contains(got, "linha 1\n") {
		t.Errorf("the head survived: %q", got)
	}
}

// The line bound says nothing about size. One line of minified JSON or base64
// is a real thing a failing HTTP call prints, and it must not become the whole
// screen.
func TestTail_BoundsBytesWhenThereAreNoNewlines(t *testing.T) {
	blob := strings.Repeat("x", stepout.MaxBytes+512)
	got := stepout.Tail(blob)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("Tail() produced %d lines, want the marker plus one long line", len(lines))
	}
	if lines[0] != "[512 earlier byte(s) trimmed]" {
		t.Errorf("marker = %q, want the exact byte count", lines[0])
	}
	if len(lines[1]) != stepout.MaxBytes {
		t.Errorf("kept %d bytes, want the %d-byte bound", len(lines[1]), stepout.MaxBytes)
	}
}

// A byte cut that lands inside a multi-byte rune renders as a replacement
// character and makes the first kept line look corrupted — which an operator
// reading a diagnostic would reasonably blame on the tool.
func TestTail_NeverStartsInsideARune(t *testing.T) {
	// "é" is two bytes; an odd-length prefix guarantees the naive cut lands
	// between them.
	blob := strings.Repeat("é", stepout.MaxBytes)
	got := stepout.Tail(blob)
	body := got[strings.Index(got, "\n")+1:]
	if strings.ContainsRune(body, '�') {
		t.Errorf("Tail() cut mid-rune: %q", body[:12])
	}
	if !strings.HasPrefix(body, "é") {
		t.Errorf("kept body starts with %q, want a whole rune", body[:3])
	}
}

// Short output passes through untouched and gains no marker: most failures are
// one line, and a note about truncation that did not happen is noise the reader
// has to learn to ignore.
func TestTail_LeavesShortOutputAloneAndDropsEmptyOutput(t *testing.T) {
	if got := stepout.Tail("ERROR: --repository is required\n"); got != "ERROR: --repository is required" {
		t.Errorf("Tail() = %q, want the message with no marker and no trailing newline", got)
	}
	for _, in := range []string{"", "\n\n", "   \n\t\n"} {
		if got := stepout.Tail(in); got != "" {
			t.Errorf("Tail(%q) = %q, want empty: a command that printed nothing has no diagnostic", in, got)
		}
	}
}

// Path builds a filename out of two ids, so it has to refuse the ids that would
// make it write somewhere else. Fan-out mints step ids containing `/`, which is
// why escaping rather than rejecting is the rule for the legal ones.
func TestPath_RefusesIdsThatWouldEscapeTheDirectory(t *testing.T) {
	repo := t.TempDir()
	for _, tc := range []struct{ name, runID, stepID string }{
		{"traversal in the step id", "run_8f21", ".."},
		{"traversal in the run id", "../../etc", "S01"},
		{"empty step id", "run_8f21", ""},
		{"malformed run id", "nope", "S01"},
	} {
		if got, err := stepout.Path(repo, tc.runID, tc.stepID); err == nil {
			t.Errorf("%s: Path() = %q, want an error", tc.name, got)
		}
	}
	// A fan-out id is legal and must not turn into directories.
	got, err := stepout.Path(repo, "run_8f21", "S06/003/apply")
	if err != nil {
		t.Fatalf("Path() on a fan-out step id: %v", err)
	}
	if base := filepath.Base(got); base != "run_8f21-S06%2F003%2Fapply.txt" {
		t.Errorf("filename = %q, want the slashes escaped into one component", base)
	}
	if filepath.Dir(got) != stepout.Dir(repo) {
		t.Errorf("Path() = %q, outside Dir(%q)", got, repo)
	}
}

// Write/Read round-trip, plus the two "nothing there" cases the screens rely on:
// a step that never failed, and a run that predates the store.
func TestWriteRead_RoundTripsAndAnswersEmptyWhenThereIsNothing(t *testing.T) {
	repo := t.TempDir()
	if got := stepout.Read(repo, "run_8f21", "S03"); got != "" {
		t.Errorf("Read() before any write = %q, want empty", got)
	}
	if err := stepout.Write(repo, "run_8f21", "S03", "ERROR: --repository is required"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := stepout.Read(repo, "run_8f21", "S03"); got != "ERROR: --repository is required" {
		t.Errorf("Read() = %q", got)
	}
	// A retried step overwrites: the second failure is the one being looked at.
	if err := stepout.Write(repo, "run_8f21", "S03", "ERROR: second try"); err != nil {
		t.Fatalf("Write again: %v", err)
	}
	if got := stepout.Read(repo, "run_8f21", "S03"); got != "ERROR: second try" {
		t.Errorf("after a retry Read() = %q, want the newer failure", got)
	}
	if got := stepout.Read(repo, "run_8f21", "S04"); got != "" {
		t.Errorf("Read() for a step that never failed = %q, want empty", got)
	}
	// Empty output writes no file at all, so a step whose command printed
	// nothing leaves no empty artefact behind.
	if err := stepout.Write(repo, "run_8f21", "S05", "   \n"); err != nil {
		t.Fatalf("Write empty: %v", err)
	}
	if path, _ := stepout.Path(repo, "run_8f21", "S05"); path != "" {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("Write() of blank output created %s", path)
		}
	}
}

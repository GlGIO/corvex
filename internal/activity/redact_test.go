package activity_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/activity"
)

// Path redaction. The canary shapes are the ones an audit actually found on the
// way into a git commit: a Bash command line carrying a token and a home path,
// and a provider's stderr carrying a credentials file.

func TestRedactPaths_RemovesAbsolutePathsAndKeepsTheRest(t *testing.T) {
	cases := map[string]string{
		"tool Bash ($ deploy.sh --home=/Users/victim/secret)": "tool Bash ($ deploy.sh --home=<path>)",
		"could not read /Users/victim/.aws/credentials":       "could not read <path>",
		"internal/step/foo.go:12 failed":                      "internal/step/foo.go:12 failed",
		"see https://example.com/a/b for why":                 "see https://example.com/a/b for why",
		"3 uncommitted change(s)":                             "3 uncommitted change(s)",
	}
	for in, want := range cases {
		if got := activity.RedactPaths(in); got != want {
			t.Errorf("RedactPaths(%q)\n  got  %q\n  want %q", in, got, want)
		}
	}
}

// The end-to-end property, stated the way the package comment states it: what
// lands on disk carries no absolute path, whatever a producer put in the entry.
func TestAppend_NeverWritesAnAbsolutePath(t *testing.T) {
	workDir := t.TempDir()
	project := "alpha"
	if err := os.MkdirAll(filepath.Join(workDir, ".corvex", "tasks", project), 0o755); err != nil {
		t.Fatal(err)
	}
	ledger, err := activity.New(workDir, project, activity.Identity{RunID: "run_ab12"})
	if err != nil {
		t.Fatalf("activity.New: %v", err)
	}
	if err := ledger.Append(activity.Entry{
		Type:    "task_timeout",
		TaskID:  "S01",
		Message: "task S01 aborted: no provider output (last activity: tool Bash ($ deploy.sh --token=sk-ant-CANARY111 --config=/Users/victim/.aws/credentials))",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Read it back the way any consumer would, rather than grepping the raw
	// bytes: encoding/json escapes `<` and `>`, so a raw grep for the marker
	// would fail on a line that is in fact correct.
	entries, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("activity.Read: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	line := entries[0].Message
	if strings.Contains(line, "/Users/victim") {
		t.Errorf("an absolute path reached the committed file:\n%s", line)
	}
	if !strings.Contains(line, "<path>") {
		t.Errorf("the path was dropped rather than marked, so a reader cannot tell something was there:\n%s", line)
	}
	// Stated out loud so nobody reads more into this than it does: redaction
	// covers PATHS. A secret pasted into free text is stopped at the producer,
	// not here — see redact.go.
	if !strings.Contains(line, "CANARY111") {
		t.Log("note: the token survived redaction, as documented — producers are the defence for secrets")
	}
}

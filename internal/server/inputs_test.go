package server_test

// What the recipe needs to know, asked for on the screen that starts the run.
//
// A recipe declares `requires: - env: INCIDENT_ID`. Before this, the UI could
// dispatch it and nothing else: the run died in its own preflight saying the
// variable was missing, and the person went to a terminal to do what they had
// just tried to do on the screen. The three things measured here are the three
// that make the field trustworthy — the value reaches the run, it does NOT reach
// the command line, and a name the repository declared as the runner's own is
// refused instead of typed into a browser.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/server"
)

func TestStartRun_InputsReachTheRunsEnvironmentAndNotItsArgv(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argv := filepath.Join(root, "argv")
	stub := stubBinary(t, argv, `printf 'ENV INCIDENT_ID=%s\n' "$INCIDENT_ID" >> `+argv, 0)
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stub})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	rec, body := post(t, srv.Handler(), srv, "/api/runs", `{"target":"incident","inputs":{"INCIDENT_ID":"73960"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body)
	}
	// The parity line is the line a person would type, assignments included, so
	// pasting it does the same thing rather than failing the same preflight.
	if command, _ := body["command"].(string); !strings.HasPrefix(command, "INCIDENT_ID=73960 corvex run start incident") {
		t.Errorf("recorded command = %q, want the assignment in front of it", command)
	}

	waitFor(t, "the child to report its environment", func() bool {
		data, err := os.ReadFile(argv)
		return err == nil && strings.Contains(string(data), "ENV INCIDENT_ID=")
	})
	data, _ := os.ReadFile(argv)
	if !strings.Contains(string(data), "ENV INCIDENT_ID=73960") {
		t.Errorf("the run did not see the value:\n%s", data)
	}
	// And it travelled in the environment, not on the command line: argv is
	// world-readable on this machine (`ps -Ao args`), which is the same reason
	// this repository passes tokens through a curl config instead of a flag.
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "ENV ") || line == workDir {
			continue
		}
		if strings.Contains(line, "73960") {
			t.Errorf("the value is in the run's argv: %q", line)
		}
	}
}

// A name the repository declared as the runner's own is refused, and the rule is
// read from the repository the run is DISPATCHED INTO.
//
// That second half is the defect: `ops.LoadConfig` answers for the process
// working directory, and one UI speaks for several checkouts. A dispatch into
// another one was judged by whatever config happened to be under the server's
// own cwd — and for a list of names that must never be let through, being judged
// by the wrong list means being let through.
func TestStartRun_RefusesARunnerOnlyNameUsingTheTargetRepositorysRules(t *testing.T) {
	root := t.TempDir()
	workDir := gitRepo(t, filepath.Join(root, "repo"))
	writeConfig(t, workDir, "security:\n  runner_only_env:\n    - SMARTCARE_PRD_URL\n")

	argv := filepath.Join(root, "argv")
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, argv, "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()

	// The checkout the run is dispatched into is a worktree, not the directory
	// this process is sitting in — the operating model the whole feature is for.
	_, made := post(t, h, srv, "/api/worktrees", `{"name":"73960","branch":"hotfix/73960"}`)
	worktree, _ := made["path"].(string)
	if worktree == "" {
		t.Fatalf("the worktree was not created: %v", made)
	}

	rec, body := post(t, h, srv, "/api/runs",
		`{"target":"incident","repo":"`+worktree+`","inputs":{"SMARTCARE_PRD_URL":"postgres://prod"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — the production URL was accepted as a form field: %s", rec.Code, rec.Body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "runner_only_env") {
		t.Errorf("error = %q, want it to name the rule that refused", msg)
	}
	// Nothing was spawned: the refusal happens before the process exists, so the
	// value never reaches an environment at all.
	if _, err := os.Stat(argv); err == nil {
		t.Error("a run was spawned despite the refusal")
	}
}

// The assignments belong to the corvex call, not to the line.
//
// MEASURED while running the control above: a dispatch into a worktree recorded
// `INCIDENT_ID=73960 cd /path && corvex run start incident`. That line sets the
// variable for `cd` and hands corvex nothing, so the one command the audit log
// promises is runnable dies in the exact preflight the field was added to
// satisfy — and it does so only when a `cd` is involved, which is every dispatch
// into a worktree, which is the whole operating model.
func TestStartRun_TheRecordedLineSetsTheVariableForCorvexNotForTheCd(t *testing.T) {
	root := t.TempDir()
	workDir := gitRepo(t, filepath.Join(root, "repo"))
	argv := filepath.Join(root, "argv")
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, argv, "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()
	_, made := post(t, h, srv, "/api/worktrees", `{"name":"73960","branch":"hotfix/73960"}`)
	worktree, _ := made["path"].(string)

	_, body := post(t, h, srv, "/api/runs",
		`{"target":"incident","repo":"`+worktree+`","inputs":{"INCIDENT_ID":"73960"}}`)
	command, _ := body["command"].(string)
	if !strings.Contains(command, "&& INCIDENT_ID=73960 corvex run start incident") {
		t.Errorf("recorded command = %q, want the assignment on the corvex call", command)
	}
}

// The same name is accepted when the repository does not claim it, so the
// refusal above is the rule doing its job and not the form refusing everything.
func TestStartRun_AcceptsANameNoRepositoryClaims(t *testing.T) {
	root := t.TempDir()
	workDir := gitRepo(t, filepath.Join(root, "repo"))
	writeConfig(t, workDir, "security:\n  runner_only_env:\n    - AZURE_DEVOPS_PAT\n")

	argv := filepath.Join(root, "argv")
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, argv, "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	rec, _ := post(t, srv.Handler(), srv, "/api/runs", `{"target":"incident","inputs":{"INCIDENT_ID":"73960"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body)
	}
}

// The catalogue carries the declared inputs, which is how the form knows what to
// ask for. Read through ops rather than the wire: the handler returns it
// verbatim, and this is the layer that reads the recipe.
func TestListRecipes_CarriesTheDeclaredInputs(t *testing.T) {
	dir := t.TempDir()
	writeFileAt(t, filepath.Join(dir, ".corvex", "recipes", "incident.yaml"), `name: incident
description: |
  Um incidente.
requires:
  - env: INCIDENT_ID
    why: "o id do work item no board"
stages:
  - id: S01
    kind: tool
    title: "Contexto"
    command: "echo ${INCIDENT_ID}"
`)
	list := ops.ListRecipes(dir)
	if len(list) != 1 {
		t.Fatalf("got %d recipes, want 1", len(list))
	}
	if len(list[0].Inputs) != 1 {
		t.Fatalf("the recipe declares one input, the catalogue carries %d: %+v", len(list[0].Inputs), list[0])
	}
	if got := list[0].Inputs[0]; got.Name != "INCIDENT_ID" || got.Why != "o id do work item no board" {
		t.Errorf("input = %+v, want the name and the author's reason", got)
	}
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	writeFileAt(t, filepath.Join(dir, ".corvex", "config.yaml"), body)
}

func writeFileAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

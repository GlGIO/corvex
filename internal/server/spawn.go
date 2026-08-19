package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/run"
)

// startRequest is what the "dispatch a run" screen (2c) sends.
type startRequest struct {
	Target      string `json:"target"` // recipe or project name
	Environment string `json:"environment,omitempty"`
	Task        string `json:"task,omitempty"`
	Recompile   bool   `json:"recompile,omitempty"`
}

// handleStartRun spawns a DETACHED run and returns immediately.
//
// Detached, not a child this server waits on, and the difference is the trap F1
// wrote down: a child nobody reaps becomes a zombie, a zombie still answers
// signal 0, and liveness would then report a dead run as alive. Setsid also
// means the run survives the UI being closed, which is the property that makes
// "no daemon" work — the server supervises runs, it never owns them.
//
// The run's own identity (F1) is what connects the two processes afterwards:
// this handler does not track the child, it reads it back from the index like
// any other reader.
func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := decodeBody(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Target) == "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("target is required: the recipe or project to run"))
		return
	}

	args := startArgs(req)
	command := "corvex " + strings.Join(args, " ")

	bin, err := s.binary()
	if err != nil {
		action := s.actions.Record(command, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "action": action})
		return
	}

	logPath, logFile, err := s.runLog(req.Target)
	if err != nil {
		action := s.actions.Record(command, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "action": action})
		return
	}
	defer logFile.Close()

	cmd := exec.Command(bin, args...)
	cmd.Dir = s.opts.WorkDir
	cmd.Stdout, cmd.Stderr = logFile, logFile
	// The run must not inherit this server's controlling terminal or process
	// group: with Setsid, Ctrl-C in the shell that started `corvex ui` cannot
	// take a run down with it, and closing the UI leaves the run untouched.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	err = cmd.Start()
	action := s.actions.Record(command, err)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "action": action})
		return
	}
	// The pid is read BEFORE anything releases the handle: os.Process.Release
	// sets Pid to -1 as part of releasing, so the first version of this handler
	// reported -1 on every dispatch — the one identifier the caller gets back,
	// always the sentinel.
	pid := cmd.Process.Pid

	// Reap on a goroutine instead of releasing.
	//
	// `Release` was chosen first with the argument that "this process must not
	// be the child's parent". That argument is wrong about what Release does:
	// it frees the Go-side handle and changes nothing about the process tree.
	// The child stays a child of this server, and with nobody calling Wait it
	// becomes a <defunct> zombie the moment it exits — which is exactly the
	// failure F1 documented, because a zombie still answers signal 0 and would
	// be read as `alive` by the liveness probe.
	//
	// Setsid is what actually delivers "close the UI and the run keeps going":
	// the child is in its own session, so no terminal signal reaches it. Waiting
	// costs one parked goroutine per dispatch and buys a process table that
	// tells the truth.
	go func() { _ = cmd.Wait() }()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"pid":     pid,
		"log":     logPath,
		"action":  action,
		"command": command,
	})
}

// startArgs builds the CLI invocation. The UI has no private vocabulary: what it
// spawns is the command a user could have typed, which is what makes the action
// log auditable rather than decorative.
func startArgs(req startRequest) []string {
	args := []string{"run", "start", req.Target, "--plain", "--yes"}
	if req.Environment != "" {
		args = append(args, "--env", req.Environment)
	}
	if req.Task != "" {
		args = append(args, "--task", req.Task)
	}
	if req.Recompile {
		args = append(args, "--recompile")
	}
	return args
}

// handleKillRun stops a live run. It goes through the same ops call `corvex run
// kill` uses, including the heartbeat proof that the pid still belongs to the
// run — a UI button is exactly the place where signalling a recycled pid would
// be least noticed.
func (s *Server) handleKillRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	command := "corvex run kill " + id
	res, err := s.lister().KillRun(id, ops.KillOptions{Prove: true})
	action := s.actions.Record(command, err)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "action": action})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"killed": res, "action": action})
}

// handlePauseRun and handleResumeRun are the non-destructive half of the run
// controls, and they exist here because of the parity rule: `run pause` is a
// command a user can type, so it is a button a user can press. No path may exist
// on only one surface.
//
// Three lines each, and the shape on the wire is ops.PauseResult verbatim.
func (s *Server) handlePauseRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.lister().PauseRun(id)
	s.writeRunControl(w, "corvex run pause "+id, "paused", res, err)
}

func (s *Server) handleResumeRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.lister().ResumeRun(id)
	s.writeRunControl(w, "corvex run resume "+id, "resumed", res, err)
}

// writeRunControl records the action and answers, in the shape handleKillRun
// established: 409 with the error text when ops refused, because every refusal
// here is "the run is not in a state for this", not a malformed request.
func (s *Server) writeRunControl(w http.ResponseWriter, command, key string, res ops.PauseResult, err error) {
	action := s.actions.Record(command, err)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "action": action})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{key: res, "action": action})
}

// binary is the corvex executable to spawn. Explicit override first so a test
// can point at a stub; os.Executable() otherwise, which is the only answer that
// keeps a spawned run on the same version as the server that spawned it.
func (s *Server) binary() (string, error) {
	if s.opts.Binary != "" {
		return s.opts.Binary, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating the corvex binary to spawn: %w", err)
	}
	return exe, nil
}

// runLog is where a detached run's output goes. It has to go somewhere: a
// detached process with no stdout writes into a closed descriptor, and the first
// thing anyone asks about a run started from a button is what it printed.
func (s *Server) runLog(target string) (string, *os.File, error) {
	dir := filepath.Join(s.opts.WorkDir, ".corvex", "runs", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("creating the run log directory: %w", err)
	}
	// A run's whole stdout lands here — the model's output, the commands it ran,
	// whatever a failing tool printed. It is not the user's history to publish,
	// and the guard is written HERE rather than assumed from F1: a UI that
	// dispatches before any run has recorded itself would otherwise create the
	// directory without it.
	if err := run.EnsureScratchIgnored(filepath.Join(s.opts.WorkDir, ".corvex", "runs")); err != nil {
		return "", nil, fmt.Errorf("guarding the run log directory: %w", err)
	}
	// One file per dispatch, not one per target: with a shared name the path the
	// API hands back points at a file that already holds every earlier run of
	// the same recipe, and nothing rotates it. The run id would be the right
	// key and cannot be used — it is minted inside the child, after the spawn —
	// so the dispatch time is the next best thing that is unique and readable.
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.log", sanitize(target), s.now().Format("20060102-150405")))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return path, f, nil
}

// sanitize keeps a target name from escaping the log directory.
func sanitize(name string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	if clean == "" {
		return "run"
	}
	return clean
}

// decodeBody reads a JSON body with a ceiling. A UI on localhost is not a
// hostile client, but a bounded read is the difference between a bug and an
// out-of-memory.
func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && err != io.EOF {
		return fmt.Errorf("reading the request body: %w", err)
	}
	return nil
}

// gateCommand renders the CLI equivalent of a gate decision, quoting each
// acknowledgement the way a shell would need it.
//
// The verdict is translated to the VERB rather than printed: the on-disk verdict
// is `approved`, the command is `approve`, and a log line that says
// `corvex gate approved run_8f21` is not a command anybody could run. The whole
// value of this log is that every line in it is executable — parity that cannot
// be pasted into a terminal is decoration.
func gateCommand(verdict gate.Verdict, id string, req decideRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "corvex gate %s %s", gateVerb(verdict), id)
	if req.Step != "" {
		fmt.Fprintf(&b, " --step %s", req.Step)
	}
	for _, ack := range req.Ack {
		fmt.Fprintf(&b, " --ack %q", ack)
	}
	if req.Reason != "" {
		fmt.Fprintf(&b, " --reason %q", req.Reason)
	}
	return b.String()
}

// answerCommand renders the CLI equivalent of answering a question.
//
// `answer` is a verb the CLI really has, which is the guard the action log
// learned the hard way (see gateCommand): a line in this file that is not
// runnable looks like parity and is not. The text is quoted the way a shell
// needs it, so a reply containing a space or a quote pastes back intact.
func answerCommand(id string, req answerRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "corvex gate answer %s", id)
	if req.Step != "" {
		fmt.Fprintf(&b, " --step %s", req.Step)
	}
	for _, ack := range req.Ack {
		fmt.Fprintf(&b, " --ack %q", ack)
	}
	fmt.Fprintf(&b, " --text %q", req.Text)
	return b.String()
}

// gateVerb maps a verdict to the CLI verb that produces it.
func gateVerb(v gate.Verdict) string {
	switch v {
	case gate.Approved:
		return "approve"
	case gate.Rejected:
		return "reject"
	default:
		return string(v)
	}
}

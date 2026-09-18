package server

import (
	"encoding/json"
	"errors"
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
	// Here forces the run into the directory the UI opened, even when the
	// project has a worktree. It is the `--here` flag, and it exists on the wire
	// for the same reason it exists on the CLI: the worktree can be a leftover
	// from an abandoned experiment, and the person looking at the screen is the
	// one who knows.
	Here bool `json:"here,omitempty"`
	// Repo dispatches into a repository other than the one the UI opened. Empty
	// means the local one.
	//
	// It is a CHOICE FROM A LIST, never a free path: the value has to match a
	// repository `GET /api/repos` offers, which is the repository under the
	// cursor plus the ones the run index has seen. Accepting any path would turn
	// a localhost surface whose whole job is to spawn a binary into one that
	// spawns it anywhere the poster names, and the fact that the token stops a
	// stranger is not a reason to leave that open to a mistake.
	Repo string `json:"repo,omitempty"`
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

	repo, repoErr := s.dispatchRepo(req)
	if repoErr != nil {
		fail(w, http.StatusBadRequest, repoErr)
		return
	}
	dir := s.dispatchDir(repo, req)
	args := startArgs(req)
	command := dispatchCommand(dir, s.opts.WorkDir, args)

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
	logName := filepath.Base(logPath)

	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = logFile, logFile
	// The run must not inherit this server's controlling terminal or process
	// group: with Setsid, Ctrl-C in the shell that started `corvex ui` cannot
	// take a run down with it, and closing the UI leaves the run untouched.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	err = cmd.Start()
	action := s.actions.RecordDispatch(command, err, logName)
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
	//
	// The wait is ALSO where the dispatch stops lying. A run that dies before it
	// registers itself — a dirty tree, a worktree mismatch, a recipe that does
	// not parse — never reaches the index, so the history stays empty and the
	// only record of it is the `ok` this handler already wrote. The UI reported
	// a success and the reason lived in a file nobody was told about. So the
	// exit status is recorded when it is known, with the line from the log that
	// says why.
	go func() {
		werr := cmd.Wait()
		s.actions.RecordOutcome(command, dispatchOutcome(werr, logPath), logName)
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"pid":      pid,
		"dir":      dir,
		"log":      logPath,
		"log_name": logName,
		"action":   action,
		"command":  command,
	})
}

// dispatchDir is the directory the run executes in.
//
// `corvex start <project>` opens a worktree beside the repo, and every later
// command for that project belongs in it — the CLI enforces that by REFUSING to
// run from the main checkout (cmd/guards.go). The UI used to spawn in its own
// WorkDir regardless, so dispatching a project that had a worktree produced a
// child that died on that guard while the screen said `ok`.
//
// Bypassing the guard with `--here` would have been the smaller change and the
// wrong one: the guard is right, and the UI is exactly the surface where "which
// checkout is this writing to" must not be a guess. So the UI answers the
// guard's question instead of silencing it — it dispatches INTO the worktree,
// which is also the model the operator has in mind when they keep one worktree
// per feature and watch all of them from one screen.
func (s *Server) dispatchDir(repo string, req startRequest) string {
	if req.Here {
		return repo
	}
	if wt := ops.FindProjectWorktree(repo, req.Target); wt != "" {
		return wt
	}
	return repo
}

// dispatchRepo resolves which repository a dispatch belongs to, refusing
// anything the machine has not already seen.
//
// The refusal names the two ways in, because "unknown repository" with no way
// forward is how a person concludes the feature does not exist: a repository
// joins the list by having been run once — from a terminal, or from a UI opened
// there — and that is a sentence the error can say.
func (s *Server) dispatchRepo(req startRequest) (string, error) {
	local := ops.CanonicalRepo(s.opts.WorkDir)
	asked := strings.TrimSpace(req.Repo)
	if asked == "" {
		return s.opts.WorkDir, nil
	}
	want := ops.CanonicalRepo(asked)
	if want == local {
		return s.opts.WorkDir, nil
	}
	for _, ws := range s.gates().Workspaces(s.opts.WorkDir) {
		if ws.Path == want {
			return ws.Path, nil
		}
	}
	return "", fmt.Errorf("%q is not a repository this machine has run: the list is the repository this UI opened plus the ones in the run index — run corvex there once, or open a UI in it", asked)
}

// dispatchCommand is the parity line for a spawn: what a person would have typed
// to do the same thing, including the `cd` when the run does not happen where
// the UI is. A recorded command that silently omits the directory is a command
// that does something else when pasted.
func dispatchCommand(dir, workDir string, args []string) string {
	command := "corvex " + strings.Join(args, " ")
	if dir != "" && dir != workDir {
		return "cd " + dir + " && " + command
	}
	return command
}

// dispatchOutcome phrases what became of a dispatched run: "ok" when it exited
// clean, and otherwise the exit status plus the reason the run itself printed.
func dispatchOutcome(werr error, logPath string) string {
	if werr == nil {
		return "ok"
	}
	status := werr.Error()
	var exit *exec.ExitError
	if errors.As(werr, &exit) {
		status = fmt.Sprintf("exit %d", exit.ExitCode())
	}
	if reason := failureReason(logPath); reason != "" {
		return status + " — " + reason
	}
	return status
}

// failureReason digs the explanation out of a dispatch log.
//
// It prefers the last `Error:` line, which is the shape cmd.Execute prints, and
// falls back to the last non-empty line — a run killed by a signal or by a
// crashing hook prints no `Error:` at all, and "exit 2" with nothing attached is
// the state this change exists to remove. Only the tail is read: a `code` step's
// log holds every token the model emitted.
func failureReason(logPath string) string {
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	const tail = 8 << 10
	offset := info.Size() - tail
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, info.Size()-offset)
	if _, rerr := f.ReadAt(buf, offset); rerr != nil && rerr != io.EOF {
		return ""
	}
	var lastError, lastLine string
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lastLine = line
		if strings.HasPrefix(line, "Error:") {
			lastError = strings.TrimSpace(strings.TrimPrefix(line, "Error:"))
		}
	}
	if lastError != "" {
		return truncateReason(lastError)
	}
	return truncateReason(lastLine)
}

// truncateReason keeps one line of the audit file readable. The whole log is one
// fetch away (GET /api/runs/logs/{name}); this is the sentence that tells the
// reader whether they need it.
func truncateReason(s string) string {
	const max = 240
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// startArgs builds the CLI invocation. The UI has no private vocabulary: what it
// spawns is the command a user could have typed, which is what makes the action
// log auditable rather than decorative.
func startArgs(req startRequest) []string {
	args := []string{"run", "start", req.Target, "--plain", "--yes"}
	if req.Here {
		args = append(args, "--here")
	}
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

// maxLogTail is how much of a dispatch log the API hands back. A `code` step's
// log is unbounded — every token the model emitted — and the question the reader
// has ("why did this die") is answered at the end of the file.
const maxLogTail = 64 << 10

// handleRunLog serves one dispatch log by name.
//
// It is the other half of recording the exit status: the audit line says `exit
// 1 — working tree has 1 uncommitted change(s)`, and this is where the reader
// goes when that sentence is not enough. Without it the UI can only ever point
// at a path on disk, which is the same as pointing at nothing for someone who is
// looking at a browser.
//
// The name is a base name inside this repo's log directory and nothing else:
// `filepath.Base` collapses any traversal, and the result has to match what was
// asked for, so `../../etc/passwd` is refused rather than quietly rewritten into
// something that happens to exist.
func (s *Server) handleRunLog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || filepath.Base(name) != name || !strings.HasSuffix(name, ".log") {
		fail(w, http.StatusBadRequest, fmt.Errorf("%q is not a dispatch log name", name))
		return
	}
	path := filepath.Join(s.opts.WorkDir, ".corvex", "runs", "logs", name)
	f, err := os.Open(path)
	if err != nil {
		fail(w, http.StatusNotFound, fmt.Errorf("no dispatch log named %q", name))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		fail(w, http.StatusNotFound, fmt.Errorf("no dispatch log named %q", name))
		return
	}
	offset := info.Size() - maxLogTail
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, info.Size()-offset)
	if _, rerr := f.ReadAt(buf, offset); rerr != nil && rerr != io.EOF {
		fail(w, http.StatusInternalServerError, fmt.Errorf("reading %s: %w", name, rerr))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":      name,
		"path":      path,
		"size":      info.Size(),
		"truncated": offset > 0,
		"content":   string(buf),
	})
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

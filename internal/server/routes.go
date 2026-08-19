package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/ops"
)

// routes wires the API. Go 1.22+ pattern routing gives method and path variables
// without a router dependency — one less thing embedded in a binary that has to
// stay small enough to trust.
//
// The shapes are the ops types verbatim (RunRow, RunReport, Inbox, GateView).
// That is deliberate: the CLI's `--json` prints the same structs, so the UI and
// `corvex ... --json` cannot disagree about what a run is.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/state", s.handleState)
	s.mux.HandleFunc("GET /api/events", s.handleEvents)
	s.mux.HandleFunc("GET /api/runs", s.handleRuns)
	s.mux.HandleFunc("GET /api/runs/{id}", s.handleRun)
	s.mux.HandleFunc("POST /api/runs", s.handleStartRun)
	s.mux.HandleFunc("POST /api/runs/{id}/kill", s.handleKillRun)
	s.mux.HandleFunc("GET /api/gates", s.handleGates)
	// Before the {id} pattern in intent, though not in effect: Go's mux prefers
	// the literal segment, and no run id can ever be the word `audit` (`run_` +
	// 4 hex), so the two cannot collide.
	s.mux.HandleFunc("GET /api/gates/audit", s.handleGateAudit)
	s.mux.HandleFunc("GET /api/gates/{id}", s.handleGate)
	s.mux.HandleFunc("POST /api/gates/{id}/approve", s.handleApprove)
	s.mux.HandleFunc("POST /api/gates/{id}/reject", s.handleReject)
	s.mux.HandleFunc("POST /api/gates/{id}/answer", s.handleAnswer)
	s.mux.HandleFunc("GET /api/actions", s.handleActions)
	s.mux.HandleFunc("GET /api/recipes", s.handleRecipes)
	s.mux.HandleFunc("GET /assets/", s.handleAsset)
	s.mux.HandleFunc("GET /", s.handleIndex)
}

// stateWindow is how far back the two list screens look. It is one constant
// because the event stream fingerprints the same set (stream.go): if the window
// the stream watches and the window the screen shows could drift apart, a run
// could change inside one and not the other, and the page would sit still
// holding a row that had moved.
const stateWindow = 7 * 24 * time.Hour

// handleState is screen 2a in one request: what is waiting, and what ran.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	inbox, err := s.gates().LoadInbox(s.opts.WorkDir)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	runs, err := s.lister().ListRuns(ops.RunListOptions{Since: stateWindow})
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"inbox": inbox,
		"runs":  runs,
		"repo":  s.opts.WorkDir,
	})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	opt := ops.RunListOptions{Since: stateWindow}
	if raw := r.URL.Query().Get("since"); raw != "" {
		// The same parser the CLI uses, so `?since=2d` and `--since 2d` cannot
		// disagree about what a day is — or about whether days exist.
		d, err := ops.ParseWindow(raw)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		opt.Since = d
	}
	if repo := r.URL.Query().Get("repo"); repo != "" {
		opt.Repo = repo
		if repo == "." {
			opt.Repo = s.opts.WorkDir
		}
	}
	rows, err := s.lister().ListRuns(opt)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	workDir := ""
	if !ops.IsRunID(id) {
		workDir = s.opts.WorkDir
	}
	report, err := s.lister().LoadRunReport(workDir, id, strings.ToUpper(r.URL.Query().Get("step")))
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleGates(w http.ResponseWriter, r *http.Request) {
	inbox, err := s.gates().LoadInbox(s.opts.WorkDir)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inbox)
}

// handleGateAudit is the sensor over the sensors on the wire — the same
// ops.GateAudit the CLI prints with `gate audit --json`, verbatim.
//
// It carries no evidence, by construction rather than by filtering here: the
// audit type never holds any (internal/ops/gate_audit.go). That is what makes it
// safe to hand to a browser tab that anyone on the machine can open, while
// `GET /api/gates/{id}` — which does embed the evidence — exists for the screen
// where a person deliberately asked to read it.
func (s *Server) handleGateAudit(w http.ResponseWriter, r *http.Request) {
	// Written out rather than reusing stateWindow: this default tracks the CLI's
	// `gate audit --since 7d`, while stateWindow tracks the set the event stream
	// fingerprints. They happen to be equal today and answer different questions,
	// so tying them together would make one move when the other was changed.
	opt := ops.GateAuditOptions{Since: 7 * 24 * time.Hour}
	if raw := r.URL.Query().Get("since"); raw != "" {
		// The same parser the CLI uses, so `?since=2d` and `--since 2d` cannot
		// disagree about what a day is. `?since=0` is every gate on disk.
		d, err := ops.ParseWindow(raw)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		opt.Since = d
	}
	audit, err := s.gates().LoadGateAudit(s.opts.WorkDir, opt)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, audit)
}

func (s *Server) handleGate(w http.ResponseWriter, r *http.Request) {
	view, err := s.gates().FindGate(r.PathValue("id"), r.URL.Query().Get("step"))
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// decideRequest is the body of approve/reject.
type decideRequest struct {
	Step   string   `json:"step"`
	Ack    []string `json:"ack,omitempty"`
	Reason string   `json:"reason,omitempty"`
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	s.decide(w, r, gate.Approved)
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	s.decide(w, r, gate.Rejected)
}

// decide is the mutating path that matters most, so it is also the one that
// records the CLI equivalent. The command string is built BEFORE the call and
// recorded with the outcome, so a refused approval leaves a trace too.
func (s *Server) decide(w http.ResponseWriter, r *http.Request, verdict gate.Verdict) {
	id := r.PathValue("id")
	var req decideRequest
	if err := decodeBody(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	command := gateCommand(verdict, id, req)

	decided, err := s.gates().DecideGate(id, req.Step, verdict, req.Ack, req.Reason)
	action := s.actions.Record(command, err)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "action": action})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gate": decided, "action": action})
}

// answerRequest is the body of answer. A type of its own rather than a field on
// decideRequest: the decoder rejects unknown fields, so a `text` posted at
// /approve has to fail rather than be quietly dropped — which is the same reason
// the two verbs are separate routes at all.
type answerRequest struct {
	Step string   `json:"step"`
	Ack  []string `json:"ack,omitempty"`
	Text string   `json:"text"`
}

// handleAnswer is the question axis on the wire, and the same three lines as
// every other mutating handler: decode, call ops, encode. The rule that an empty
// answer is not an answer is not repeated here — it is in gate.Decide, which
// this call reaches through ops, so the browser and the terminal are refused by
// the same sentence.
func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req answerRequest
	if err := decodeBody(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	command := answerCommand(id, req)

	answered, err := s.gates().AnswerGate(id, req.Step, req.Ack, req.Text)
	action := s.actions.Record(command, err)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "action": action})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gate": answered, "action": action})
}

func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	actions, err := s.actions.Read(limit)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, actions)
}

func (s *Server) handleRecipes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ops.ListRecipes(s.opts.WorkDir))
}

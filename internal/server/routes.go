package server

import (
	"fmt"
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
	s.mux.HandleFunc("GET /api/runs", s.handleRuns)
	s.mux.HandleFunc("GET /api/runs/{id}", s.handleRun)
	s.mux.HandleFunc("POST /api/runs", s.handleStartRun)
	s.mux.HandleFunc("POST /api/runs/{id}/kill", s.handleKillRun)
	s.mux.HandleFunc("GET /api/gates", s.handleGates)
	s.mux.HandleFunc("GET /api/gates/{id}", s.handleGate)
	s.mux.HandleFunc("POST /api/gates/{id}/approve", s.handleApprove)
	s.mux.HandleFunc("POST /api/gates/{id}/reject", s.handleReject)
	s.mux.HandleFunc("GET /api/actions", s.handleActions)
	s.mux.HandleFunc("GET /api/recipes", s.handleRecipes)
	s.mux.HandleFunc("GET /assets/", s.handleAsset)
	s.mux.HandleFunc("GET /", s.handleIndex)
}

// handleState is screen 2a in one request: what is waiting, and what ran.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	inbox, err := s.gates().LoadInbox(s.opts.WorkDir)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	runs, err := s.lister().ListRuns(ops.RunListOptions{Since: 7 * 24 * time.Hour})
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
	opt := ops.RunListOptions{Since: 7 * 24 * time.Hour}
	if raw := r.URL.Query().Get("since"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			fail(w, http.StatusBadRequest, fmt.Errorf("since: %w", err))
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

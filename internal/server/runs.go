package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/audemed44/keep/internal/runner"
)

func (s *Server) getOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.Runner.Overview(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	runs, err := s.Store.Runs(r.Context(), before, 50)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) startRun(w http.ResponseWriter, _ *http.Request) {
	id, err := s.Runner.Trigger("manual")
	if errors.Is(err, runner.ErrBusy) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "id": id})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) runLog(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	lines, err := s.Store.RunLog(r.Context(), id, after)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lines)
}

func (s *Server) sourceHistory(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.SourceSizes(r.Context(), r.PathValue("name"), 60)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

package server

import (
	"errors"
	"net/http"
	"os"
	"slices"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/runner"
)

// The settings (what to back up, how, how often) are edited in the UI:
// the page changes the whole config and saves it back.

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	c, err := s.Runner.Config()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": c, "roots": s.Runner.BrowseRoots(r.Context())})
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var c config.Config
	if !readJSON(w, r, 256<<10, &c) {
		return
	}
	// Paths must be ones Keep can see.
	roots := s.Runner.BrowseRoots(r.Context())
	visible := func(p string) bool {
		return slices.ContainsFunc(roots, func(rt string) bool { return runner.Within(p, rt) })
	}
	for _, rt := range c.Roots {
		if !visible(rt.Path) {
			writeError(w, http.StatusBadRequest, runner.ErrOutside{Path: rt.Path}.Error())
			return
		}
	}
	for _, src := range c.Sources {
		if src.Path != "" && !visible(src.Path) {
			writeError(w, http.StatusBadRequest, runner.ErrOutside{Path: src.Path}.Error())
			return
		}
		if src.Path != "" && !src.Skip {
			if info, err := os.Stat(src.Path); err != nil || !info.IsDir() {
				writeError(w, http.StatusBadRequest, src.Path+" isn't a folder Keep can read")
				return
			}
		}
	}
	saved, err := s.Runner.SaveConfig(r.Context(), c)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": saved, "roots": roots})
}

func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	l, err := s.Runner.Browse(r.Context(), r.URL.Query().Get("path"))
	var outside runner.ErrOutside
	switch {
	case errors.As(err, &outside):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such folder")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) suggestions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Runner.Suggestions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

package server

import (
	"net/http"
	"strings"

	"github.com/audemed44/skeleton/internal/store"
)

// Items are the template's example resource: a list, a save that both
// creates (POST) and updates (PUT /{id}), and a delete. Replace them with
// the app's own.

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.Items(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) saveItem(w http.ResponseWriter, r *http.Request) {
	var it store.Item
	if !readJSON(w, r, 64<<10, &it) {
		return
	}
	it.ID = 0
	if r.PathValue("id") != "" {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		it.ID = id
	}
	it.Title = strings.TrimSpace(it.Title)
	it.Note = strings.TrimSpace(it.Note)
	if it.Title == "" {
		writeError(w, http.StatusBadRequest, "a title is required")
		return
	}
	if err := s.Store.SaveItem(r.Context(), &it); err != nil {
		storeError(w, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	writeJSON(w, status, it)
}

func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteItem(r.Context(), id); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

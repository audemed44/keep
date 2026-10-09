package server

import (
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/audemed44/keep/internal/runner"
	"github.com/audemed44/keep/internal/store"
)

// Restores: pick a backup of a source (a restore point), restore all of
// it or a file or folder into the restores folder, then browse and
// download what came back. Nothing is ever restored in place.

func (s *Server) restorePoints(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Runner.Config()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sources, err := cfg.Resolve(os.ReadDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, src := range sources {
		if src.Name != r.PathValue("name") {
			continue
		}
		points, err := s.Runner.RestorePoints(r.Context(), cfg, src)
		if err != nil {
			storeError(w, err)
			return
		}
		listed, _ := s.Store.SnapshotsListed(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"points": points, "listed": listed})
		return
	}
	writeError(w, http.StatusNotFound, "no such source")
}

func (s *Server) startRestore(w http.ResponseWriter, r *http.Request) {
	var spec runner.RestoreSpec
	if !readJSON(w, r, 4<<10, &spec) {
		return
	}
	if (spec.Source == "") == (spec.Snapshot == "") {
		writeError(w, http.StatusBadRequest, "restore a source's backup (source and run) or one snapshot")
		return
	}
	if spec.Source != "" && spec.Run <= 0 {
		writeError(w, http.StatusBadRequest, "run: which backup to restore")
		return
	}
	if _, err := runner.CleanSub(spec.Path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.Runner.Start(runner.Job{Kind: store.KindRestore, Trigger: "manual", Restore: &spec})
	if errors.Is(err, runner.ErrBusy) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "id": id})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id})
}

func (s *Server) listRestores(w http.ResponseWriter, r *http.Request) {
	list, err := s.Runner.Restores(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restores": list, "keep_days": int(runner.RestoreKeep.Hours() / 24)})
}

func (s *Server) deleteRestore(w http.ResponseWriter, r *http.Request) {
	err := s.Runner.DeleteRestore(r.Context(), r.PathValue("name"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such restore")
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// inRestore resolves a path inside a restore folder, refusing anything
// that leads out of it, symlinks included.
func (s *Server) inRestore(w http.ResponseWriter, r *http.Request) (dir, rel, full string, ok bool) {
	dir, err := s.Runner.RestoreDir(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such restore")
		return "", "", "", false
	}
	rel, err = runner.CleanSub(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", "", "", false
	}
	realDir, err1 := filepath.EvalSymlinks(dir)
	full, err2 := filepath.EvalSymlinks(filepath.Join(dir, rel))
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusNotFound, "no such file or folder")
		return "", "", "", false
	}
	if !runner.Within(full, realDir) {
		writeError(w, http.StatusForbidden, "that leads out of the restore")
		return "", "", "", false
	}
	return dir, rel, full, true
}

type restoreEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"` // relative to the restore
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func (s *Server) browseRestore(w http.ResponseWriter, r *http.Request) {
	_, rel, full, ok := s.inRestore(w, r)
	if !ok {
		return
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		writeError(w, http.StatusBadRequest, "not a folder")
		return
	}
	out := []restoreEntry{}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || (!fi.IsDir() && !fi.Mode().IsRegular()) {
			continue // symlinks and devices aren't offered
		}
		out = append(out, restoreEntry{Name: e.Name(), Path: strings.TrimPrefix(rel+"/"+e.Name(), "/"),
			Dir: fi.IsDir(), Size: fi.Size(), Modified: fi.ModTime()})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Dir && !out[j].Dir })
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "entries": out})
}

func (s *Server) downloadRestore(w http.ResponseWriter, r *http.Request) {
	_, _, full, ok := s.inRestore(w, r)
	if !ok {
		return
	}
	f, err := os.Open(full)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		writeError(w, http.StatusBadRequest, "not a file")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fi.Name()}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(fi.Size()))
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// repository lists the snapshots of paths Keep doesn't back up now, from
// the last listing (the weekly verify).
func (s *Server) repository(w http.ResponseWriter, r *http.Request) {
	others, listed, err := s.Runner.OtherSources(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listed": listed, "others": others})
}

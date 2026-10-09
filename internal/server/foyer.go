package server

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/keep/internal/runner"
)

// Keep serves a card in the Foyer widget format
// (https://github.com/audemed44/foyer/blob/main/docs/app-widgets.md):
// the last run, how many sources are backed up, the repository size and
// the next run, with a Run now button. /api/foyer/backups feeds Foyer's
// topology map, which marks each data folder backed up or not.

type foyerStat struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	Unit    string `json:"unit,omitempty"`
	Caption string `json:"caption,omitempty"`
	Tone    string `json:"tone,omitempty"` // good, warn, bad or accent
}

type foyerAction struct {
	Label   string `json:"label"`
	URL     string `json:"url"`
	Confirm string `json:"confirm,omitempty"`
}

type foyerItem struct {
	Title    string       `json:"title"`
	Subtitle string       `json:"subtitle,omitempty"`
	Caption  string       `json:"caption,omitempty"`
	URL      string       `json:"url,omitempty"`
	Action   *foyerAction `json:"action,omitempty"`
}

type foyerWidget struct {
	Version     int         `json:"version"`
	Stats       []foyerStat `json:"stats"`
	ItemsTitle  string      `json:"items_title,omitempty"`
	ItemsLayout string      `json:"items_layout,omitempty"` // list or covers
	Items       []foyerItem `json:"items"`
}

var stateRank = map[string]int{
	runner.StateErrors: 0, runner.StateStale: 1, runner.StateNever: 2, runner.StateRunning: 3, runner.StateOK: 4,
}

func (s *Server) foyerWidget(w http.ResponseWriter, r *http.Request) {
	o, err := s.Runner.Overview(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	now := time.Now()
	out := foyerWidget{Version: 1, Items: []foyerItem{}, ItemsTitle: "Sources", ItemsLayout: "list"}

	last := foyerStat{Label: "Last backup", Value: "Never", Tone: "warn"}
	run := foyerItem{Title: "No backup yet", Action: &foyerAction{Label: "Run now", URL: "/api/foyer/run", Confirm: "Back up every source now?"}}
	switch {
	case o.Backing():
		last = foyerStat{Label: "Last backup", Value: "Running", Caption: o.Current, Tone: "accent"}
		run = foyerItem{Title: fmt.Sprintf("Run %d", o.Running), Subtitle: "Backing up " + o.Current, URL: fmt.Sprintf("/runs/%d", o.Running)}
	case o.LastRun != nil:
		lr := o.LastRun
		last = foyerStat{Label: "Last backup", Value: ago(now, lr.Started), Caption: statusWord(lr.Status), Tone: statusTone(lr.Status)}
		run.Title, run.Subtitle, run.URL = fmt.Sprintf("Run %d", lr.ID), lr.Summary, fmt.Sprintf("/runs/%d", lr.ID)
	}
	if o.ConfigError != "" {
		last = foyerStat{Label: "Config", Value: "Error", Caption: "keep.yml", Tone: "bad"}
		run.Subtitle = o.ConfigError
	}
	ok := 0
	for _, src := range o.Sources {
		if src.State == runner.StateOK || src.State == runner.StateRunning {
			ok++
		}
	}
	sources := foyerStat{Label: "Backed up", Value: strconv.Itoa(ok), Unit: "/" + strconv.Itoa(len(o.Sources)), Caption: "sources", Tone: "good"}
	if ok < len(o.Sources) {
		sources.Tone = "warn"
	}
	out.Stats = append(out.Stats, last, sources)
	if o.Repo != nil {
		out.Stats = append(out.Stats, foyerStat{Label: "Repository", Value: runner.Bytes(o.Repo.Size), Caption: o.Engine})
	}
	if o.Running == 0 && !o.Next.IsZero() && o.ConfigError == "" {
		label := "Next"
		if o.NextKind == "verify" {
			label = "Next check"
		}
		out.Stats = append(out.Stats, foyerStat{Label: label, Value: "in " + until(now, o.Next)})
	}

	out.Items = append(out.Items, run)
	list := append([]runner.SourceStatus(nil), o.Sources...)
	sort.SliceStable(list, func(i, j int) bool { return stateRank[list[i].State] < stateRank[list[j].State] })
	for _, src := range list {
		if len(out.Items) == 12 {
			break
		}
		it := foyerItem{Title: src.Name, Subtitle: stateText(src, now), URL: "/sources/" + src.Name}
		if src.Success != nil {
			it.Caption = runner.Bytes(src.Success.Size)
		}
		out.Items = append(out.Items, it)
	}
	writeJSON(w, http.StatusOK, out)
}

func stateText(src runner.SourceStatus, now time.Time) string {
	switch src.State {
	case runner.StateRunning:
		return "Backing up now"
	case runner.StateNever:
		return "Never backed up"
	case runner.StateErrors:
		if src.Latest != nil && src.Latest.Message != "" {
			return src.Latest.Message
		}
		return "The last backup failed"
	case runner.StateStale:
		return "Stale: last good backup " + ago(now, src.Success.Finished) + " ago"
	}
	return "Backed up " + ago(now, src.Success.Finished) + " ago"
}

func statusWord(status string) string {
	switch status {
	case "ok":
		return "ago, all good"
	case "warn":
		return "ago, with warnings"
	case "failed":
		return "ago, failed"
	}
	return "ago"
}

func statusTone(status string) string {
	switch status {
	case "ok":
		return "good"
	case "warn":
		return "warn"
	case "failed":
		return "bad"
	}
	return ""
}

// ago is a short age: 45s, 12m, 3h, 2d.
func ago(now, t time.Time) string { return short(now.Sub(t)) }

func until(now, t time.Time) string { return short(t.Sub(now)) }

func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// foyerRun is the card's Run now button.
func (s *Server) foyerRun(w http.ResponseWriter, _ *http.Request) {
	id, err := s.Runner.Trigger("foyer")
	msg := "Backing up…"
	if errors.Is(err, runner.ErrBusy) {
		msg = "Keep is busy with another job"
		if s.Runner.State().Backing() {
			msg = "A backup is already running"
		}
	}
	out := map[string]string{"message": msg}
	if id != 0 {
		out["url"] = fmt.Sprintf("/runs/%d", id)
		out["status_url"] = fmt.Sprintf("/api/foyer/runs/%d", id)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) foyerRunStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	state := "done"
	switch run.Status {
	case "running":
		state = "running"
	case "failed":
		state = "failed"
	}
	msg := run.Summary
	if run.Status == "running" {
		msg = "Busy with " + s.Runner.State().Current
		if run.Kind == "backup" {
			msg = "Backing up " + s.Runner.State().Current
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": state, "message": msg, "url": fmt.Sprintf("/runs/%d", id)})
}

// foyerBackup is one source as Foyer's topology map wants it: where its
// data is on the host (or the Docker volume a dump covers), and whether
// it's backed up.
type foyerBackup struct {
	Name     string     `json:"name"`
	Path     string     `json:"path,omitempty"`   // host path
	Volume   string     `json:"volume,omitempty"` // Docker volume
	Strategy string     `json:"strategy"`
	State    string     `json:"state"` // ok, running, stale, errors or never
	Last     *time.Time `json:"last,omitempty"`
	Size     int64      `json:"size"`
	Files    int64      `json:"files"`
	// Partial is true when part of the folder is left out; Excluded lists
	// those parts as host-path patterns (globs), so Foyer can mark what's
	// really missing rather than the whole folder.
	Partial  bool     `json:"partial,omitempty"`
	Excluded []string `json:"excluded,omitempty"`
}

// excludedPaths turns a source's anchored excludes (/library,
// /config/index-v2) into host-path patterns. Unanchored ones (*.log) match
// files anywhere, which a folder-level map can't show.
func excludedPaths(host string, patterns []string) []string {
	var out []string
	for _, p := range patterns {
		p = strings.TrimSuffix(p, "/")
		if host == "" || !strings.Contains(p, "/") {
			continue
		}
		out = append(out, path.Join(host, p))
	}
	return out
}

func (s *Server) foyerBackups(w http.ResponseWriter, r *http.Request) {
	o, err := s.Runner.Overview(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	if o.ConfigError != "" {
		writeError(w, http.StatusInternalServerError, "settings: "+o.ConfigError)
		return
	}
	stale, _ := time.ParseDuration(o.Stale)
	out := struct {
		Engine     string        `json:"engine"`
		StaleHours int           `json:"stale_hours"`
		Sources    []foyerBackup `json:"sources"`
	}{Engine: o.Engine, StaleHours: int(stale.Hours()), Sources: []foyerBackup{}}
	var global []string
	if cfg, err := s.Runner.Config(); err == nil {
		global = cfg.Excludes
	}
	for _, src := range o.Sources {
		excluded := excludedPaths(src.HostPath, slices.Concat(global, src.Excludes))
		b := foyerBackup{Name: src.Name, Path: src.HostPath, Volume: src.Volume, Strategy: src.Strategy, State: src.State,
			Partial: src.Partial || len(excluded) > 0, Excluded: excluded}
		if src.Success != nil {
			b.Last, b.Size, b.Files = timePtr(src.Success.Finished), src.Success.Size, src.Success.Files
		}
		out.Sources = append(out.Sources, b)
	}
	writeJSON(w, http.StatusOK, out)
}

func timePtr(t time.Time) *time.Time { return &t }

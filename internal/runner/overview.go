package runner

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/store"
)

// Source states, the same set Foyer uses for backups.
const (
	StateOK      = "ok"
	StateRunning = "running"
	StateStale   = "stale"  // the last good backup is older than stale_after
	StateErrors  = "errors" // the latest attempt failed or warned
	StateNever   = "never"
)

type SourceStatus struct {
	config.Source
	// HostPath is Path on the host (through Keep's mounts).
	HostPath string `json:"host_path,omitempty"`
	// Partial is true when the source leaves part of its folder out (its
	// own excludes).
	Partial bool             `json:"partial"`
	State   string           `json:"state"`
	Latest  *store.RunSource `json:"latest,omitempty"`
	Success *store.RunSource `json:"success,omitempty"`
}

type Overview struct {
	Engine  string         `json:"engine"`
	Every   string         `json:"every"`
	Stale   string         `json:"stale_after"`
	Sources []SourceStatus `json:"sources"`
	State
	LastRun *store.Run `json:"last_run,omitempty"`
	// LastVerify is the last repository check.
	LastVerify *store.Run `json:"last_verify,omitempty"`
	Repo       *RepoInfo  `json:"repo,omitempty"`
	// Local is the local repository's folder ("" for none), and LocalInfo
	// how the last run went there.
	Local     string     `json:"local,omitempty"`
	LocalInfo *LocalInfo `json:"local_info,omitempty"`
	// ConfigError is set when the settings can't be read or resolved
	// (a folder went missing).
	ConfigError string `json:"config_error,omitempty"`
}

// Overview is every source with its state, plus the schedule and the
// last run.
func (r *Runner) Overview(ctx context.Context) (Overview, error) {
	out := Overview{Sources: []SourceStatus{}, State: r.State()}
	if last, ok, err := r.Store.LastRun(ctx, store.KindBackup); err != nil {
		return out, err
	} else if ok {
		out.LastRun = &last
	}
	if last, ok, err := r.Store.LastRun(ctx, store.KindVerify); err != nil {
		return out, err
	} else if ok {
		out.LastVerify = &last
	}
	var repo RepoInfo
	if err := r.Store.Get(ctx, "repo", &repo); err != nil {
		return out, err
	}
	if !repo.At.IsZero() {
		out.Repo = &repo
	}
	hist, err := r.Store.SourceHistories(ctx)
	if err != nil {
		return out, err
	}

	cfg, err := r.Config()
	if err != nil {
		out.ConfigError = err.Error()
		return out, nil
	}
	out.Engine, out.Every, out.Stale = cfg.Engine.Type, cfg.Every.String(), cfg.StaleAfter.String()
	out.Local = cfg.Local.Path
	var li LocalInfo
	if err := r.Store.Get(ctx, "local", &li); err == nil && !li.At.IsZero() && out.Local != "" {
		out.LocalInfo = &li
	}
	sources, err := cfg.Resolve(os.ReadDir)
	if err != nil {
		out.ConfigError = err.Error()
		return out, nil
	}
	now := time.Now()
	hostPath := r.HostPaths(ctx)
	for _, s := range sources {
		h := hist[s.Name]
		st := SourceStatus{Source: s, Latest: h.Latest, Success: h.Success, Partial: len(s.Excludes) > 0}
		if s.Path != "" {
			st.HostPath = hostPath(s.Path)
		}
		st.State = state(h, out.Backing() && out.Current == s.Name, now, cfg.StaleAfter.D())
		out.Sources = append(out.Sources, st)
	}
	return out, nil
}

func state(h store.SourceHistory, running bool, now time.Time, staleAfter time.Duration) string {
	switch {
	case running:
		return StateRunning
	case h.Success == nil:
		if h.Latest != nil && h.Latest.Status == "failed" {
			return StateErrors
		}
		return StateNever
	case now.Sub(h.Success.Finished) > staleAfter:
		return StateStale
	case h.Latest != nil && slices.Contains([]string{"failed", "warn"}, h.Latest.Status):
		return StateErrors
	}
	return StateOK
}

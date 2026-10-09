package runner

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"time"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/engine"
)

// Other snapshots are the repository's snapshots of paths that aren't
// Keep's sources now: taken before Keep, by hand in Kopia, or of sources
// since removed. They don't expire on their own (nothing snapshots those
// paths any more), so a retire date deletes them.

// OtherSource is one such path.
type OtherSource struct {
	Path      string    `json:"path"`
	Snapshots int       `json:"snapshots"`
	Oldest    time.Time `json:"oldest"`
	Newest    time.Time `json:"newest"`
	Latest    string    `json:"latest"` // the newest snapshot's id
	Size      int64     `json:"size"`   // of the newest
	Files     int64     `json:"files"`
	// RetireAfter is the date its snapshots are deleted ("" for never).
	RetireAfter string `json:"retire_after,omitempty"`
}

// keepPaths are the paths Keep snapshots now: each source's folder and
// its staging.
func keepPaths(cfg config.Config) map[string]bool {
	out := map[string]bool{}
	sources, _ := cfg.Resolve(os.ReadDir)
	for _, s := range sources {
		if s.Path != "" {
			out[s.Path] = true
		}
		out[path.Join(cfg.Staging, s.Name)] = true
	}
	return out
}

// OtherSources lists the paths in the repository listing that Keep
// doesn't snapshot now.
func (r *Runner) OtherSources(ctx context.Context) ([]OtherSource, time.Time, error) {
	cfg, err := r.Config()
	if err != nil {
		return nil, time.Time{}, err
	}
	listed, err := r.Store.SnapshotsListed(ctx)
	if err != nil {
		return nil, listed, err
	}
	snaps, err := r.Store.Snapshots(ctx) // newest first
	if err != nil {
		return nil, listed, err
	}
	ours := keepPaths(cfg)
	retire := map[string]string{}
	for _, rt := range cfg.Retire {
		retire[rt.Path] = rt.After
	}
	byPath := map[string]*OtherSource{}
	var order []string
	for _, s := range snaps {
		if ours[s.Path] {
			continue
		}
		o := byPath[s.Path]
		if o == nil {
			o = &OtherSource{Path: s.Path, Newest: s.Start, Latest: s.ID, Size: s.Size, Files: s.Files, RetireAfter: retire[s.Path]}
			byPath[s.Path] = o
			order = append(order, s.Path)
		}
		o.Snapshots++
		o.Oldest = s.Start
	}
	out := make([]OtherSource, 0, len(order))
	for _, p := range order {
		out = append(out, *byPath[p])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, listed, nil
}

// retire deletes the snapshots of paths whose retire date has come, from
// a fresh listing, and drops those entries from the settings. The error
// is for anything that needs a look (the run ends with warnings).
func (r *Runner) retire(ctx context.Context, id int64, eng engine.Engine, cfg config.Config) error {
	ours := keepPaths(cfg)
	var keep []config.Retire
	changed := false
	var failed error
	for _, rt := range cfg.Retire {
		switch {
		case !rt.Due(time.Now()):
			keep = append(keep, rt)
			continue
		case ours[rt.Path]:
			r.log(ctx, id, "warn", "Not deleting the snapshots of %s: Keep backs it up now. Remove its retire date.", rt.Path)
			keep = append(keep, rt)
			failed = fmt.Errorf("%s is a source", rt.Path)
			continue
		}
		snaps, err := r.Store.Snapshots(ctx, rt.Path)
		if err != nil {
			return err
		}
		ids := make([]string, len(snaps))
		for i, s := range snaps {
			ids[i] = s.ID
		}
		r.setCurrent("deleting old snapshots of " + rt.Path)
		if err := eng.Delete(ctx, ids); err != nil {
			r.log(ctx, id, "error", "Deleting the snapshots of %s: %v", rt.Path, err)
			keep = append(keep, rt)
			failed = err
			continue
		}
		if err := r.Store.DeleteSnapshots(ctx, ids); err != nil {
			return err
		}
		r.log(ctx, id, "info", "Deleted %s of %s (retire date %s)", plural(len(ids), "snapshot"), rt.Path, rt.After)
		changed = true
	}
	if changed {
		cfg.Retire = keep
		if _, err := r.SaveConfig(ctx, cfg); err != nil {
			r.log(ctx, id, "warn", "Removing the done retire dates from the settings: %v", err)
		}
	}
	return failed
}

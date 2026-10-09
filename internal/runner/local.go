package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/engine"
	"github.com/audemed44/keep/internal/store"
)

// The local repository is a second, separate repository in a folder on
// this server (another disk). Every run snapshots each source into it
// too, from the same prepared files, after the main repository. It
// doesn't depend on the main one or the network, and restores from it are
// fast. A failure there never fails a source: it's recorded next to it,
// makes the run a warning, and goes to the local heartbeat.

// repoMarker is the file every Kopia repository has at its top. A folder
// that holds files but not this one isn't a repository, and Keep won't
// put one there.
const repoMarker = "kopia.repository.f"

// LocalInfo is the local repository after the last run.
type LocalInfo struct {
	Size  int64     `json:"size"`
	At    time.Time `json:"at"`
	OK    int       `json:"ok"`    // sources in it from that run
	Total int       `json:"total"` // sources in that run
}

// openLocal checks the local repository's folder and connects to it,
// creating the repository the first time.
func (r *Runner) openLocal(ctx context.Context, id int64, cfg config.Config, pc pathCheck) (engine.Local, error) {
	dir := cfg.Local.Path
	if err := pc.writable(dir); err != nil {
		return nil, err
	}
	empty, err := checkRepoDir(dir)
	if err != nil {
		return nil, err
	}
	loc := r.Local(cfg)
	if err := loc.Open(ctx, dir, empty); err != nil {
		return nil, fmt.Errorf("opening the repository in %s: %w", dir, err)
	}
	if empty {
		r.log(ctx, id, "info", "Created the local repository in %s", dir)
	}
	return loc, nil
}

// checkRepoDir creates the folder, or checks it's empty or a repository
// already. empty is true when there's no repository in it yet.
func checkRepoDir(dir string) (empty bool, err error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return false, fmt.Errorf("creating %s: %w", dir, err)
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", dir, err)
	}
	if len(entries) == 0 {
		return true, nil
	}
	if _, err := os.Stat(filepath.Join(dir, repoMarker)); err != nil {
		return false, fmt.Errorf("%s holds files but isn't a repository: pick an empty folder", dir)
	}
	return false, nil
}

// localSnapshot sets the jobs' policies in the local repository and takes
// all their snapshots there in one engine call.
func (r *Runner) localSnapshot(ctx context.Context, runID int64, loc engine.Local, jobs []*job) {
	skip := func(j *job) bool { return !j.ready || j.localDone }
	r.configure(ctx, loc, "local-policy:", jobs, skip, func(j *job, msg string) { r.localFail(ctx, j, "%s", msg) })
	var paths []string
	for _, j := range jobs {
		if skip(j) {
			continue
		}
		for _, t := range j.snaps {
			paths = append(paths, t.path)
		}
	}
	if len(paths) == 0 {
		return
	}
	started := time.Now()
	got, err := loc.Snapshot(ctx, paths, fmt.Sprintf("Keep run %d", runID))
	if err != nil {
		r.log(ctx, runID, "warn", "Local copy: %v", err)
	}
	r.log(ctx, runID, "info", "Local snapshots of %d paths took %s", len(paths), time.Since(started).Round(time.Second))
	for _, j := range jobs {
		if skip(j) {
			continue
		}
		var taken []store.LocalSnapshot
		for _, t := range j.snaps {
			snap, ok := got[t.path]
			if !ok {
				r.localFail(ctx, j, "no snapshot of %s: %s", t.path, errorFor(err, t.path))
				break
			}
			taken = append(taken, store.LocalSnapshot{ID: snap.ID, Path: t.path})
		}
		if j.localDone {
			continue
		}
		j.rs.Local, j.localDone = taken, true
		_ = r.Store.SaveRunSource(context.WithoutCancel(ctx), j.rs)
	}
}

func (r *Runner) localFail(ctx context.Context, j *job, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.log(ctx, j.rs.RunID, "warn", "%s: local copy: %s", j.src.Name, msg)
	j.rs.LocalError, j.localDone = msg, true
	_ = r.Store.SaveRunSource(context.WithoutCancel(ctx), j.rs)
}

// finishLocal records how the local copy went, pings its heartbeat and
// returns the sources it's missing.
func (r *Runner) finishLocal(ctx context.Context, id int64, cfg config.Config, loc engine.Local, jobs []*job) []string {
	var missing []string
	for _, j := range jobs {
		if len(j.rs.Local) == 0 {
			missing = append(missing, j.src.Name)
			if j.rs.LocalError == "" {
				// Never got that far: it failed before its snapshots.
				j.rs.LocalError = "not prepared"
				_ = r.Store.SaveRunSource(context.WithoutCancel(ctx), j.rs)
			}
		}
	}
	info := LocalInfo{At: time.Now(), OK: len(jobs) - len(missing), Total: len(jobs)}
	var prev LocalInfo
	_ = r.Store.Get(ctx, "local", &prev)
	info.Size = prev.Size
	if loc != nil {
		r.setCurrent("the local repository size")
		if st, err := loc.Stats(ctx); err != nil {
			r.log(ctx, id, "warn", "Reading the local repository size: %v", err)
		} else {
			info.Size = st.Size
		}
	}
	if err := r.Store.Put(ctx, "local", info); err != nil {
		slog.Warn("saving the local repository's state", "err", err)
	}
	msg := fmt.Sprintf("%d of %d sources in the local copy, %s", info.OK, info.Total, Bytes(info.Size))
	if len(missing) > 0 {
		msg += "; missing: " + strings.Join(missing, ", ")
		r.ping(context.WithoutCancel(ctx), cfg.Local.Heartbeat, "/fail", msg)
	} else {
		r.ping(context.WithoutCancel(ctx), cfg.Local.Heartbeat, "", msg)
	}
	r.log(ctx, id, levelIf(len(missing) > 0), "%s", msg)
	return missing
}

func levelIf(warn bool) string {
	if warn {
		return "warn"
	}
	return "info"
}

// localEngine opens the local repository for a verify or a restore; nil
// when there's none.
func (r *Runner) localEngine(ctx context.Context, id int64, cfg config.Config) (engine.Local, error) {
	if cfg.Local.Path == "" {
		return nil, nil
	}
	return r.openLocal(ctx, id, cfg, r.pathCheck(ctx, cfg))
}

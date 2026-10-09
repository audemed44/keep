package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/engine"
	"github.com/audemed44/keep/internal/store"
)

// Restores copy a backup out of the repository into the restores folder,
// never in place: moving files back into an app stays a manual step. Each
// restore is a folder of its own, laid out like the source, and is deleted
// after RestoreKeep.

// RestoreKeep is how long a restore stays before Keep deletes it.
const RestoreKeep = 7 * 24 * time.Hour

// RestoreSpec is what to restore: a Keep source as one of its backups
// (Run), or a single snapshot by id (one Keep doesn't manage), and
// optionally only a file or folder inside it.
type RestoreSpec struct {
	Source   string `json:"source,omitempty"`
	Run      int64  `json:"run,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
	Path     string `json:"path,omitempty"` // relative to the source
}

// RestoreInfo describes a restore folder.
type RestoreInfo struct {
	Name     string    `json:"name"`
	Dir      string    `json:"dir"`
	Label    string    `json:"label"` // the source, or the snapshot's path
	Run      int64     `json:"run,omitempty"`
	Taken    time.Time `json:"taken"` // when the backup was taken
	Path     string    `json:"path,omitempty"`
	RunID    int64     `json:"run_id"` // the restore job
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
	Size     int64     `json:"size"`
	Files    int64     `json:"files"`
	Complete bool      `json:"complete"`
}

var safeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// CleanSub checks a path inside a source: relative, no "..".
func CleanSub(p string) (string, error) {
	p = strings.TrimSpace(p)
	if slices.Contains(strings.Split(p, "/"), "..") {
		return "", errors.New("the path can't go up with ..")
	}
	return strings.Trim(path.Clean("/"+p), "/"), nil
}

// part is one snapshot to restore into the restore folder.
type part struct {
	snap   store.Snapshot
	staged bool // database copies and dumps
}

func (r *Runner) restore(ctx context.Context, id int64, spec *RestoreSpec) (status, summary string) {
	if spec == nil {
		return "failed", "nothing to restore"
	}
	cfg, err := r.Config()
	if err != nil {
		r.log(ctx, id, "error", "Reading the settings: %v", err)
		return "failed", "settings: " + err.Error()
	}
	sub, err := CleanSub(spec.Path)
	if err != nil {
		r.log(ctx, id, "error", "%v", err)
		return "failed", err.Error()
	}
	eng := r.Engine(cfg)
	pc := r.pathCheck(ctx, cfg)
	if err := os.MkdirAll(cfg.Restores, 0o755); err != nil {
		r.log(ctx, id, "error", "Creating %s: %v", cfg.Restores, err)
		return "failed", err.Error()
	}
	if err := pc.writable(cfg.Restores); err != nil {
		r.log(ctx, id, "error", "%v", err)
		return "failed", err.Error()
	}

	// A fresh listing: the weekly one may predate the backup, or retention
	// may have removed it since.
	if err := r.listSnapshots(ctx, id, eng); err != nil {
		return "failed", "listing snapshots: " + firstLine(err.Error())
	}
	parts, label, err := r.restoreParts(ctx, cfg, spec)
	if err != nil {
		r.log(ctx, id, "error", "%v", err)
		return "failed", err.Error()
	}
	taken := parts[0].snap.Start
	name := fmt.Sprintf("%s-%s-%d", safeName.ReplaceAllString(label, "_"), taken.Local().Format("20060102-1504"), id)
	name = strings.Trim(name, "_")
	dir := path.Join(cfg.Restores, name)
	info := RestoreInfo{Name: name, Dir: dir, Label: label, Run: spec.Run, Taken: taken, Path: sub, RunID: id, Created: time.Now()}
	if err := r.Store.Put(ctx, "restore:"+name, info); err != nil {
		slog.Warn("saving a restore", "err", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.log(ctx, id, "error", "%v", err)
		return "failed", err.Error()
	}

	what := label
	if sub != "" {
		what += "/" + sub
	}
	r.log(ctx, id, "info", "Restoring %s as of %s into %s", what, taken.Local().Format("2 Jan 2006 15:04"), dir)
	restored := 0
	var failed error
	for _, p := range parts {
		target := dir
		if sub != "" {
			target = path.Join(dir, sub)
			if err := os.MkdirAll(path.Dir(target), 0o755); err != nil {
				failed = err
				break
			}
		}
		r.setCurrent(fmt.Sprintf("restoring %s", p.snap.Path))
		started := time.Now()
		err := eng.Restore(ctx, p.snap.ID, sub, target)
		if errors.Is(err, engine.ErrNotInSnapshot) {
			// A file or folder is in the source's snapshot or in its
			// database copies, rarely both.
			r.log(ctx, id, "info", "%s isn't in the snapshot of %s", sub, p.snap.Path)
			continue
		}
		if err != nil {
			failed = err
			break
		}
		restored++
		r.log(ctx, id, "info", "Restored the snapshot of %s in %s", p.snap.Path, time.Since(started).Round(time.Second))
	}

	// Kopia runs as root: hand the files to Keep, so they can be read,
	// downloaded and deleted.
	r.setCurrent("handing the files to Keep")
	owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if err := r.Docker.Exec(ctx, cfg.Engine.Container, []string{"chown", "-R", owner, dir}, nil); err != nil {
		r.log(ctx, id, "warn", "Changing the files' owner to Keep: %v", err)
	}
	info.Files, info.Size = folderSize(dir)
	info.Complete = failed == nil && restored > 0
	info.Expires = info.Created.Add(RestoreKeep)
	if err := r.Store.Put(context.WithoutCancel(ctx), "restore:"+name, info); err != nil {
		slog.Warn("saving a restore", "err", err)
	}
	switch {
	case failed != nil:
		r.log(ctx, id, "error", "%v", failed)
		return "failed", fmt.Sprintf("%s: %s", what, firstLine(failed.Error()))
	case restored == 0:
		// Nothing came back: no empty folder to clutter Restores.
		if err := r.DeleteRestore(context.WithoutCancel(ctx), name); err != nil {
			slog.Warn("deleting an empty restore", "err", err)
		}
		r.log(ctx, id, "error", "%s isn't in the backup", sub)
		return "failed", fmt.Sprintf("%s isn't in the backup from %s", what, taken.Local().Format("2 Jan 15:04"))
	}
	return "ok", fmt.Sprintf("Restored %s: %s, %s, into %s", what, plural(int(info.Files), "file"), Bytes(info.Size), name)
}

// restoreParts finds the snapshots to restore and a label for the folder.
func (r *Runner) restoreParts(ctx context.Context, cfg config.Config, spec *RestoreSpec) ([]part, string, error) {
	if spec.Snapshot != "" {
		all, err := r.Store.Snapshots(ctx)
		if err != nil {
			return nil, "", err
		}
		for _, s := range all {
			if s.ID == spec.Snapshot {
				return []part{{snap: s}}, path.Base(s.Path), nil
			}
		}
		return nil, "", fmt.Errorf("snapshot %s isn't in the repository (any more)", spec.Snapshot)
	}
	sources, err := cfg.Resolve(os.ReadDir)
	if err != nil {
		return nil, "", err
	}
	var src *config.Source
	for i := range sources {
		if sources[i].Name == spec.Source {
			src = &sources[i]
		}
	}
	if src == nil {
		return nil, "", fmt.Errorf("no source called %q", spec.Source)
	}
	points, err := r.RestorePoints(ctx, cfg, *src)
	if err != nil {
		return nil, "", err
	}
	for _, p := range points {
		if p.Run == spec.Run && len(p.parts) > 0 {
			return p.parts, src.Name, nil
		}
	}
	return nil, "", fmt.Errorf("no snapshot of %s from run %d is left in the repository", src.Name, spec.Run)
}

// RestorePoint is one backup of a source that can be restored.
type RestorePoint struct {
	Run   int64     `json:"run"`
	Taken time.Time `json:"taken"`
	Size  int64     `json:"size"`
	Files int64     `json:"files"`
	parts []part
}

var keepRun = regexp.MustCompile(`^Keep run (\d+)$`)

// RestorePoints lists a source's backups, newest first: the snapshots of
// its path and of its staging in the repository listing, grouped by the
// run that took them, plus good runs since the listing (a restore lists
// again, so it finds their snapshots).
func (r *Runner) RestorePoints(ctx context.Context, cfg config.Config, src config.Source) ([]RestorePoint, error) {
	stage := path.Join(cfg.Staging, src.Name)
	var paths []string
	if src.Path != "" {
		paths = append(paths, src.Path)
	}
	paths = append(paths, stage)
	snaps, err := r.Store.Snapshots(ctx, paths...)
	if err != nil {
		return nil, err
	}
	byRun := map[int64]*RestorePoint{}
	for _, s := range snaps {
		m := keepRun.FindStringSubmatch(s.Description)
		if m == nil {
			continue // not taken by Keep
		}
		var run int64
		fmt.Sscan(m[1], &run)
		p := byRun[run]
		if p == nil {
			p = &RestorePoint{Run: run, Taken: s.Start}
			byRun[run] = p
		}
		p.Size += s.Size
		p.Files += s.Files
		p.parts = append(p.parts, part{snap: s, staged: s.Path == stage})
		if s.Start.Before(p.Taken) {
			p.Taken = s.Start
		}
	}
	listed, err := r.Store.SnapshotsListed(ctx)
	if err != nil {
		return nil, err
	}
	recent, err := r.Store.SourceSizes(ctx, src.Name, 400)
	if err != nil {
		return nil, err
	}
	for _, rs := range recent {
		if _, ok := byRun[rs.RunID]; !ok && rs.Finished.After(listed) {
			byRun[rs.RunID] = &RestorePoint{Run: rs.RunID, Taken: rs.Started, Size: rs.Size, Files: rs.Files}
		}
	}
	out := make([]RestorePoint, 0, len(byRun))
	for _, p := range byRun {
		// The source's own files first, then the database copies over them.
		sort.SliceStable(p.parts, func(i, j int) bool { return !p.parts[i].staged && p.parts[j].staged })
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Run > out[j].Run })
	return out, nil
}

// Restores lists the restore folders, newest first.
func (r *Runner) Restores(ctx context.Context) ([]RestoreInfo, error) {
	cfg, err := r.Config()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(cfg.Restores)
	if errors.Is(err, fs.ErrNotExist) {
		return []RestoreInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []RestoreInfo{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := RestoreInfo{Name: e.Name(), Dir: path.Join(cfg.Restores, e.Name()), Label: e.Name()}
		if err := r.Store.Get(ctx, "restore:"+e.Name(), &info); err != nil {
			return nil, err
		}
		if info.Created.IsZero() {
			if fi, err := e.Info(); err == nil {
				info.Created = fi.ModTime()
			}
			info.Expires = info.Created.Add(RestoreKeep)
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// RestoreDir is the folder of the restore called name.
func (r *Runner) RestoreDir(name string) (string, error) {
	cfg, err := r.Config()
	if err != nil {
		return "", err
	}
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return "", fs.ErrNotExist
	}
	dir := path.Join(cfg.Restores, name)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fs.ErrNotExist
	}
	return dir, nil
}

// DeleteRestore deletes a restore folder. Files Keep couldn't take over
// from the engine are deleted from inside its container.
func (r *Runner) DeleteRestore(ctx context.Context, name string) error {
	dir, err := r.RestoreDir(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		cfg, cerr := r.Config()
		if cerr != nil {
			return err
		}
		if xerr := r.Docker.Exec(ctx, cfg.Engine.Container, []string{"rm", "-rf", "--", dir}, nil); xerr != nil {
			return fmt.Errorf("%v; and from the engine's container: %v", err, xerr)
		}
	}
	return r.Store.Delete(ctx, "restore:"+name)
}

// cleanRestores deletes restores older than RestoreKeep. A restore whose
// job is still running is left alone.
func (r *Runner) cleanRestores(ctx context.Context) {
	list, err := r.Restores(ctx)
	if err != nil {
		slog.Warn("listing restores", "err", err)
		return
	}
	running := r.State().Running
	for _, info := range list {
		if time.Now().Before(info.Expires) || (info.RunID != 0 && info.RunID == running) {
			continue
		}
		if err := r.DeleteRestore(ctx, info.Name); err != nil {
			slog.Warn("deleting an old restore", "name", info.Name, "err", err)
		} else {
			slog.Info("deleted an old restore", "name", info.Name)
		}
	}
}

func folderSize(dir string) (files, size int64) {
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				files++
				size += fi.Size()
			}
		}
		return nil
	})
	return files, size
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

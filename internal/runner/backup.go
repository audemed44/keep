package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/engine"
	"github.com/audemed44/keep/internal/prepare"
	"github.com/audemed44/keep/internal/store"
)

// A run goes in phases, because every engine call opens the repository
// (20-50 s over rclone): prepare every source into staging, set the
// policies that changed, take every snapshot in one engine call, then
// record each source. Sources with the stop strategy go one at a time
// afterwards, so their containers are down only for their own snapshot.

// job is one source on its way through a run.
type job struct {
	src   config.Source
	rs    store.RunSource
	stage string
	snaps []target // what to snapshot
	warns []string
	done  bool // failed already, or finished
}

// target is a path to snapshot and its policy.
type target struct {
	path   string
	policy engine.Policy
}

func (r *Runner) run(ctx context.Context, id int64) (status, summary string) {
	cfg, err := r.Config()
	if err != nil {
		r.log(ctx, id, "error", "Reading the settings: %v", err)
		return "failed", "settings: " + err.Error()
	}
	sources, err := cfg.Resolve(os.ReadDir)
	if err != nil {
		r.log(ctx, id, "error", "Finding sources: %v", err)
		return "failed", err.Error()
	}
	if len(sources) == 0 {
		r.log(ctx, id, "error", "No sources: add folders to back up")
		return "failed", "nothing to back up"
	}
	eng := r.Engine(cfg)
	r.log(ctx, id, "info", "Backing up %d sources with %s", len(sources), eng.Name())
	pc := r.pathCheck(ctx, cfg)
	ret := engine.Retention(cfg.Retention)

	var jobs, stops []*job
	for _, s := range sources {
		j := &job{src: s, stage: path.Join(cfg.Staging, s.Name),
			rs: store.RunSource{RunID: id, Name: s.Name, Strategy: s.Strategy, Status: "running", Started: time.Now()}}
		_ = r.Store.SaveRunSource(ctx, j.rs)
		jobs = append(jobs, j)
	}

	// 1. Prepare.
	var batch []*job
	for _, j := range jobs {
		if ctx.Err() != nil {
			r.fail(ctx, j, "Keep stopped during the run")
			continue
		}
		r.setCurrent(j.src.Name)
		r.prepare(ctx, cfg, pc, ret, j)
		if j.done {
			continue
		}
		if j.src.Strategy == config.Stop {
			stops = append(stops, j)
		} else {
			batch = append(batch, j)
		}
	}

	// 2. Policies, 3. one snapshot for the batch.
	r.setCurrent(fmt.Sprintf("%d sources", len(batch)))
	r.snapshot(ctx, id, eng, batch)

	// The stop strategy: one source at a time.
	for _, j := range stops {
		r.setCurrent(j.src.Name)
		names := splitList(j.src.Container)
		stopped, err := r.stop(ctx, id, j.src.Name, names)
		if err != nil {
			r.start(context.WithoutCancel(ctx), id, j.src.Name, stopped)
			r.fail(ctx, j, "stopping %s: %v", strings.Join(names, ", "), err)
			continue
		}
		r.snapshot(ctx, id, eng, []*job{j})
		r.start(context.WithoutCancel(ctx), id, j.src.Name, stopped)
	}

	var failed, warned []string
	var size int64
	for _, j := range jobs {
		os.RemoveAll(j.stage)
		size += j.rs.Size
		switch j.rs.Status {
		case "failed":
			failed = append(failed, j.src.Name)
		case "warn":
			warned = append(warned, j.src.Name)
		}
	}

	r.setCurrent("the repository size")
	if st, err := eng.Stats(ctx); err != nil {
		r.log(ctx, id, "warn", "Reading the repository size: %v", err)
	} else if err := r.Store.Put(ctx, "repo", RepoInfo{Size: st.Size, At: time.Now()}); err != nil {
		slog.Warn("saving the repository size", "err", err)
	}

	ok := len(sources) - len(failed) - len(warned)
	summary = fmt.Sprintf("%d of %d sources backed up, %s", ok+len(warned), len(sources), Bytes(size))
	switch {
	case len(failed) > 0:
		return "failed", summary + "; failed: " + strings.Join(failed, ", ")
	case len(warned) > 0:
		return "warn", summary + "; warnings: " + strings.Join(warned, ", ")
	}
	return "ok", summary
}

func (r *Runner) setCurrent(what string) {
	r.mu.Lock()
	r.current = what
	r.mu.Unlock()
}

// prepare makes a source consistent in staging and lists its targets.
func (r *Runner) prepare(ctx context.Context, cfg config.Config, pc pathCheck, ret engine.Retention, j *job) {
	s := j.src
	if s.Path != "" {
		if err := pc.check(s.Path); err != nil {
			r.fail(ctx, j, "%v", err)
			return
		}
		if info, err := os.Stat(s.Path); err != nil || !info.IsDir() {
			r.fail(ctx, j, "%s isn't a folder Keep can read", s.Path)
			return
		}
	}
	if err := os.RemoveAll(j.stage); err != nil {
		r.fail(ctx, j, "clearing staging: %v", err)
		return
	}

	ignores := slices.Concat(cfg.Excludes, s.Excludes)
	if s.Path != "" && within(cfg.Staging, s.Path) {
		ignores = append(ignores, config.Literal(rel(s.Path, cfg.Staging)))
	}
	switch s.Strategy {
	case config.SQLite:
		dbs, err := prepare.FindSQLite(s.Path, ignores, cfg.Staging)
		if err != nil {
			r.fail(ctx, j, "looking for databases: %v", err)
			return
		}
		for _, db := range dbs {
			if err := prepare.CopySQLite(ctx, filepath.Join(s.Path, db), filepath.Join(j.stage, db)); err != nil {
				r.warn(ctx, j, "%s couldn't be copied (%v); the live file is in the snapshot instead", db, err)
				continue
			}
			j.rs.Databases++
			for _, f := range prepare.LiveFiles(db) {
				ignores = append(ignores, config.Literal(f))
			}
		}
		if len(dbs) > 0 {
			r.log(ctx, j.rs.RunID, "info", "%s: copied %d of %d SQLite databases", s.Name, j.rs.Databases, len(dbs))
		}
	case config.Postgres, config.MariaDB:
		n, err := prepare.Dump(ctx, r.Docker, s, filepath.Join(j.stage, prepare.DumpName(s)))
		if err != nil {
			r.fail(ctx, j, "%v", err)
			return
		}
		j.rs.Databases = 1
		r.log(ctx, j.rs.RunID, "info", "%s: dumped %s from %s", s.Name, Bytes(n), s.Container)
	}

	if s.Path != "" {
		j.snaps = append(j.snaps, target{s.Path, engine.Policy{Ignores: dedupe(ignores), Retention: ret}})
	}
	if staged, _ := hasFiles(j.stage); staged {
		if err := pc.check(j.stage); err != nil {
			r.fail(ctx, j, "staging: %v", err)
			return
		}
		j.snaps = append(j.snaps, target{j.stage, engine.Policy{Retention: ret}})
	}
	if len(j.snaps) == 0 {
		r.fail(ctx, j, "nothing to snapshot")
	}
}

// snapshot sets the jobs' policies and takes all their snapshots in one
// engine call, then records each job.
func (r *Runner) snapshot(ctx context.Context, runID int64, eng engine.Engine, jobs []*job) {
	var paths []string
	owner := map[string]*job{}
	r.configure(ctx, eng, jobs)
	for _, j := range jobs {
		if j.done {
			continue
		}
		for _, t := range j.snaps {
			paths = append(paths, t.path)
			owner[t.path] = j
		}
	}
	if len(paths) == 0 {
		return
	}
	started := time.Now()
	got, err := eng.Snapshot(ctx, paths, fmt.Sprintf("Keep run %d", runID))
	if err != nil {
		r.log(ctx, runID, "warn", "%v", err)
	}
	r.log(ctx, runID, "info", "Snapshots of %d paths took %s", len(paths), time.Since(started).Round(time.Second))
	for _, j := range jobs {
		if j.done {
			continue
		}
		for _, t := range j.snaps {
			snap, ok := got[t.path]
			if !ok {
				r.fail(ctx, j, "no snapshot of %s: %s", t.path, errorFor(err, t.path))
				break
			}
			j.rs.Size += snap.Size
			j.rs.Files += snap.Files
			j.rs.Snapshots = append(j.rs.Snapshots, snap.ID)
			if snap.Errors > 0 {
				r.warn(ctx, j, "%d files in %s couldn't be read", snap.Errors, t.path)
			}
		}
		if j.done {
			continue
		}
		j.rs.Status, j.rs.Finished, j.done = "ok", time.Now(), true
		if len(j.warns) > 0 {
			j.rs.Status, j.rs.Message = "warn", strings.Join(j.warns, "; ")
		}
		r.log(ctx, runID, "info", "%s: %s in %d files", j.src.Name, Bytes(j.rs.Size), j.rs.Files)
		_ = r.Store.SaveRunSource(context.WithoutCancel(ctx), j.rs)
	}
}

// errorFor picks the lines of an engine error that name path.
func errorFor(err error, p string) string {
	if err == nil {
		return "the engine didn't say why"
	}
	var lines []string
	for _, l := range strings.Split(err.Error(), "\n") {
		if strings.Contains(l, p) {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) == 0 {
		return strings.SplitN(err.Error(), "\n", 2)[0]
	}
	return strings.Join(lines, "; ")
}

// policyRecheck is how long a policy Keep set is trusted before Keep
// reads it from the engine again (someone may have changed it there). Each
// path adds up to six days on top, from a hash of its path, so paths set
// in the same run come due on different days rather than all at once.
const policyRecheck = 7 * 24 * time.Hour

func recheckAfter(p string) time.Duration {
	h := fnv.New32a()
	h.Write([]byte(p))
	return policyRecheck + time.Duration(h.Sum32()%7)*24*time.Hour
}

type policyMemo struct {
	Hash string    `json:"hash"`
	At   time.Time `json:"at"`
}

// configure sets the policies of the jobs' targets, skipping those Keep
// set the same way recently. The rest are read in one engine call and set
// one by one where they differ: every call opens the repository.
func (r *Runner) configure(ctx context.Context, eng engine.Engine, jobs []*job) {
	type due struct {
		t    target
		j    *job
		hash string
	}
	var list []due
	for _, j := range jobs {
		if j.done {
			continue
		}
		for _, t := range j.snaps {
			raw, _ := json.Marshal(t.policy)
			sum := sha256.Sum256(append([]byte(eng.Name()+"\x00"), raw...))
			hash := hex.EncodeToString(sum[:])
			var memo policyMemo
			if err := r.Store.Get(ctx, "policy:"+t.path, &memo); err == nil && memo.Hash == hash && time.Since(memo.At) < recheckAfter(t.path) {
				continue
			}
			list = append(list, due{t, j, hash})
		}
	}
	if len(list) == 0 {
		return
	}
	paths := make([]string, len(list))
	for i, d := range list {
		paths[i] = d.t.path
	}
	runID := list[0].j.rs.RunID
	r.log(ctx, runID, "info", "Checking the policies of %d paths", len(paths))
	cur, err := eng.Policies(ctx, paths)
	if err != nil {
		for _, d := range list {
			if !d.j.done {
				r.fail(ctx, d.j, "reading its policy: %v", err)
			}
		}
		return
	}
	for i, d := range list {
		if d.j.done {
			continue
		}
		if err := eng.Configure(ctx, d.t.path, cur[i], d.t.policy); err != nil {
			r.fail(ctx, d.j, "%v", err)
			continue
		}
		if err := r.Store.Put(ctx, "policy:"+d.t.path, policyMemo{Hash: d.hash, At: time.Now()}); err != nil {
			slog.Warn("remembering a policy", "err", err)
		}
	}
}

func (r *Runner) fail(ctx context.Context, j *job, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.log(ctx, j.rs.RunID, "error", "%s: %s", j.src.Name, msg)
	j.rs.Status, j.rs.Message, j.rs.Finished, j.done = "failed", msg, time.Now(), true
	_ = r.Store.SaveRunSource(context.WithoutCancel(ctx), j.rs)
}

func (r *Runner) warn(ctx context.Context, j *job, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.log(ctx, j.rs.RunID, "warn", "%s: %s", j.src.Name, msg)
	j.warns = append(j.warns, msg)
}

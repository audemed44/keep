package runner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/docker"
	"github.com/audemed44/keep/internal/engine"
	"github.com/audemed44/keep/internal/store"
)

type fakeDocker struct {
	mu         sync.Mutex
	containers map[string]docker.Container
	dump       string
	log        []string
}

func (d *fakeDocker) Exec(_ context.Context, c string, cmd []string, stdout io.Writer) error {
	d.mu.Lock()
	d.log = append(d.log, "exec "+c)
	d.mu.Unlock()
	switch {
	case c == "app": // hooks
		d.mu.Lock()
		d.log[len(d.log)-1] = "hook " + c + ": " + cmd[2]
		d.mu.Unlock()
		if strings.Contains(cmd[2], "fail") {
			return &docker.ExitError{Code: 1, Stderr: "it failed"}
		}
		return nil
	case cmd[0] == "chown":
		return nil
	case cmd[0] == "rm":
		return os.RemoveAll(cmd[len(cmd)-1])
	}
	if d.dump == "" {
		return &docker.ExitError{Code: 1, Stderr: "connection refused"}
	}
	_, err := io.WriteString(stdout, d.dump)
	return err
}

func (d *fakeDocker) Inspect(_ context.Context, name string) (docker.Container, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[name]
	if !ok {
		return c, docker.ErrNotFound
	}
	return c, nil
}

func (d *fakeDocker) set(name string, running bool) {
	c := d.containers[name]
	c.Running = running
	d.containers[name] = c
}

func (d *fakeDocker) List(context.Context) ([]docker.Summary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []docker.Summary
	for name, c := range d.containers {
		state := "exited"
		if c.Running {
			state = "running"
		}
		out = append(out, docker.Summary{ID: name, Name: name, Image: c.Image, State: state})
	}
	return out, nil
}

func (d *fakeDocker) Stop(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = append(d.log, "stop "+name)
	d.set(name, false)
	return nil
}

func (d *fakeDocker) Start(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = append(d.log, "start "+name)
	d.set(name, true)
	return nil
}

// fakeEngine "snapshots" by listing the files it would take, applying the
// ignores the way Kopia does.
type fakeEngine struct {
	ignores map[string][]string
	taken   map[string][]string // path → files in its snapshot
	fail    map[string]bool
	n       int
	// calls counts Snapshot calls, configured policies set, reads Policies
	// calls and read the paths they read.
	calls, configured, reads, read int

	listed    []engine.Snapshot // what List returns
	verify    engine.Verified
	verifyErr error
	verified  []int // Verify calls' percents
	deleted   []string
	contents  map[string]map[string][]byte // snapshot id → file → content
	restored  []string
}

func (e *fakeEngine) Restore(_ context.Context, id, sub, target string) error {
	e.restored = append(e.restored, id+"/"+sub+" → "+target)
	found := false
	for f, data := range e.contents[id] {
		rel := f
		if sub != "" {
			if f != sub && !strings.HasPrefix(f, sub+"/") {
				continue
			}
			rel = strings.TrimPrefix(strings.TrimPrefix(f, sub), "/")
		}
		found = true
		dst := filepath.Join(target, rel)
		os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("%s: %w", sub, engine.ErrNotInSnapshot)
	}
	return nil
}

func (e *fakeEngine) Name() string { return "fake" }

func (e *fakeEngine) Policies(_ context.Context, paths []string) ([]engine.Current, error) {
	e.reads++
	e.read += len(paths)
	out := make([]engine.Current, len(paths))
	for i, p := range paths {
		if ig, ok := e.ignores[p]; ok {
			out[i] = engine.Current{Ignores: ig, Manual: true}
		}
	}
	return out, nil
}

func (e *fakeEngine) Configure(_ context.Context, p string, cur engine.Current, pol engine.Policy) error {
	if cur.Manual && slices.Equal(cur.Ignores, pol.Ignores) {
		return nil
	}
	e.ignores[p] = pol.Ignores
	e.configured++
	return nil
}

func (e *fakeEngine) Snapshot(_ context.Context, paths []string, desc string) (map[string]engine.Snapshot, error) {
	e.calls++
	got := map[string]engine.Snapshot{}
	var failed []string
	for _, p := range paths {
		if e.fail[p] {
			failed = append(failed, "upload error: "+p+": upload failed")
			continue
		}
		var files []string
		var size int64
		err := filepath.WalkDir(p, func(f string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(p, f)
			if rel == "." {
				return nil
			}
			if config.Excluded(e.ignores[p], rel, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				info, _ := d.Info()
				size += info.Size()
				files = append(files, rel)
			}
			return nil
		})
		if err != nil {
			return got, err
		}
		e.taken[p] = files
		e.n++
		snap := engine.Snapshot{ID: fmt.Sprintf("snap%d", e.n), Path: p, Description: desc, Start: time.Now(), Size: size, Files: int64(len(files))}
		got[p] = snap
		e.listed = append(e.listed, snap)
		if e.contents == nil {
			e.contents = map[string]map[string][]byte{}
		}
		e.contents[snap.ID] = map[string][]byte{}
		for _, f := range files {
			e.contents[snap.ID][f], _ = os.ReadFile(filepath.Join(p, f))
		}
	}
	if len(failed) > 0 {
		return got, errors.New("exit status 1: " + strings.Join(failed, "\n"))
	}
	return got, nil
}

func (e *fakeEngine) Stats(context.Context) (engine.Stats, error) {
	return engine.Stats{Size: 12345}, nil
}

func (e *fakeEngine) List(context.Context) ([]engine.Snapshot, error) {
	return slices.Clone(e.listed), nil
}

func (e *fakeEngine) Verify(_ context.Context, percent int) (engine.Verified, error) {
	e.verified = append(e.verified, percent)
	return e.verify, e.verifyErr
}

func (e *fakeEngine) Delete(_ context.Context, ids []string) error {
	e.deleted = append(e.deleted, ids...)
	e.listed = slices.DeleteFunc(e.listed, func(s engine.Snapshot) bool { return slices.Contains(ids, s.ID) })
	return nil
}

type pings struct {
	mu   sync.Mutex
	list []string
}

type fixture struct {
	root, staging string
	store         *store.Store
	docker        *fakeDocker
	engine        *fakeEngine
	runner        *Runner
	pings         *pings
}

func writeDB(t *testing.T, p string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	db, err := sql.Open("sqlite", "file:"+p+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (v TEXT); INSERT INTO t VALUES ('a')`); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T, yml string) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir()}
	f.staging = filepath.Join(f.root, "keep", "staging")
	writeDB(t, filepath.Join(f.root, "ledger", "ledger.db"))
	os.WriteFile(filepath.Join(f.root, "ledger", "notes.txt"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(f.root, "ledger", "x.sync-conflict-1"), []byte("old"), 0o644)
	os.MkdirAll(f.staging, 0o755)
	os.WriteFile(filepath.Join(f.root, "keep", "keep.yml"), []byte("x"), 0o644)

	yml = strings.ReplaceAll(yml, "ROOT", f.root)
	db, err := store.Open(filepath.Join(t.TempDir(), "keep.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.store = db
	f.docker = &fakeDocker{containers: map[string]docker.Container{
		"kopia": {Name: "kopia", Running: true, Mounts: []docker.Mount{{Source: f.root, Destination: f.root, RW: true}}},
	}}
	f.engine = &fakeEngine{ignores: map[string][]string{}, taken: map[string][]string{}, fail: map[string]bool{}}
	f.pings = &pings{}
	hb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.pings.mu.Lock()
		f.pings.list = append(f.pings.list, r.URL.Path+" "+string(body))
		f.pings.mu.Unlock()
	}))
	t.Cleanup(hb.Close)
	f.runner = New(Options{
		Store: db, Docker: f.docker, Heartbeat: hb.URL + "/ping/abc",
		Engine: func(config.Config) engine.Engine { return f.engine },
	})
	cfg, err := config.Parse([]byte(yml))
	if err == nil {
		_, err = f.runner.SaveConfig(context.Background(), cfg)
	}
	if err != nil {
		db.Put(context.Background(), "config", map[string]string{"every": "never"}) // a broken config
	}
	return f
}

const baseConfig = `
roots: [{path: ROOT}]
staging: ROOT/keep/staging
excludes: ["*.sync-conflict-*"]
`

func TestRun(t *testing.T) {
	f := newFixture(t, baseConfig+`
sources:
  - {name: pg, strategy: postgres, container: db, volume: pgdata}
`)
	f.docker.dump = "CREATE TABLE x;\n"
	ctx := context.Background()
	id := f.runner.Run(ctx, "manual")
	run, err := f.store.GetRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "ok" || run.Trigger != "manual" || len(run.Sources) != 3 {
		t.Fatalf("%+v", run)
	}

	ledger := filepath.Join(f.root, "ledger")
	if got := f.engine.taken[ledger]; !slices.Equal(got, []string{"notes.txt"}) {
		t.Fatalf("the ledger snapshot took %v; the live database and conflicts belong out", got)
	}
	if got := f.engine.taken[filepath.Join(f.staging, "ledger")]; !slices.Equal(got, []string{"ledger.db"}) {
		t.Fatalf("staged ledger snapshot: %v", got)
	}
	if got := f.engine.taken[filepath.Join(f.staging, "pg")]; !slices.Equal(got, []string{"pg.sql"}) {
		t.Fatalf("staged pg snapshot: %v", got)
	}
	// Keep's own folder is a source too, without the staging inside it.
	if got := f.engine.taken[filepath.Join(f.root, "keep")]; !slices.Equal(got, []string{"keep.yml"}) {
		t.Fatalf("keep snapshot: %v", got)
	}
	// Staging is emptied after each source.
	if entries, _ := os.ReadDir(f.staging); len(entries) != 0 {
		t.Fatalf("staging left: %v", entries)
	}
	for _, rs := range run.Sources {
		if rs.Name == "ledger" && (rs.Databases != 1 || len(rs.Snapshots) != 2 || rs.Status != "ok") {
			t.Fatalf("ledger: %+v", rs)
		}
	}
	if len(f.pings.list) != 2 || f.pings.list[0] != "/ping/abc/start " || !strings.HasPrefix(f.pings.list[1], "/ping/abc 3 of 3 sources") {
		t.Fatalf("pings: %q", f.pings.list)
	}
	var repo RepoInfo
	f.store.Get(ctx, "repo", &repo)
	if repo.Size != 12345 {
		t.Fatalf("repo %+v", repo)
	}

	o, err := f.runner.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range o.Sources {
		if s.State != StateOK || s.Success == nil {
			t.Fatalf("%s: %s", s.Name, s.State)
		}
	}
}

func TestRunFailures(t *testing.T) {
	f := newFixture(t, baseConfig+`
sources:
  - {name: pg, strategy: postgres, container: db}
`)
	f.engine.fail[filepath.Join(f.root, "ledger")] = true
	ctx := context.Background()
	id := f.runner.Run(ctx, "schedule")
	run, _ := f.store.GetRun(ctx, id)
	if run.Status != "failed" || !strings.Contains(run.Summary, "failed: ledger, pg") {
		t.Fatalf("%+v", run)
	}
	// The other source still ran.
	if _, ok := f.engine.taken[filepath.Join(f.root, "keep")]; !ok {
		t.Fatal("keep wasn't backed up after ledger failed")
	}
	if !strings.HasPrefix(f.pings.list[1], "/ping/abc/fail ") {
		t.Fatalf("pings: %q", f.pings.list)
	}
	o, _ := f.runner.Overview(ctx)
	states := map[string]string{}
	for _, s := range o.Sources {
		states[s.Name] = s.State
	}
	if states["ledger"] != StateErrors || states["pg"] != StateErrors || states["keep"] != StateOK {
		t.Fatalf("%v", states)
	}
	logs, _ := f.store.RunLog(ctx, id, 0)
	var text []string
	for _, l := range logs {
		text = append(text, l.Text)
	}
	if !strings.Contains(strings.Join(text, "\n"), "connection refused") {
		t.Fatalf("the dump error isn't in the log:\n%s", strings.Join(text, "\n"))
	}
}

func TestRunWarnsAndSnapshotsLiveFile(t *testing.T) {
	f := newFixture(t, baseConfig)
	// A file with a SQLite header that isn't a database can't be copied:
	// the live file goes into the snapshot instead.
	bad := filepath.Join(f.root, "ledger", "broken.db")
	os.WriteFile(bad, append([]byte("SQLite format 3\x00"), make([]byte, 100)...), 0o644)
	ctx := context.Background()
	id := f.runner.Run(ctx, "manual")
	run, _ := f.store.GetRun(ctx, id)
	if run.Status != "warn" {
		t.Fatalf("%+v", run)
	}
	if got := f.engine.taken[filepath.Join(f.root, "ledger")]; !slices.Contains(got, "broken.db") {
		t.Fatalf("snapshot %v should keep the database it couldn't copy", got)
	}
	if len(f.pings.list) != 2 || strings.Contains(f.pings.list[1], "/fail") {
		t.Fatalf("a warning isn't a failure: %q", f.pings.list)
	}
}

func TestStopStrategy(t *testing.T) {
	f := newFixture(t, baseConfig+`
sources:
  - {name: ledger, strategy: stop, container: "app, idle"}
`)
	f.docker.containers["app"] = docker.Container{Name: "app", Running: true}
	f.docker.containers["idle"] = docker.Container{Name: "idle"}
	id := f.runner.Run(context.Background(), "manual")
	run, _ := f.store.GetRun(context.Background(), id)
	if run.Status != "ok" {
		t.Fatalf("%+v", run)
	}
	if got := strings.Join(f.docker.log, ","); got != "stop app,start app" {
		t.Fatalf("docker calls %q: only the running container is stopped, and started once", got)
	}
	// stop takes the files as they are: the database isn't left out.
	if got := f.engine.taken[filepath.Join(f.root, "ledger")]; !slices.Contains(got, "ledger.db") {
		t.Fatalf("%v", got)
	}
}

func TestPathMismatch(t *testing.T) {
	f := newFixture(t, baseConfig)
	f.runner.Self = "keep"
	f.docker.containers["keep"] = docker.Container{Mounts: []docker.Mount{{Source: "/elsewhere", Destination: f.root}}}
	id := f.runner.Run(context.Background(), "manual")
	run, _ := f.store.GetRun(context.Background(), id)
	if run.Status != "failed" || len(f.engine.taken) != 0 {
		t.Fatalf("%+v %v", run, f.engine.taken)
	}
	if !strings.Contains(run.Sources[0].Message, "same path") {
		t.Fatal(run.Sources[0].Message)
	}

	f.docker.containers["kopia"] = docker.Container{Name: "kopia"}
	id = f.runner.Run(context.Background(), "manual")
	run, _ = f.store.GetRun(context.Background(), id)
	if !strings.Contains(run.Sources[0].Message, "isn't running") {
		t.Fatal(run.Sources[0].Message)
	}
}

func TestBadConfig(t *testing.T) {
	f := newFixture(t, "every: never")
	id := f.runner.Run(context.Background(), "manual")
	run, _ := f.store.GetRun(context.Background(), id)
	if run.Status != "failed" || !strings.Contains(run.Summary, "settings") {
		t.Fatalf("%+v", run)
	}
	o, _ := f.runner.Overview(context.Background())
	if o.ConfigError == "" {
		t.Fatal("no config error in the overview")
	}
}

func TestScheduleAndTrigger(t *testing.T) {
	f := newFixture(t, baseConfig)
	f.runner.FirstRunAfter = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.runner.Loop(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for f.runner.State().Next.IsZero() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if next := time.Until(f.runner.State().Next); next < 59*time.Minute {
		t.Fatalf("first run in %s", next)
	}
	id, err := f.runner.Trigger("manual")
	if err != nil || id == 0 {
		t.Fatalf("%d %v", id, err)
	}
	for f.runner.State().Running != 0 && time.Now().Before(deadline.Add(3*time.Second)) {
		time.Sleep(10 * time.Millisecond)
	}
	for time.Now().Before(deadline.Add(3 * time.Second)) {
		if next := time.Until(f.runner.State().Next); next > 11*time.Hour {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("after a run, the next is in %s, not 12h", time.Until(f.runner.State().Next))
}

func TestAbandonedRun(t *testing.T) {
	f := newFixture(t, baseConfig)
	ctx := context.Background()
	id, _ := f.store.StartRun(ctx, store.KindBackup, "schedule", time.Now())
	f.store.SaveRunSource(ctx, store.RunSource{RunID: id, Name: "ledger", Strategy: "sqlite", Status: "running"})
	f.store.AbandonRuns(ctx, time.Now())
	run, _ := f.store.GetRun(ctx, id)
	if run.Status != "failed" || run.Sources[0].Status != "failed" {
		t.Fatalf("%+v", run)
	}
}

func TestStaleState(t *testing.T) {
	now := time.Now()
	ok := &store.RunSource{Status: "ok", Finished: now.Add(-30 * time.Hour)}
	if s := state(store.SourceHistory{Latest: ok, Success: ok}, false, now, 25*time.Hour); s != StateStale {
		t.Fatal(s)
	}
	if s := state(store.SourceHistory{}, false, now, time.Hour); s != StateNever {
		t.Fatal(s)
	}
	if s := state(store.SourceHistory{}, true, now, time.Hour); s != StateRunning {
		t.Fatal(s)
	}
}

func TestHostPath(t *testing.T) {
	m := []docker.Mount{{Source: "/home/a/main-stack", Destination: "/data"}, {Source: "/mnt/hdd/x", Destination: "/data/x"}}
	for in, want := range map[string]string{
		"/data/ledger": "/home/a/main-stack/ledger",
		"/data/x/db":   "/mnt/hdd/x/db",
		"/data":        "/home/a/main-stack",
	} {
		if got, _ := hostPath(in, m); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	if _, ok := hostPath("/other", m); ok {
		t.Fatal("unmounted path mapped")
	}
}

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{999: "999 B", 1500: "1.5 kB", 3_200_000_000: "3.2 GB"} {
		if got := Bytes(n); got != want {
			t.Errorf("%d: %s", n, got)
		}
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{12 * time.Hour: "12h", 90 * time.Minute: "1h30m", 5 * time.Minute: "5m", 90 * time.Second: "1m30s"} {
		if got := config.Duration(d).String(); got != want {
			t.Errorf("%s: %s", d, got)
		}
	}
}

// Every engine call opens the repository (slow over rclone): one snapshot
// call per run, and policies are only set when they change.
func TestEngineCallsBatchedAndPoliciesRemembered(t *testing.T) {
	f := newFixture(t, baseConfig)
	ctx := context.Background()
	f.runner.Run(ctx, "manual")
	if f.engine.calls != 1 || f.engine.configured != 3 { // ledger, its staging, keep
		t.Fatalf("first run: %d snapshot calls, %d policies set", f.engine.calls, f.engine.configured)
	}
	f.runner.Run(ctx, "manual")
	if f.engine.calls != 2 || f.engine.configured != 3 {
		t.Fatalf("second run: %d snapshot calls, %d policies set", f.engine.calls, f.engine.configured)
	}

	// A new exclude changes ledger's policy only.
	cfg, _ := f.runner.Config()
	cfg.Sources = append(cfg.Sources, config.Source{Name: "ledger", Excludes: []string{"*.txt"}})
	if _, err := f.runner.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	f.runner.Run(ctx, "manual")
	if f.engine.configured != 4 {
		t.Fatalf("after an edit: %d policies set", f.engine.configured)
	}
	if got := f.engine.taken[filepath.Join(f.root, "ledger")]; len(got) != 0 {
		t.Fatalf("ledger took %v; *.txt is excluded now", got)
	}
}

// The weekly re-check reads every due policy in one call, and paths set
// together come due on different days.
func TestPolicyRecheckBatched(t *testing.T) {
	f := newFixture(t, baseConfig)
	ctx := context.Background()
	f.runner.Run(ctx, "manual")
	if f.engine.reads != 1 || f.engine.read != 3 {
		t.Fatalf("first run: %d policy reads of %d paths", f.engine.reads, f.engine.read)
	}
	// Age every memo by 10 days: some paths are due, not necessarily all.
	paths := []string{filepath.Join(f.root, "ledger"), filepath.Join(f.staging, "ledger"), filepath.Join(f.root, "keep")}
	due := 0
	for _, p := range paths {
		var m policyMemo
		f.store.Get(ctx, "policy:"+p, &m)
		m.At = m.At.Add(-10 * 24 * time.Hour)
		f.store.Put(ctx, "policy:"+p, m)
		if 10*24*time.Hour >= recheckAfter(p) {
			due++
		}
	}
	f.runner.Run(ctx, "manual")
	wantReads := 1
	if due > 0 {
		wantReads = 2
	}
	if f.engine.reads != wantReads || f.engine.read != 3+due || f.engine.configured != 3 {
		t.Fatalf("re-check: %d reads of %d paths (%d due), %d set", f.engine.reads, f.engine.read, due, f.engine.configured)
	}

	spread := map[time.Duration]bool{}
	for i := range 50 {
		d := recheckAfter(fmt.Sprintf("/data/app%d", i))
		if d < policyRecheck || d >= policyRecheck+7*24*time.Hour {
			t.Fatal(d)
		}
		spread[d] = true
	}
	if len(spread) < 5 {
		t.Fatalf("due dates aren't spread: %v", spread)
	}
}

func TestVerify(t *testing.T) {
	f := newFixture(t, baseConfig)
	ctx := context.Background()
	cfg, _ := f.runner.Config()
	hb := strings.TrimSuffix(f.runner.Heartbeat, "/ping/abc") + "/ping/verify"
	cfg.Verify.Heartbeat = hb
	if _, err := f.runner.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	f.engine.verify = engine.Verified{Objects: 40, Files: 3, Bytes: 2000}
	f.engine.listed = []engine.Snapshot{{ID: "a", Path: "/data/ledger", Description: "Keep run 1", Start: time.Now()}}

	id := f.runner.Do(ctx, Job{Kind: store.KindVerify, Trigger: "manual"})
	run, _ := f.store.GetRun(ctx, id)
	if run.Kind != store.KindVerify || run.Status != "ok" || !strings.Contains(run.Summary, "3 files (2.0 kB) read back") {
		t.Fatalf("%+v", run)
	}
	if !slices.Equal(f.engine.verified, []int{5}) {
		t.Fatalf("verify calls %v: 5%% by default", f.engine.verified)
	}
	if list, _ := f.store.Snapshots(ctx); len(list) != 1 || list[0].Description != "Keep run 1" {
		t.Fatalf("listing not saved: %+v", list)
	}
	if len(f.pings.list) != 2 || f.pings.list[0] != "/ping/verify/start " || !strings.HasPrefix(f.pings.list[1], "/ping/verify 40 objects") {
		t.Fatalf("verify pings its own heartbeat: %q", f.pings.list)
	}
	// A verify isn't a backup: the overview's last backup stays empty.
	o, _ := f.runner.Overview(ctx)
	if o.LastRun != nil || o.LastVerify == nil || o.LastVerify.ID != id {
		t.Fatalf("%+v %+v", o.LastRun, o.LastVerify)
	}

	// Damage found: failed, with each problem in the log.
	f.engine.verify = engine.Verified{Objects: 4, ErrorCount: 2, Errors: []string{"invalid checksum at p14d", "invalid checksum at p15e"}}
	id = f.runner.Do(ctx, Job{Kind: store.KindVerify, Trigger: "manual"})
	run, _ = f.store.GetRun(ctx, id)
	if run.Status != "failed" || !strings.HasPrefix(run.Summary, "2 problems found") {
		t.Fatalf("%+v", run)
	}
	logs, _ := f.store.RunLog(ctx, id, 0)
	if !slices.ContainsFunc(logs, func(l store.LogLine) bool { return l.Level == "error" && l.Text == "invalid checksum at p15e" }) {
		t.Fatalf("%+v", logs)
	}
	if last := f.pings.list[len(f.pings.list)-1]; !strings.HasPrefix(last, "/ping/verify/fail ") {
		t.Fatal(last)
	}

	// The check couldn't run at all.
	f.engine.verifyErr = errors.New("kopia snapshot verify: can't connect")
	id = f.runner.Do(ctx, Job{Kind: store.KindVerify, Trigger: "manual"})
	run, _ = f.store.GetRun(ctx, id)
	if run.Status != "failed" || !strings.Contains(run.Summary, "couldn't run") {
		t.Fatalf("%+v", run)
	}
}

func TestScheduleVerify(t *testing.T) {
	f := newFixture(t, baseConfig)
	ctx := context.Background()
	if _, kind := f.runner.schedule(ctx); kind != store.KindBackup {
		t.Fatal("the first job is a backup")
	}
	// The first backup a week ago, the last one just now: the first verify
	// is due.
	old, _ := f.store.StartRun(ctx, store.KindBackup, "schedule", time.Now().Add(-8*24*time.Hour))
	f.store.FinishRun(ctx, old, "ok", "", time.Now().Add(-8*24*time.Hour))
	f.runner.Run(ctx, "manual")
	next, kind := f.runner.schedule(ctx)
	if kind != store.KindVerify || time.Until(next) > 2*time.Minute {
		t.Fatalf("%s in %s", kind, time.Until(next))
	}
	f.runner.Do(ctx, Job{Kind: store.KindVerify, Trigger: "schedule"})
	next, kind = f.runner.schedule(ctx)
	if kind != store.KindBackup || time.Until(next) < 11*time.Hour {
		t.Fatalf("after the verify: %s in %s", kind, time.Until(next))
	}
}

func TestRestore(t *testing.T) {
	f := newFixture(t, baseConfig)
	ctx := context.Background()
	run := f.runner.Run(ctx, "manual")
	restores := filepath.Join(f.root, "keep", "restores")

	// The source's points: the run, from its history (nothing listed yet).
	cfg, _ := f.runner.Config()
	ledger := config.Source{Name: "ledger", Path: filepath.Join(f.root, "ledger")}
	points, err := f.runner.RestorePoints(ctx, cfg, ledger)
	if err != nil || len(points) != 1 || points[0].Run != run {
		t.Fatalf("%+v %v", points, err)
	}

	// Everything: the files, and the database from its staged copy.
	id := f.runner.Do(ctx, Job{Kind: store.KindRestore, Trigger: "manual", Restore: &RestoreSpec{Source: "ledger", Run: run}})
	got, _ := f.store.GetRun(ctx, id)
	if got.Status != "ok" || got.Kind != store.KindRestore {
		t.Fatalf("%+v", got)
	}
	list, err := f.runner.Restores(ctx)
	if err != nil || len(list) != 1 || !list[0].Complete || list[0].Files != 2 || list[0].RunID != id {
		t.Fatalf("%+v %v", list, err)
	}
	dir := list[0].Dir
	if !strings.HasPrefix(dir, restores+"/ledger-") {
		t.Fatal(dir)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes.txt")); string(b) != "hi" {
		t.Fatalf("notes.txt: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "ledger.db")); err != nil {
		t.Fatal("the staged database isn't in the restore")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.sync-conflict-1")); err == nil {
		t.Fatal("an excluded file came back")
	}

	// One file, which only the staged copies have.
	id = f.runner.Do(ctx, Job{Kind: store.KindRestore, Restore: &RestoreSpec{Source: "ledger", Run: run, Path: "/ledger.db"}})
	if got, _ := f.store.GetRun(ctx, id); got.Status != "ok" {
		t.Fatalf("%+v", got)
	}
	// Not in the backup at all; and no going up.
	for _, p := range []string{"nope.txt", "../keep"} {
		id = f.runner.Do(ctx, Job{Kind: store.KindRestore, Restore: &RestoreSpec{Source: "ledger", Run: run, Path: p}})
		if got, _ := f.store.GetRun(ctx, id); got.Status != "failed" {
			t.Fatalf("%s: %+v", p, got)
		}
	}
	if list, _ := f.runner.Restores(ctx); len(list) != 2 {
		t.Fatalf("a restore that brought nothing back left a folder: %+v", list)
	}

	// The next backup leaves restores out of Keep's own source.
	f.runner.Run(ctx, "manual")
	for _, file := range f.engine.taken[filepath.Join(f.root, "keep")] {
		if strings.HasPrefix(file, "restores") {
			t.Fatalf("keep's snapshot took %s", file)
		}
	}

	// Old restores are deleted; a folder outside restores can't be named.
	if _, err := f.runner.RestoreDir("../ledger"); err == nil {
		t.Fatal("a name with .. was accepted")
	}
	list, _ = f.runner.Restores(ctx)
	old := list[len(list)-1]
	old.Expires = time.Now().Add(-time.Minute)
	f.store.Put(ctx, "restore:"+old.Name, old)
	f.runner.cleanRestores(ctx)
	if _, err := os.Stat(old.Dir); err == nil {
		t.Fatal("an expired restore is still there")
	}
	if after, _ := f.runner.Restores(ctx); len(after) != len(list)-1 {
		t.Fatalf("%d restores, want %d", len(after), len(list)-1)
	}
	if err := f.runner.DeleteRestore(ctx, list[0].Name); err != nil {
		t.Fatal(err)
	}

	// The engine must be able to write to the restores folder.
	f.docker.containers["kopia"] = docker.Container{Name: "kopia", Running: true, Mounts: []docker.Mount{{Source: f.root, Destination: f.root}}}
	id = f.runner.Do(ctx, Job{Kind: store.KindRestore, Restore: &RestoreSpec{Source: "ledger", Run: run}})
	if got, _ := f.store.GetRun(ctx, id); got.Status != "failed" || !strings.Contains(got.Summary, "read-write") {
		t.Fatalf("%+v", got)
	}
}

func TestOtherSourcesAndRetire(t *testing.T) {
	f := newFixture(t, baseConfig)
	ctx := context.Background()
	f.runner.Run(ctx, "manual")
	now := time.Now()
	f.engine.listed = append(f.engine.listed,
		engine.Snapshot{ID: "old1", Path: "/data/shelfloom", Start: now.Add(-48 * time.Hour), Size: 10},
		engine.Snapshot{ID: "old2", Path: "/data/shelfloom", Start: now.Add(-24 * time.Hour), Size: 20},
		engine.Snapshot{ID: "old3", Path: "/koreader", Start: now.Add(-24 * time.Hour)},
	)
	cfg, _ := f.runner.Config()
	cfg.Retire = []config.Retire{
		{Path: "/data/shelfloom", After: now.Format(time.DateOnly)},
		{Path: "/koreader", After: now.AddDate(0, 3, 0).Format(time.DateOnly)},
		{Path: filepath.Join(f.root, "ledger"), After: "2020-01-01"}, // a source now: never deleted
	}
	if _, err := f.runner.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := f.runner.listSnapshots(ctx, 0, f.engine); err != nil {
		t.Fatal(err)
	}
	others, _, err := f.runner.OtherSources(ctx)
	if err != nil || len(others) != 2 {
		t.Fatalf("Keep's own paths aren't others: %+v %v", others, err)
	}
	if o := others[0]; o.Path != "/data/shelfloom" || o.Snapshots != 2 || o.Latest != "old2" || o.Size != 20 || o.RetireAfter == "" {
		t.Fatalf("%+v", o)
	}

	id := f.runner.Do(ctx, Job{Kind: store.KindVerify, Trigger: "manual"})
	if run, _ := f.store.GetRun(ctx, id); run.Status != "warn" {
		t.Fatalf("a retire date on a source is a warning: %+v", run)
	}
	if !slices.Equal(f.engine.deleted, []string{"old2", "old1"}) {
		t.Fatalf("deleted %v", f.engine.deleted)
	}
	others, _, _ = f.runner.OtherSources(ctx)
	if len(others) != 1 || others[0].Path != "/koreader" {
		t.Fatalf("%+v", others)
	}
	cfg, _ = f.runner.Config()
	if len(cfg.Retire) != 2 || slices.ContainsFunc(cfg.Retire, func(r config.Retire) bool { return r.Path == "/data/shelfloom" }) {
		t.Fatalf("the done date stays in the settings: %+v", cfg.Retire)
	}
}

func TestHooks(t *testing.T) {
	f := newFixture(t, baseConfig+`
sources:
  - {name: ledger, hooks: {container: app, before: "app pause", after: "app resume"}}
  - {name: keep, hooks: {container: app, before: "fail now", after: "never runs"}}
`)
	ctx := context.Background()
	id := f.runner.Run(ctx, "manual")
	run, _ := f.store.GetRun(ctx, id)
	got := map[string]store.RunSource{}
	for _, rs := range run.Sources {
		got[rs.Name] = rs
	}
	if got["ledger"].Status != "ok" || got["keep"].Status != "failed" || !strings.Contains(got["keep"].Message, "before hook failed") {
		t.Fatalf("%+v", run.Sources)
	}
	if _, ok := f.engine.taken[filepath.Join(f.root, "keep")]; ok {
		t.Fatal("a source whose before hook failed was snapshotted")
	}
	if log := strings.Join(f.docker.log, ","); log != "hook app: fail now,hook app: app pause,hook app: app resume" {
		t.Fatalf("hooks: %s", log)
	}

	// A failing after hook is a warning.
	cfg, _ := f.runner.Config()
	cfg.Sources = []config.Source{{Name: "ledger", Hooks: config.Hooks{Container: "app", After: "fail later"}}}
	if _, err := f.runner.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	id = f.runner.Run(ctx, "manual")
	run, _ = f.store.GetRun(ctx, id)
	if run.Status != "warn" || !strings.Contains(run.Summary, "warnings: ledger") {
		t.Fatalf("%+v", run)
	}

	// Hooks need a container.
	cfg.Sources = []config.Source{{Name: "ledger", Hooks: config.Hooks{Before: "x"}}}
	if _, err := f.runner.SaveConfig(ctx, cfg); err == nil {
		t.Fatal("hooks without a container passed")
	}
}

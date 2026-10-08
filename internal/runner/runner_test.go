package runner

import (
	"context"
	"database/sql"
	"errors"
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
}

func (e *fakeEngine) Name() string { return "fake" }

func (e *fakeEngine) Configure(_ context.Context, p string, ignores []string) error {
	e.ignores[p] = ignores
	return nil
}

func (e *fakeEngine) Snapshot(_ context.Context, p, _ string) (engine.Snapshot, error) {
	if e.fail[p] {
		return engine.Snapshot{}, errors.New("upload failed")
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
	e.taken[p] = files
	e.n++
	return engine.Snapshot{ID: "snap" + string(rune('0'+e.n)), Path: p, Size: size, Files: int64(len(files))}, err
}

func (e *fakeEngine) Stats(context.Context) (engine.Stats, error) {
	return engine.Stats{Size: 12345}, nil
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

	cfgFile := filepath.Join(t.TempDir(), "keep.yml")
	yml = strings.ReplaceAll(yml, "ROOT", f.root)
	os.WriteFile(cfgFile, []byte(yml), 0o644)

	db, err := store.Open(filepath.Join(t.TempDir(), "keep.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.store = db
	f.docker = &fakeDocker{containers: map[string]docker.Container{
		"kopia": {Name: "kopia", Running: true, Mounts: []docker.Mount{{Source: f.root, Destination: f.root}}},
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
		Store: db, Docker: f.docker, ConfigFile: cfgFile, Heartbeat: hb.URL + "/ping/abc",
		Engine: func(config.Config) engine.Engine { return f.engine },
	})
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
	if run.Status != "failed" || !strings.Contains(run.Summary, "keep.yml") {
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
	id, _ := f.store.StartRun(ctx, "schedule", time.Now())
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
		if got := Duration(d); got != want {
			t.Errorf("%s: %s", d, got)
		}
	}
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/docker"
	"github.com/audemed44/keep/internal/engine"
	"github.com/audemed44/keep/internal/runner"
	"github.com/audemed44/keep/internal/store"
)

const token = "test-token"

type fakeDocker struct{}

func (fakeDocker) Exec(_ context.Context, _ string, _ []string, w io.Writer) error {
	_, err := io.WriteString(w, "-- dump\n")
	return err
}
func (fakeDocker) Inspect(_ context.Context, name string) (docker.Container, error) {
	if name == "kopia" {
		return docker.Container{Name: "kopia", Running: true, Mounts: []docker.Mount{{Source: "/", Destination: "/"}}}, nil
	}
	return docker.Container{}, docker.ErrNotFound
}
func (fakeDocker) Stop(context.Context, string) error  { return nil }
func (fakeDocker) Start(context.Context, string) error { return nil }

type fakeEngine struct{ fail string }

func (fakeEngine) Name() string                                      { return "fake" }
func (fakeEngine) Configure(context.Context, string, []string) error { return nil }
func (e fakeEngine) Snapshot(_ context.Context, p, _ string) (engine.Snapshot, error) {
	if filepath.Base(p) == e.fail {
		return engine.Snapshot{}, errors.New("upload failed")
	}
	return engine.Snapshot{ID: "s", Path: p, Size: 2_000_000, Files: 4}, nil
}
func (fakeEngine) Stats(context.Context) (engine.Stats, error) {
	return engine.Stats{Size: 5_000_000}, nil
}

type testServer struct {
	http.Handler
	runner *runner.Runner
	root   string
}

func newServer(t *testing.T) testServer {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	root := t.TempDir()
	for _, d := range []string{"ledger", "lookout", "romm"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	cfg := filepath.Join(t.TempDir(), "keep.yml")
	os.WriteFile(cfg, []byte("roots: [{path: "+root+"}]\nstaging: "+root+"/.staging\n"+
		"sources: [{name: romm, excludes: [/library]}, {name: pg, strategy: postgres, container: db, volume: pgdata}]\n"), 0o644)
	run := runner.New(runner.Options{
		Store: db, Docker: fakeDocker{}, ConfigFile: cfg,
		Engine: func(config.Config) engine.Engine { return fakeEngine{fail: "lookout"} },
	})
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>app")}}
	return testServer{New(Options{Store: db, Runner: run, Token: token, Web: web}).Handler(), run, root}
}

func do(h http.Handler, method, path, body string, auth bool, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuth(t *testing.T) {
	h := newServer(t)
	if rec := do(h, "GET", "/api/overview", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without a token: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/overview", "", true); rec.Code != http.StatusOK {
		t.Fatalf("with the token: %d", rec.Code)
	}

	rec := do(h, "POST", "/api/session", `{"token":"`+token+`"}`, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign in: %d %s", rec.Code, rec.Body)
	}
	cookie := rec.Result().Cookies()[0]
	if strings.Contains(cookie.Value, token) {
		t.Fatal("the cookie holds the token")
	}
	if rec := do(h, "GET", "/api/overview", "", false, "Cookie", cookie.Name+"="+cookie.Value); rec.Code != http.StatusOK {
		t.Fatalf("with the cookie: %d", rec.Code)
	}
}

func TestCrossOriginRefused(t *testing.T) {
	h := newServer(t)
	rec := do(h, "POST", "/api/runs", "", true, "Origin", "https://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d", rec.Code)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body, err)
	}
	return v
}

func TestBeforeAnyRun(t *testing.T) {
	h := newServer(t)
	o := decode[runner.Overview](t, do(h, "GET", "/api/overview", "", true))
	if o.Engine != "kopia" || len(o.Sources) != 4 || o.LastRun != nil {
		t.Fatalf("%+v", o)
	}
	w := decode[foyerWidget](t, do(h, "GET", "/api/foyer/widget", "", true))
	if w.Stats[0].Value != "Never" || w.Items[0].Action == nil || w.Items[0].Action.URL != "/api/foyer/run" {
		t.Fatalf("%+v", w)
	}
	cfg := decode[map[string]string](t, do(h, "GET", "/api/config", "", true))
	if !strings.Contains(cfg["text"], "roots") {
		t.Fatal(cfg)
	}
}

func TestAfterRun(t *testing.T) {
	h := newServer(t)
	id := h.runner.Run(context.Background(), "manual")

	runs := decode[[]store.Run](t, do(h, "GET", "/api/runs", "", true))
	if len(runs) != 1 || runs[0].ID != id || runs[0].Status != "failed" || runs[0].Size != 6_000_000 {
		t.Fatalf("%+v", runs)
	}
	run := decode[store.Run](t, do(h, "GET", "/api/runs/1", "", true))
	if len(run.Sources) != 4 {
		t.Fatalf("%+v", run)
	}
	if rec := do(h, "GET", "/api/runs/9", "", true); rec.Code != http.StatusNotFound {
		t.Fatal(rec.Code)
	}
	logs := decode[[]store.LogLine](t, do(h, "GET", "/api/runs/1/log?after=0", "", true))
	if len(logs) < 3 {
		t.Fatalf("%+v", logs)
	}
	sizes := decode[[]store.RunSource](t, do(h, "GET", "/api/sources/ledger/history", "", true))
	if len(sizes) != 1 || sizes[0].Size != 2_000_000 {
		t.Fatalf("%+v", sizes)
	}

	w := decode[foyerWidget](t, do(h, "GET", "/api/foyer/widget", "", true))
	if w.Stats[0].Tone != "bad" || w.Stats[1].Value != "3" || w.Stats[1].Unit != "/4" || w.Stats[2].Value != "5.0 MB" {
		t.Fatalf("%+v", w.Stats)
	}
	// Problems first, after the run line.
	if w.Items[1].Title != "lookout" || !strings.Contains(w.Items[1].Subtitle, "upload failed") {
		t.Fatalf("%+v", w.Items)
	}

	b := decode[struct {
		Engine     string        `json:"engine"`
		StaleHours int           `json:"stale_hours"`
		Sources    []foyerBackup `json:"sources"`
	}](t, do(h, "GET", "/api/foyer/backups", "", true))
	if b.Engine != "kopia" || b.StaleHours != 25 || len(b.Sources) != 4 {
		t.Fatalf("%+v", b)
	}
	got := map[string]foyerBackup{}
	for _, s := range b.Sources {
		got[s.Name] = s
	}
	if got["ledger"].Path != filepath.Join(h.root, "ledger") || got["ledger"].State != "ok" || got["ledger"].Last == nil {
		t.Fatalf("%+v", got["ledger"])
	}
	if got["pg"].Volume != "pgdata" || got["pg"].Path != "" || got["pg"].Strategy != "postgres" {
		t.Fatalf("%+v", got["pg"])
	}
	if !got["romm"].Partial || got["lookout"].State != "errors" {
		t.Fatalf("%+v", b.Sources)
	}

	st := decode[map[string]string](t, do(h, "GET", "/api/foyer/runs/1", "", true))
	if st["state"] != "failed" || st["url"] != "/runs/1" {
		t.Fatal(st)
	}
}

func TestRunNow(t *testing.T) {
	h := newServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.runner.Loop(ctx)
	rec := do(h, "POST", "/api/foyer/run", "{}", true)
	res := decode[map[string]string](t, rec)
	if rec.Code != http.StatusOK || res["status_url"] == "" {
		t.Fatalf("%d %v", rec.Code, res)
	}
	if rec := do(h, "POST", "/api/runs", "", true); rec.Code != http.StatusAccepted && rec.Code != http.StatusConflict {
		t.Fatalf("run now: %d", rec.Code)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if st := decode[map[string]string](t, do(h, "GET", res["status_url"], "", true)); st["state"] != "running" {
			return
		}
	}
	t.Fatal("the run didn't finish")
}

func TestSPAFallback(t *testing.T) {
	h := newServer(t)
	rec := do(h, "GET", "/some/page", "", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/healthz", "", false); rec.Code != http.StatusNoContent {
		t.Fatalf("healthz: %d", rec.Code)
	}
}

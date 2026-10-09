// Package runner runs backups: on a schedule or on request, one at a time.
// A run prepares each source (database copies and dumps into staging),
// has the engine snapshot it, records the result and reports to a
// healthchecks-style heartbeat (Lookout).
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/docker"
	"github.com/audemed44/keep/internal/engine"
	"github.com/audemed44/keep/internal/store"
)

// Docker is what a run needs from the Docker API.
type Docker interface {
	engine.Execer
	Inspect(ctx context.Context, name string) (docker.Container, error)
	List(ctx context.Context) ([]docker.Summary, error)
	Stop(ctx context.Context, name string) error
	Start(ctx context.Context, name string) error
}

type Options struct {
	Store  *store.Store
	Docker Docker
	// DefaultStaging is staging for a fresh config.
	DefaultStaging string
	// Engine builds the engine for a config; tests swap it.
	Engine func(config.Config) engine.Engine
	// Heartbeat is a healthchecks-style ping URL: /start, then the URL
	// itself or /fail. Empty: no pings.
	Heartbeat string
	// Self is Keep's own container, to turn its paths into host paths.
	Self string
	// FirstRunAfter is how long after the first start the first scheduled
	// run happens (default 10 minutes).
	FirstRunAfter time.Duration
}

type Runner struct {
	Options
	client  *http.Client
	trigger chan request

	mu      sync.Mutex
	running int64  // the run in progress, 0 when idle
	current string // the source it's on
	next    time.Time
}

func New(o Options) *Runner {
	if o.FirstRunAfter == 0 {
		o.FirstRunAfter = 10 * time.Minute
	}
	if o.Engine == nil {
		o.Engine = func(c config.Config) engine.Engine {
			return &engine.Kopia{Exec: o.Docker, Container: c.Engine.Container}
		}
	}
	return &Runner{Options: o, client: &http.Client{Timeout: 15 * time.Second}, trigger: make(chan request, 1)}
}

// Config reads the settings (what to back up, how, how often) from the
// database; edits apply from the next run.
func (r *Runner) Config() (config.Config, error) {
	c := config.Config{Staging: r.DefaultStaging}
	if err := r.Store.Get(context.Background(), "config", &c); err != nil {
		return config.Config{}, err
	}
	err := c.Validate()
	return c, err
}

// SaveConfig checks and stores the settings.
func (r *Runner) SaveConfig(ctx context.Context, c config.Config) (config.Config, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	if _, err := c.Resolve(os.ReadDir); err != nil {
		return c, err
	}
	return c, r.Store.Put(ctx, "config", c)
}

// HasConfig reports whether settings were ever saved.
func (r *Runner) HasConfig(ctx context.Context) bool {
	var raw json.RawMessage
	return r.Store.Get(ctx, "config", &raw) == nil && raw != nil
}

// ErrBusy means a run is already in progress.
var ErrBusy = errors.New("a backup is already running")

// request asks the loop for a run; the loop answers its id on id.
type request struct {
	why string
	id  chan int64
}

// Trigger asks for a run now and returns its id. When a run is already in
// progress it returns that run's id with ErrBusy.
func (r *Runner) Trigger(why string) (int64, error) {
	r.mu.Lock()
	running := r.running
	r.mu.Unlock()
	if running != 0 {
		return running, ErrBusy
	}
	req := request{why: why, id: make(chan int64, 1)}
	select {
	case r.trigger <- req:
	default:
		return 0, ErrBusy // another request is queued
	}
	select {
	case id := <-req.id:
		return id, nil
	case <-time.After(5 * time.Second):
		return 0, nil // queued; the loop picks it up when it can
	}
}

// State is what the runner is doing now.
type State struct {
	Running int64     `json:"running,omitempty"` // run id
	Current string    `json:"current,omitempty"` // source being backed up
	Next    time.Time `json:"next"`
}

func (r *Runner) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return State{Running: r.running, Current: r.current, Next: r.next}
}

// Loop runs backups on schedule until ctx ends.
func (r *Runner) Loop(ctx context.Context) {
	if err := r.Store.AbandonRuns(ctx, time.Now()); err != nil {
		slog.Error("marking interrupted runs", "err", err)
	}
	for {
		next := r.schedule(ctx)
		r.mu.Lock()
		r.next = next
		r.mu.Unlock()
		timer := time.NewTimer(time.Until(next))
		req := request{why: "schedule"}
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case req = <-r.trigger:
			timer.Stop()
		case <-timer.C:
		}
		r.runRequest(ctx, req)
	}
}

// schedule is when the next run is due: Every after the last one started.
// An overdue run starts a minute after Keep does.
func (r *Runner) schedule(ctx context.Context) time.Time {
	now := time.Now()
	every := 12 * time.Hour
	if c, err := r.Config(); err == nil {
		every = c.Every.D()
	}
	last, ok, err := r.Store.LastRun(ctx)
	switch {
	case err != nil || !ok:
		return now.Add(r.FirstRunAfter)
	case last.Started.Add(every).Before(now):
		return now.Add(time.Minute)
	}
	return last.Started.Add(every)
}

// Run does one backup of every source and returns its id.
func (r *Runner) Run(ctx context.Context, trigger string) int64 {
	return r.runRequest(ctx, request{why: trigger})
}

func (r *Runner) runRequest(ctx context.Context, req request) int64 {
	started := time.Now()
	id, err := r.Store.StartRun(ctx, req.why, started)
	if req.id != nil {
		req.id <- id // 0 when it couldn't start
	}
	if err != nil {
		slog.Error("starting a run", "err", err)
		return 0
	}
	r.mu.Lock()
	r.running = id
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running, r.current = 0, ""
		r.mu.Unlock()
	}()

	r.ping(ctx, "/start", "")
	status, summary := r.run(ctx, id)
	// Record the end even when Keep is shutting down.
	done := context.WithoutCancel(ctx)
	if err := r.Store.FinishRun(done, id, status, summary, time.Now()); err != nil {
		slog.Error("finishing a run", "err", err)
	}
	r.log(done, id, levelFor(status), "Finished in %s: %s", time.Since(started).Round(time.Second), summary)
	if status == "failed" {
		r.ping(done, "/fail", summary)
	} else {
		r.ping(done, "", summary)
	}
	if err := r.Store.Prune(done, time.Now(), 90*24*time.Hour, 400*24*time.Hour); err != nil {
		slog.Warn("pruning old runs", "err", err)
	}
	return id
}

func levelFor(status string) string {
	switch status {
	case "failed":
		return "error"
	case "warn":
		return "warn"
	}
	return "info"
}

// RepoInfo is the repository size after the last run.
type RepoInfo struct {
	Size int64     `json:"size"`
	At   time.Time `json:"at"`
}

// stop stops the running containers among names and returns them, to be
// started again.
func (r *Runner) stop(ctx context.Context, runID int64, source string, names []string) ([]string, error) {
	var stopped []string
	for _, n := range names {
		c, err := r.Docker.Inspect(ctx, n)
		if err != nil {
			return stopped, err
		}
		if !c.Running {
			continue
		}
		r.log(ctx, runID, "info", "%s: stopping %s", source, n)
		if err := r.Docker.Stop(ctx, n); err != nil {
			return stopped, err
		}
		stopped = append(stopped, n)
	}
	return stopped, nil
}

// start starts containers stopped for a snapshot; starting one that's
// already running does nothing.
func (r *Runner) start(ctx context.Context, runID int64, source string, names []string) {
	for _, n := range names {
		if err := r.Docker.Start(ctx, n); err != nil {
			r.log(ctx, runID, "error", "%s: starting %s again: %v", source, n, err)
		}
	}
}

func (r *Runner) ping(ctx context.Context, suffix, body string) {
	if r.Heartbeat == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.Heartbeat, "/")+suffix, strings.NewReader(body))
	if err != nil {
		slog.Warn("heartbeat", "err", err)
		return
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := r.client.Do(req)
	if err != nil {
		slog.Warn("heartbeat", "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.Warn("heartbeat", "status", resp.StatusCode)
	}
}

func (r *Runner) log(ctx context.Context, runID int64, level, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	switch level {
	case "error":
		slog.Error(text, "run", runID)
	case "warn":
		slog.Warn(text, "run", runID)
	default:
		slog.Info(text, "run", runID)
	}
	if err := r.Store.Log(context.WithoutCancel(ctx), runID, level, text); err != nil {
		slog.Warn("saving a log line", "err", err)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dedupe(list []string) []string {
	out := slices.Clone(list)
	slices.Sort(out)
	return slices.Compact(out)
}

func hasFiles(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return found, err
}

// Within reports whether p is dir or inside it.
func Within(p, dir string) bool { return within(p, dir) }

// within reports whether p is dir or inside it.
func within(p, dir string) bool {
	p, dir = path.Clean(p), path.Clean(dir)
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}

func rel(base, p string) string {
	return strings.TrimPrefix(strings.TrimPrefix(path.Clean(p), path.Clean(base)), "/")
}

// Bytes formats a size like 1.2 GB.
func Bytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

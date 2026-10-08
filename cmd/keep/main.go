// Command keep serves Keep's API and frontend from one small binary.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the runtime image may have no zoneinfo; TZ needs this

	"github.com/audemed44/keep/internal/server"
	"github.com/audemed44/keep/internal/store"
	"github.com/audemed44/keep/web"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	level := slog.LevelInfo
	if os.Getenv("KEEP_DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	token := os.Getenv("KEEP_TOKEN")
	if token == "" {
		slog.Error("set KEEP_TOKEN: it's what you sign in with, and what Foyer uses for the widget")
		os.Exit(1)
	}
	dataDir := env("KEEP_DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		slog.Error("could not create the data folder", "err", err)
		os.Exit(1)
	}
	db, err := store.Open(filepath.Join(dataDir, "keep.db"))
	if err != nil {
		slog.Error("could not open the database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	app := server.New(server.Options{Store: db, Token: token, FoyerURL: foyerURL(), Web: dist})

	srv := &http.Server{
		Addr:              ":" + env("KEEP_PORT", "8080"),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("keep listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// healthcheck is the image's HEALTHCHECK: the runtime image has no curl.
func healthcheck() int {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + env("KEEP_PORT", "8080") + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return 1
	}
	return 0
}

// foyerURL is HOMEPAGE_URL, the link back to Foyer in the header, when
// it's an http(s) address.
func foyerURL() string {
	u := os.Getenv("HOMEPAGE_URL")
	if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		slog.Warn("HOMEPAGE_URL isn't an http(s) address; ignoring it", "url", u)
		return ""
	}
	return u
}

// Package engine is the backup engine Keep drives: the tool that chunks,
// deduplicates, encrypts and stores snapshots. Keep never does those
// itself; it only calls this interface.
//
// Kopia is the engine now. restic is planned behind the same interface
// (see the README), which is why nothing outside this package knows how
// Kopia is called.
package engine

import (
	"context"
	"io"
	"time"
)

type Engine interface {
	// Name is the engine's name, for reports ("kopia").
	Name() string
	// Configure makes path a source Keep schedules (the engine's own
	// schedule off) with exactly this policy: ignores not inherited from
	// parent folders or the engine's global settings.
	Configure(ctx context.Context, path string, p Policy) error
	// Snapshot takes a snapshot of each path, in one go: opening the
	// repository is the slow part (tens of seconds over rclone). Paths that
	// failed are missing from the map, and the error says why. The engine
	// applies its retention.
	Snapshot(ctx context.Context, paths []string, description string) (map[string]Snapshot, error)
	// Stats reads the repository's size.
	Stats(ctx context.Context) (Stats, error)
}

// Policy is how a path is snapshotted and how many snapshots are kept.
type Policy struct {
	Ignores   []string  `json:"ignores"`
	Retention Retention `json:"retention"`
}

// Retention is the keep-* rules Kopia and restic share.
type Retention struct {
	Latest, Hourly, Daily, Weekly, Monthly, Annual int
}

type Snapshot struct {
	ID    string    `json:"id"`
	Path  string    `json:"path"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Size  int64     `json:"size"`
	Files int64     `json:"files"`
	// Errors counts files the engine couldn't read.
	Errors int64 `json:"errors"`
}

type Stats struct {
	// Size is what the repository holds in storage, after dedup and
	// compression.
	Size int64 `json:"size"`
}

// Execer runs a command in a container, writing its stdout to stdout
// (internal/docker in production).
type Execer interface {
	Exec(ctx context.Context, container string, cmd []string, stdout io.Writer) error
}

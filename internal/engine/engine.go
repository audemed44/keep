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
	"errors"
	"io"
	"time"
)

type Engine interface {
	// Name is the engine's name, for reports ("kopia").
	Name() string
	// Policies reads the policies paths have now, in one go, in the same
	// order.
	Policies(ctx context.Context, paths []string) ([]Current, error)
	// Configure makes path a source Keep schedules (the engine's own
	// schedule off) with exactly this policy: ignores not inherited from
	// parent folders or the engine's global settings. cur is what Policies
	// read; nothing is set when it already matches.
	Configure(ctx context.Context, path string, cur Current, want Policy) error
	// Snapshot takes a snapshot of each path, in one go: opening the
	// repository is the slow part (tens of seconds over rclone). Paths that
	// failed are missing from the map, and the error says why. The engine
	// applies its retention.
	Snapshot(ctx context.Context, paths []string, description string) (map[string]Snapshot, error)
	// Stats reads the repository's size.
	Stats(ctx context.Context) (Stats, error)
	// List lists every snapshot in the repository.
	List(ctx context.Context) ([]Snapshot, error)
	// Verify checks the repository's structure and reads percent of the
	// files back. The error is for a check that couldn't run; damage it
	// found is in Verified.Errors.
	Verify(ctx context.Context, percent int) (Verified, error)
	// Delete deletes snapshots.
	Delete(ctx context.Context, ids []string) error
	// Restore writes a snapshot, or the file or folder at subpath inside
	// it, to target. The target's parent folder must exist. A subpath the
	// snapshot doesn't have is ErrNotInSnapshot.
	Restore(ctx context.Context, id, subpath, target string) error
}

// ErrNotInSnapshot is a restore of a path the snapshot doesn't have.
var ErrNotInSnapshot = errors.New("not in the snapshot")

type Verified struct {
	Objects int64 `json:"objects"` // checked
	Files   int64 `json:"files"`   // read back
	Bytes   int64 `json:"bytes"`
	// Errors are the problems found, at most 50.
	Errors []string `json:"errors"`
	// ErrorCount is how many there were.
	ErrorCount int `json:"error_count"`
}

// Policy is how a path is snapshotted and how many snapshots are kept.
type Policy struct {
	Ignores   []string  `json:"ignores"`
	Retention Retention `json:"retention"`
}

// Current is a path's policy as the engine has it.
type Current struct {
	Ignores []string
	// Retention is nil when any of the counts isn't set.
	Retention *Retention
	// Manual is true when the engine's own schedule is off.
	Manual bool
}

// Retention is the keep-* rules Kopia and restic share.
type Retention struct {
	Latest, Hourly, Daily, Weekly, Monthly, Annual int
}

type Snapshot struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	Description string    `json:"description,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Size        int64     `json:"size"`
	Files       int64     `json:"files"`
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

package runner

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/docker"
)

// Keep and the engine's container must see every source at the same path:
// Keep tells the engine "snapshot /data/ledger" and the engine reads its
// own /data/ledger. pathCheck compares the two containers' mounts so a
// mismatch fails loudly instead of backing up the wrong folder.
type pathCheck struct {
	self, eng []docker.Mount
	selfOK    bool // Keep runs in a container it could inspect
	engOK     bool
	engErr    error
}

func (r *Runner) pathCheck(ctx context.Context, cfg config.Config) pathCheck {
	var pc pathCheck
	if r.Self != "" {
		if c, err := r.Docker.Inspect(ctx, r.Self); err == nil {
			pc.self, pc.selfOK = c.Mounts, true
		}
	}
	c, err := r.Docker.Inspect(ctx, cfg.Engine.Container)
	switch {
	case err != nil:
		pc.engErr = fmt.Errorf("the engine's container %s: %w", cfg.Engine.Container, err)
	case !c.Running:
		pc.engErr = fmt.Errorf("the engine's container %s isn't running", cfg.Engine.Container)
	default:
		pc.eng, pc.engOK = c.Mounts, true
	}
	return pc
}

func (pc pathCheck) check(p string) error {
	if pc.engErr != nil {
		return pc.engErr
	}
	theirs, ok := hostPath(p, pc.eng)
	if !ok {
		return fmt.Errorf("the engine's container doesn't see %s: mount it there", p)
	}
	if !pc.selfOK {
		return nil // Keep isn't in a container: its paths are host paths
	}
	ours, ok := hostPath(p, pc.self)
	if !ok {
		ours = p
	}
	if ours != theirs {
		return fmt.Errorf("%s is %s for Keep but %s for the engine: mount the same folder at the same path in both", p, ours, theirs)
	}
	return nil
}

// HostPaths returns a function that turns paths inside Keep's container
// into host paths, from one look at Keep's mounts.
func (r *Runner) HostPaths(ctx context.Context) func(string) string {
	var mounts []docker.Mount
	if r.Self != "" {
		if c, err := r.Docker.Inspect(ctx, r.Self); err == nil {
			mounts = c.Mounts
		}
	}
	return func(p string) string {
		if h, ok := hostPath(p, mounts); ok {
			return h
		}
		return p
	}
}

// hostPath maps p through the mount that covers it most closely.
func hostPath(p string, mounts []docker.Mount) (string, bool) {
	best := -1
	for i, m := range mounts {
		if within(p, m.Destination) && (best < 0 || len(m.Destination) > len(mounts[best].Destination)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	m := mounts[best]
	return path.Join(m.Source, strings.TrimPrefix(path.Clean(p), path.Clean(m.Destination))), true
}

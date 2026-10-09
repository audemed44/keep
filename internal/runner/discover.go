package runner

import (
	"context"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/audemed44/keep/internal/config"
)

// Browsing and suggestions, for choosing what to back up in the UI. Keep
// can only back up what it (and the engine) can see, so both work inside
// Keep's own bind mounts: the browse roots.

// BrowseRoots are the folders Keep can see: its bind mounts, except the
// Docker socket and single files. Outside a container, "/".
func (r *Runner) BrowseRoots(ctx context.Context) []string {
	if r.Self == "" {
		return []string{"/"}
	}
	c, err := r.Docker.Inspect(ctx, r.Self)
	if err != nil {
		return []string{"/"}
	}
	var out []string
	for _, m := range c.Mounts {
		if m.Type != "bind" || systemPath(m.Destination) {
			continue
		}
		if info, err := os.Stat(m.Destination); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, path.Clean(m.Destination))
	}
	sort.Strings(out)
	return out
}

func systemPath(p string) bool {
	if strings.HasSuffix(p, ".sock") {
		return true
	}
	for _, sys := range []string{"/etc", "/sys", "/proc", "/dev", "/usr", "/lib", "/run", "/var/run", "/boot"} {
		if within(p, sys) {
			return true
		}
	}
	return false
}

// Coverage says what backs a path up.
type Coverage struct {
	// Source is the source the path is in ("" when none).
	Source string `json:"source,omitempty"`
	// Excluded is true when the path is inside a source but left out.
	Excluded bool `json:"excluded,omitempty"`
	// Root is a watched folder the path is (whose subfolders are sources).
	Root bool `json:"root,omitempty"`
	// Contains is how many sources are inside the path.
	Contains int `json:"contains,omitempty"`
}

// Cover reports how p is covered by the resolved sources.
func Cover(cfg config.Config, sources []config.Source, p string) Coverage {
	var c Coverage
	for _, root := range cfg.Roots {
		if root.Path == p {
			c.Root = true
		}
	}
	for _, s := range sources {
		if s.Path == "" {
			continue
		}
		if within(p, s.Path) {
			c.Source = s.Name
			if p != s.Path {
				rel := rel(s.Path, p)
				excl := slices.Concat(cfg.Excludes, s.Excludes)
				parts := strings.Split(rel, "/")
				for i := range parts {
					if config.Excluded(excl, strings.Join(parts[:i+1], "/"), true) {
						c.Excluded = true
					}
				}
			}
		} else if within(s.Path, p) {
			c.Contains++
		}
	}
	return c
}

type Folder struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Coverage
}

type Listing struct {
	Path    string   `json:"path"`
	Parent  string   `json:"parent,omitempty"` // "" at a root
	Roots   []string `json:"roots"`
	Folders []Folder `json:"folders"`
	Coverage
}

// ErrOutside is a path Keep can't see.
type ErrOutside struct{ Path string }

func (e ErrOutside) Error() string {
	return e.Path + " isn't in a folder Keep can see (its mounts)"
}

// Browse lists the folders in p, each with what backs it up.
func (r *Runner) Browse(ctx context.Context, p string) (Listing, error) {
	roots := r.BrowseRoots(ctx)
	if p == "" {
		p = roots[0]
	}
	p = path.Clean(p)
	var root string
	for _, rt := range roots {
		if within(p, rt) {
			root = rt
		}
	}
	if root == "" || !path.IsAbs(p) {
		return Listing{}, ErrOutside{p}
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return Listing{}, err
	}
	cfg, err := r.Config()
	if err != nil {
		return Listing{}, err
	}
	sources, _ := cfg.Resolve(os.ReadDir)
	out := Listing{Path: p, Roots: roots, Folders: []Folder{}, Coverage: Cover(cfg, sources, p)}
	if p != root {
		out.Parent = path.Dir(p)
	}
	for _, e := range entries {
		full := path.Join(p, e.Name())
		if !e.IsDir() || full == cfg.Staging || full == cfg.Restores {
			continue
		}
		out.Folders = append(out.Folders, Folder{Name: e.Name(), Path: full, Coverage: Cover(cfg, sources, full)})
	}
	return out, nil
}

// Suggestion is data a container keeps that no source backs up.
type Suggestion struct {
	Container string `json:"container"`
	Image     string `json:"image"`
	Running   bool   `json:"running"`
	// Kind is folder (add it as a source), database (add a dump source),
	// or volume (a Docker volume Keep can't reach; dump it or move it to a
	// folder).
	Kind     string `json:"kind"`
	Path     string `json:"path,omitempty"`
	Volume   string `json:"volume,omitempty"`
	Strategy string `json:"strategy,omitempty"` // for database: postgres or mariadb
	// Mount is where the container sees it.
	Mount string `json:"mount"`
}

// Suggestions lists what containers mount that isn't backed up.
func (r *Runner) Suggestions(ctx context.Context) ([]Suggestion, error) {
	cfg, err := r.Config()
	if err != nil {
		return nil, err
	}
	sources, _ := cfg.Resolve(os.ReadDir)
	roots := r.BrowseRoots(ctx)
	list, err := r.Docker.List(ctx)
	if err != nil {
		return nil, err
	}
	dumped := map[string]bool{}
	for _, s := range sources {
		if s.Volume != "" {
			dumped[s.Volume] = true
		}
	}
	skip := map[string]bool{cfg.Engine.Container: true}
	if r.Self != "" {
		if self, err := r.Docker.Inspect(ctx, r.Self); err == nil {
			skip[self.Name] = true
		}
	}
	out := []Suggestion{}
	seen := map[string]bool{}
	for _, sum := range list {
		if skip[sum.Name] {
			continue
		}
		c, err := r.Docker.Inspect(ctx, sum.ID)
		if err != nil {
			continue
		}
		for _, m := range c.Mounts {
			sg := Suggestion{Container: sum.Name, Image: sum.Image, Running: sum.State == "running", Mount: m.Destination}
			switch m.Type {
			case "bind":
				if systemPath(m.Source) || seen[m.Source] {
					continue
				}
				if !slices.ContainsFunc(roots, func(rt string) bool { return within(m.Source, rt) }) {
					continue // Keep can't see it; nothing to offer
				}
				if info, err := os.Stat(m.Source); err != nil || !info.IsDir() {
					continue // a single file (a config), or gone
				}
				if cov := Cover(cfg, sources, m.Source); cov.Source != "" && !cov.Excluded {
					continue
				}
				sg.Kind, sg.Path = "folder", m.Source
			case "volume":
				// Anonymous volumes (an image's VOLUME line) are named by a
				// hash; they're not data anyone chose to keep.
				if seen["v:"+m.Name] || dumped[m.Name] || anonymous.MatchString(m.Name) {
					continue
				}
				sg.Kind, sg.Volume = "volume", m.Name
				if st := dbStrategy(sum.Image); st != "" {
					sg.Kind, sg.Strategy = "database", st
				}
			default:
				continue
			}
			if m.Type == "bind" {
				seen[m.Source] = true
			} else {
				seen["v:"+m.Name] = true
			}
			out = append(out, sg)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Running != out[j].Running {
			return out[i].Running
		}
		return out[i].Container < out[j].Container
	})
	return out, nil
}

var anonymous = regexp.MustCompile(`^[0-9a-f]{64}$`)

// dbStrategy guesses a dump strategy from an image name.
func dbStrategy(image string) string {
	name := strings.ToLower(image)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	switch {
	case strings.HasPrefix(name, "postgres"), strings.Contains(name, "pgvecto"), strings.Contains(name, "postgis"):
		return config.Postgres
	case strings.HasPrefix(name, "mariadb"), strings.HasPrefix(name, "mysql"):
		return config.MariaDB
	}
	return ""
}

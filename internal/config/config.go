// Package config reads keep.yml: what to back up, how, and how often.
//
// Sources come from two places. Every folder inside a root becomes a source
// named after the folder, so a new app's data is backed up without
// touching the config. Entries under sources add sources (with a path or a
// container to dump) or change discovered ones (same name).
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Strategies say how a source is made consistent before its snapshot.
const (
	SQLite   = "sqlite"   // copy every SQLite database with VACUUM INTO (the default)
	Files    = "files"    // plain files, nothing to prepare
	Postgres = "postgres" // pg_dump inside the container
	MariaDB  = "mariadb"  // mariadb-dump inside the container
	Stop     = "stop"     // stop the containers, snapshot, start them again
)

var strategies = []string{SQLite, Files, Postgres, MariaDB, Stop}

type Config struct {
	// Every is the time between scheduled runs.
	Every Duration `yaml:"every" json:"every"`
	// StaleAfter marks a source stale when its last good backup is older.
	// Default: twice Every plus an hour.
	StaleAfter Duration `yaml:"stale_after" json:"stale_after"`
	Roots      []Root   `yaml:"roots" json:"roots"`
	Sources    []Source `yaml:"sources" json:"-"`
	// Excludes apply to every source, on top of its own.
	Excludes []string `yaml:"excludes" json:"excludes"`
	Engine   Engine   `yaml:"engine" json:"engine"`
	// Staging is where dumps are written before their snapshot. The engine
	// must see it at the same path.
	Staging string `yaml:"staging" json:"staging"`
}

type Root struct {
	Path string   `yaml:"path" json:"path"`
	Skip []string `yaml:"skip" json:"skip"` // folder names to leave out
}

type Engine struct {
	Type      string `yaml:"type" json:"type"`           // kopia
	Container string `yaml:"container" json:"container"` // where the engine runs
}

type Source struct {
	Name     string   `yaml:"name" json:"name"`
	Path     string   `yaml:"path" json:"path,omitempty"`
	Strategy string   `yaml:"strategy" json:"strategy"`
	Excludes []string `yaml:"excludes" json:"excludes,omitempty"`
	// Container is the database container (postgres, mariadb) or the
	// containers to stop (stop), comma-separated.
	Container string `yaml:"container" json:"container,omitempty"`
	// Volume names the Docker volume a database dump covers, for reports.
	Volume string `yaml:"volume" json:"volume,omitempty"`
	// Command replaces the default dump command (run with sh -c in the
	// container, writing the dump to stdout).
	Command string `yaml:"command" json:"command,omitempty"`
	Skip    bool   `yaml:"skip" json:"skip,omitempty"`
	// Discovered is true for a folder found in a root.
	Discovered bool `yaml:"-" json:"discovered"`
}

// Duration reads "12h" style durations.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %q isn't a duration like 12h or 30m", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%q", time.Duration(d).String())), nil
}

func (d Duration) D() time.Duration { return time.Duration(d) }

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Load reads and checks a config file.
func Load(file string) (Config, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return Config{}, err
	}
	return Parse(raw)
}

// Parse checks a config and fills in defaults.
func Parse(raw []byte) (Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) { // empty file: all defaults
		return Config{}, err
	}
	if c.Every == 0 {
		c.Every = Duration(12 * time.Hour)
	}
	if c.Every.D() < 5*time.Minute {
		return Config{}, errors.New("every: runs need at least 5m between them")
	}
	if c.StaleAfter == 0 {
		c.StaleAfter = Duration(2*c.Every.D() + time.Hour)
	}
	if c.Engine.Type == "" {
		c.Engine.Type = "kopia"
	}
	if c.Engine.Type != "kopia" {
		return Config{}, fmt.Errorf("engine: %q isn't supported yet (kopia is)", c.Engine.Type)
	}
	if c.Engine.Container == "" {
		c.Engine.Container = "kopia"
	}
	if c.Staging == "" {
		c.Staging = "/data/keep/staging"
	}
	if !path.IsAbs(c.Staging) {
		return Config{}, errors.New("staging: must be an absolute path")
	}
	c.Staging = path.Clean(c.Staging)
	for i, r := range c.Roots {
		if !path.IsAbs(r.Path) {
			return Config{}, fmt.Errorf("roots[%d]: path must be absolute", i)
		}
		c.Roots[i].Path = path.Clean(r.Path)
	}
	for _, e := range c.Excludes {
		if err := CheckExclude(e); err != nil {
			return Config{}, fmt.Errorf("excludes: %w", err)
		}
	}
	seen := map[string]bool{}
	for i := range c.Sources {
		s := &c.Sources[i]
		if !validName.MatchString(s.Name) {
			return Config{}, fmt.Errorf("sources[%d]: name %q: use letters, digits, '.', '_' or '-'", i, s.Name)
		}
		if seen[s.Name] {
			return Config{}, fmt.Errorf("sources: %q is listed twice", s.Name)
		}
		seen[s.Name] = true
		if s.Strategy != "" && !slices.Contains(strategies, s.Strategy) {
			return Config{}, fmt.Errorf("%s: strategy %q: use one of %s", s.Name, s.Strategy, strings.Join(strategies, ", "))
		}
		for _, e := range s.Excludes {
			if err := CheckExclude(e); err != nil {
				return Config{}, fmt.Errorf("%s: %w", s.Name, err)
			}
		}
		if s.Path != "" {
			if !path.IsAbs(s.Path) {
				return Config{}, fmt.Errorf("%s: path must be absolute", s.Name)
			}
			s.Path = path.Clean(s.Path)
		}
		switch s.Strategy {
		case Postgres, MariaDB:
			if s.Container == "" {
				return Config{}, fmt.Errorf("%s: %s needs the database's container", s.Name, s.Strategy)
			}
		case Stop:
			if s.Container == "" {
				return Config{}, fmt.Errorf("%s: stop needs the containers to stop", s.Name)
			}
		}
	}
	return c, nil
}

// Resolve lists the sources to back up: the folders found in the roots,
// changed by the sources of the same name, then the other sources. Skipped
// sources are left out; staging and the folders a root skips are never
// sources.
func (c Config) Resolve(readDir func(string) ([]os.DirEntry, error)) ([]Source, error) {
	byName := map[string]Source{}
	for _, s := range c.Sources {
		byName[s.Name] = s
	}
	var out []Source
	used := map[string]bool{}
	for _, r := range c.Roots {
		entries, err := readDir(r.Path)
		if err != nil {
			return nil, fmt.Errorf("root %s: %w", r.Path, err)
		}
		for _, e := range entries {
			name := e.Name()
			full := path.Join(r.Path, name)
			if !e.IsDir() || strings.HasPrefix(name, ".") || slices.Contains(r.Skip, name) || full == c.Staging {
				continue
			}
			s := Source{Name: name, Path: full, Strategy: SQLite, Discovered: true}
			if o, ok := byName[name]; ok {
				used[name] = true
				s = merge(s, o)
			}
			if !validName.MatchString(s.Name) {
				continue
			}
			if _, dup := findName(out, s.Name); dup {
				return nil, fmt.Errorf("two roots have a folder called %s; rename one with a source entry", s.Name)
			}
			out = append(out, s)
		}
	}
	for _, s := range c.Sources {
		if used[s.Name] {
			continue
		}
		if s.Path == "" && s.Strategy != Postgres && s.Strategy != MariaDB {
			if s.Skip {
				continue // skipping a folder that isn't there (any more)
			}
			return nil, fmt.Errorf("%s: no folder of that name in a root, and no path", s.Name)
		}
		out = append(out, merge(Source{}, s))
	}
	kept := out[:0]
	for _, s := range out {
		if !s.Skip {
			kept = append(kept, s)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Name < kept[j].Name })
	return kept, nil
}

func merge(base, o Source) Source {
	base.Name = o.Name
	if o.Path != "" {
		base.Path = o.Path
	}
	base.Strategy = o.Strategy
	if base.Strategy == "" {
		base.Strategy = SQLite
	}
	base.Excludes = o.Excludes
	base.Container = o.Container
	base.Volume = o.Volume
	base.Command = o.Command
	base.Skip = o.Skip
	return base
}

func findName(list []Source, name string) (int, bool) {
	for i, s := range list {
		if s.Name == name {
			return i, true
		}
	}
	return -1, false
}

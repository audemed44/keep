package config

import (
	"fmt"
	"path"
	"strings"
)

// Excludes use the part of gitignore syntax that Kopia (and restic) read
// the same way, so Keep's own walk agrees with the engine:
//
//	/library          library at the top of the source only
//	/config/index-v2  a path from the top of the source
//	*.log             any file or folder with that name, at any depth
//	cache/            folders only
//
// Patterns are globs (*, ?, [..]); ** isn't supported.

// CheckExclude reports a pattern Keep can't follow.
func CheckExclude(p string) error {
	switch {
	case strings.TrimSpace(p) == "" || strings.TrimSuffix(p, "/") == "":
		return fmt.Errorf("an empty exclude")
	case strings.Contains(p, "**"):
		return fmt.Errorf("exclude %q: ** isn't supported; anchor it with a leading / instead", p)
	case strings.HasPrefix(p, "!"):
		return fmt.Errorf("exclude %q: negation isn't supported", p)
	}
	if _, err := path.Match(strings.Trim(p, "/"), ""); err != nil {
		return fmt.Errorf("exclude %q: %w", p, err)
	}
	return nil
}

// Excluded reports whether rel (slash-separated, relative to the source,
// no leading slash) matches a pattern.
func Excluded(patterns []string, rel string, dir bool) bool {
	for _, p := range patterns {
		if match(p, rel, dir) {
			return true
		}
	}
	return false
}

func match(p, rel string, dir bool) bool {
	if strings.HasSuffix(p, "/") {
		if !dir {
			return false
		}
		p = strings.TrimSuffix(p, "/")
	}
	if strings.Contains(p, "/") {
		// Anchored: compare with the whole relative path.
		ok, _ := path.Match(strings.TrimPrefix(p, "/"), rel)
		return ok
	}
	ok, _ := path.Match(p, path.Base(rel))
	return ok
}

// Literal turns a relative file path into a pattern that matches only it
// (anchored, with glob characters escaped).
func Literal(rel string) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, r := range rel {
		if strings.ContainsRune(`*?[\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

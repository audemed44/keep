package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDefaults(t *testing.T) {
	c, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Every.D() != 12*time.Hour || c.StaleAfter.D() != 25*time.Hour {
		t.Fatalf("every %s, stale %s", c.Every.D(), c.StaleAfter.D())
	}
	if c.Engine.Type != "kopia" || c.Engine.Container != "kopia" || c.Staging != "/data/keep/staging" {
		t.Fatalf("%+v", c)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	c, err := Parse([]byte("every: 6h\nroots: [{path: /data}]\nsources: [{name: romm, excludes: [/library]}]"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c)
	var back Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Validate(); err != nil {
		t.Fatal(err)
	}
	if back.Every.D() != 6*time.Hour || len(back.Sources) != 1 || back.Sources[0].Excludes[0] != "/library" ||
		back.Retention != DefaultRetention || back.Roots[0].Path != "/data" {
		t.Fatalf("%+v", back)
	}
	back.Retention.Latest = 0
	back.Retention.Daily = 3
	if err := back.Validate(); err == nil {
		t.Fatal("retention without the latest snapshot passed")
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ yml, want string }{
		{"every: soon", "isn't a duration"},
		{"every: 1m", "at least 5m"},
		{"engine: {type: borg}", "isn't supported"},
		{"bogus: 1", "not found"},
		{"sources: [{name: 'a b'}]", "name"},
		{"sources: [{name: a}, {name: a}]", "twice"},
		{"sources: [{name: a, strategy: zip}]", "strategy"},
		{"sources: [{name: a, strategy: postgres}]", "container"},
		{"sources: [{name: a, path: rel}]", "absolute"},
		{"excludes: ['**/x']", "**"},
		{"sources: [{name: a, excludes: ['!x']}]", "negation"},
	} {
		_, err := Parse([]byte(tc.yml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.yml, err, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"ledger", "romm", "scripts", "old", ".hidden", "keep/staging"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o644)
	c, err := Parse([]byte(`
roots: [{path: ` + root + `, skip: [scripts]}]
staging: ` + root + `/keep/staging
sources:
  - {name: romm, excludes: [/library]}
  - {name: old, skip: true}
  - {name: gone, skip: true}
  - {name: docs, path: /docvault, strategy: files}
  - {name: pg, strategy: postgres, container: db, volume: pgdata}
`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Resolve(os.ReadDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range got {
		names = append(names, s.Name+":"+s.Strategy)
	}
	if strings.Join(names, " ") != "docs:files keep:sqlite ledger:sqlite pg:postgres romm:sqlite" {
		t.Fatalf("got %v", names)
	}
	for _, s := range got {
		if s.Name == "romm" && (len(s.Excludes) != 1 || !s.Discovered || s.Path != filepath.Join(root, "romm")) {
			t.Fatalf("romm: %+v", s)
		}
	}

	c.Sources = append(c.Sources, Source{Name: "missing"})
	if _, err := c.Resolve(os.ReadDir); err == nil {
		t.Fatal("a source with no folder and no path resolved")
	}
}

func TestExcluded(t *testing.T) {
	pats := []string{"/library", "/config/index-v2", "*.log", "cache/"}
	for _, tc := range []struct {
		rel  string
		dir  bool
		want bool
	}{
		{"library", true, true},
		{"assets/library", true, false},
		{"config/index-v2", true, true},
		{"index-v2", true, false},
		{"app.log", false, true},
		{"a/b/app.log", false, true},
		{"cache", true, true},
		{"cache", false, false},
		{"a/cache", true, true},
		{"data.db", false, false},
	} {
		if got := Excluded(pats, tc.rel, tc.dir); got != tc.want {
			t.Errorf("%s (dir %v): got %v", tc.rel, tc.dir, got)
		}
	}
	if !Excluded([]string{Literal("we[ird]*.db")}, "we[ird]*.db", false) {
		t.Fatal("Literal doesn't match its own path")
	}
	if Excluded([]string{Literal("a*.db")}, "abc.db", false) {
		t.Fatal("Literal matches like a glob")
	}
}

func TestRetire(t *testing.T) {
	if _, err := Parse([]byte("retire: [{path: /data/x, after: 9 Jan}]")); err == nil {
		t.Fatal("a bad date passed")
	}
	if _, err := Parse([]byte("retire: [{path: data/x, after: 2027-01-09}]")); err == nil {
		t.Fatal("a relative path passed")
	}
	r := Retire{Path: "/x", After: "2027-01-09"}
	day := time.Date(2027, 1, 9, 0, 30, 0, 0, time.Local)
	if r.Due(day.Add(-time.Hour)) || !r.Due(day) {
		t.Fatal("due from the start of the day")
	}
}

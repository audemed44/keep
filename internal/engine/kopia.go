package engine

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kopia drives the kopia CLI inside the container where Kopia's server
// runs, so it uses that container's repository connection and password,
// and snapshots keep the existing source identity (user@hostname:path).
// It reads the CLI's --json output, not the undocumented API behind
// KopiaUI.
type Kopia struct {
	Exec      Execer
	Container string
	// ConfigFile, when set, connects to another repository than the
	// container's own (the local one), with its cache in Cache.
	ConfigFile, Cache string
}

func (k *Kopia) Name() string { return "kopia" }

// argv is the command line for args. For another repository than the
// container's own it adds the config file, and the cache through
// KOPIA_CACHE_DIRECTORY: Kopia takes that variable over the cache folder
// in the config file, and the container sets it for its own repository.
// Two repositories sharing a cache mix up their format, index and
// own-writes caches, so one reads (and writes) as if it were the other.
func (k *Kopia) argv(args ...string) []string {
	if k.ConfigFile == "" {
		return append([]string{"kopia"}, args...)
	}
	cmd := append([]string{"env", "KOPIA_CACHE_DIRECTORY=" + k.Cache, "kopia"}, args...)
	return append(cmd, "--config-file="+k.ConfigFile)
}

func (k *Kopia) run(ctx context.Context, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := k.Exec.Exec(ctx, k.Container, k.argv(args...), &out)
	if err != nil {
		return nil, fmt.Errorf("kopia %s: %w", args[0]+" "+args[1], err)
	}
	return out.Bytes(), nil
}

// noInherit stands in for "no ignores". Kopia applies the ignore rules of
// a parent folder's policy to snapshots of folders inside it (anchored at
// the snapshot's root), unless the folder's policy has its own list, and
// it can't store an empty list. Without this, the staged copy of
// keep/keep.db was left out because the keep source ignores /keep.db.
const noInherit = "/.keep-sets-no-ignores"

// Policies reads every path's policy with one policy show, which prints
// one JSON document per path, in the order given.
func (k *Kopia) Policies(ctx context.Context, paths []string) ([]Current, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	raw, err := k.run(ctx, append(append([]string{"policy", "show"}, paths...), "--json")...)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	out := make([]Current, 0, len(paths))
	for range paths {
		var p struct {
			Retention struct {
				KeepLatest  *int `json:"keepLatest"`
				KeepHourly  *int `json:"keepHourly"`
				KeepDaily   *int `json:"keepDaily"`
				KeepWeekly  *int `json:"keepWeekly"`
				KeepMonthly *int `json:"keepMonthly"`
				KeepAnnual  *int `json:"keepAnnual"`
			} `json:"retention"`
			Files struct {
				Ignore []string `json:"ignore"`
			} `json:"files"`
			Scheduling struct {
				Manual bool `json:"manual"`
			} `json:"scheduling"`
		}
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("kopia policy show: %d of %d policies read: %w", len(out), len(paths), err)
		}
		c := Current{Ignores: p.Files.Ignore, Manual: p.Scheduling.Manual}
		r := p.Retention
		if r.KeepLatest != nil && r.KeepHourly != nil && r.KeepDaily != nil && r.KeepWeekly != nil &&
			r.KeepMonthly != nil && r.KeepAnnual != nil {
			c.Retention = &Retention{Latest: *r.KeepLatest, Hourly: *r.KeepHourly, Daily: *r.KeepDaily,
				Weekly: *r.KeepWeekly, Monthly: *r.KeepMonthly, Annual: *r.KeepAnnual}
		}
		out = append(out, c)
	}
	return out, nil
}

func (k *Kopia) Configure(ctx context.Context, path string, cur Current, pol Policy) error {
	ignores := pol.Ignores
	if len(ignores) == 0 {
		ignores = []string{noInherit}
	}
	args := []string{"policy", "set", path}
	if !cur.Manual {
		args = append(args, "--manual")
	}
	for _, ig := range cur.Ignores {
		if !slices.Contains(ignores, ig) {
			args = append(args, "--remove-ignore="+ig)
		}
	}
	for _, ig := range ignores {
		if !slices.Contains(cur.Ignores, ig) {
			args = append(args, "--add-ignore="+ig)
		}
	}
	// policy show reports inherited retention too, so it can't tell
	// whether the path has its own. Whenever anything changes, all six are
	// set, so a path Keep has configured always carries Keep's retention.
	if len(args) > 3 || cur.Retention == nil || *cur.Retention != pol.Retention {
		r := pol.Retention
		args = append(args, fmt.Sprintf("--keep-latest=%d", r.Latest), fmt.Sprintf("--keep-hourly=%d", r.Hourly),
			fmt.Sprintf("--keep-daily=%d", r.Daily), fmt.Sprintf("--keep-weekly=%d", r.Weekly),
			fmt.Sprintf("--keep-monthly=%d", r.Monthly), fmt.Sprintf("--keep-annual=%d", r.Annual))
	}
	if len(args) == 3 {
		return nil // already as wanted
	}
	_, err := k.run(ctx, args...)
	return err
}

func (k *Kopia) Snapshot(ctx context.Context, paths []string, description string) (map[string]Snapshot, error) {
	var out bytes.Buffer
	args := append([]string{"snapshot", "create"}, paths...)
	err := k.Exec.Exec(ctx, k.Container, k.argv(append(args, "--json", "--description="+description)...), &out)
	// One manifest per line for each path that worked, even when others
	// failed (then the exit status is 1 and stderr says which).
	got := map[string]Snapshot{}
	dec := json.NewDecoder(&out)
	for {
		var m manifest
		if dec.Decode(&m) != nil {
			break
		}
		if m.ID == "" {
			continue
		}
		got[m.Source.Path] = m.snapshot()
	}
	switch {
	case err != nil:
		return got, fmt.Errorf("kopia snapshot create: %w", err)
	case len(got) < len(paths):
		return got, fmt.Errorf("kopia snapshot create: no snapshot for %d of %d paths; output %q", len(paths)-len(got), len(paths), clip(out.Bytes()))
	}
	return got, nil
}

// manifest is a snapshot as kopia's --json prints it.
type manifest struct {
	ID     string `json:"id"`
	Source struct {
		Path string `json:"path"`
	} `json:"source"`
	Description string    `json:"description"`
	StartTime   time.Time `json:"startTime"`
	EndTime     time.Time `json:"endTime"`
	RootEntry   struct {
		Summ struct {
			Size      int64 `json:"size"`
			Files     int64 `json:"files"`
			NumFailed int64 `json:"numFailed"`
		} `json:"summ"`
	} `json:"rootEntry"`
}

func (m manifest) snapshot() Snapshot {
	return Snapshot{
		ID: m.ID, Path: m.Source.Path, Description: m.Description, Start: m.StartTime, End: m.EndTime,
		Size: m.RootEntry.Summ.Size, Files: m.RootEntry.Summ.Files, Errors: m.RootEntry.Summ.NumFailed,
	}
}

func (k *Kopia) List(ctx context.Context) ([]Snapshot, error) {
	raw, err := k.run(ctx, "snapshot", "list", "--all", "--json")
	if err != nil {
		return nil, err
	}
	var list []manifest
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("kopia snapshot list: %w", err)
	}
	out := make([]Snapshot, 0, len(list))
	for _, m := range list {
		out = append(out, m.snapshot())
	}
	return out, nil
}

// Verify runs snapshot verify. It prints progress lines and then a summary
// with the errors as JSON on stdout, and exits 1 when it found any.
func (k *Kopia) Verify(ctx context.Context, percent int) (Verified, error) {
	var out bytes.Buffer
	err := k.Exec.Exec(ctx, k.Container, k.argv("snapshot", "verify",
		fmt.Sprintf("--verify-files-percent=%d", percent), "--json"), &out)
	var sum struct {
		Stats *struct {
			ProcessedObjectCount int64 `json:"processedObjectCount"`
			ReadFileCount        int64 `json:"readFileCount"`
			ReadBytes            int64 `json:"readBytes"`
		} `json:"stats"`
		ErrorCount   int      `json:"errorCount"`
		ErrorStrings []string `json:"errorStrings"`
	}
	// The summary is the last JSON line that has stats; progress lines
	// come before it.
	found := false
	for _, line := range bytes.Split(out.Bytes(), []byte("\n")) {
		var doc struct {
			Stats        json.RawMessage `json:"stats"`
			ErrorCount   int             `json:"errorCount"`
			ErrorStrings []string        `json:"errorStrings"`
		}
		if json.Unmarshal(bytes.TrimSpace(line), &doc) != nil || doc.Stats == nil {
			continue
		}
		if json.Unmarshal(line, &sum) == nil {
			found = true
		}
	}
	if !found {
		if err == nil {
			err = errors.New("no summary in its output")
		}
		return Verified{}, fmt.Errorf("kopia snapshot verify: %w", err)
	}
	v := Verified{Objects: sum.Stats.ProcessedObjectCount, Files: sum.Stats.ReadFileCount, Bytes: sum.Stats.ReadBytes,
		ErrorCount: sum.ErrorCount, Errors: sum.ErrorStrings}
	if len(v.Errors) > 50 {
		v.Errors = v.Errors[:50]
	}
	if v.ErrorCount < len(sum.ErrorStrings) {
		v.ErrorCount = len(sum.ErrorStrings)
	}
	if err != nil && v.ErrorCount == 0 {
		return v, fmt.Errorf("kopia snapshot verify: %w", err)
	}
	return v, nil
}

func (k *Kopia) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := k.run(ctx, append(append([]string{"snapshot", "delete"}, ids...), "--delete")...)
	return err
}

// Open connects to the filesystem repository in dir through the config
// file, creating the repository when the folder is empty. The password is
// the container's (KOPIA_PASSWORD), the same as its own repository's.
// Connecting is remembered in the config file, so this is a status call
// (and a read of the folder's format file) after the first time.
//
// Every time, it checks that the repository Kopia opened is the one in the
// folder: the unique ID it reports must be the one in the folder's format
// file. Anything else (a shared cache, the wrong config) is refused before
// anything is written.
func (k *Kopia) Open(ctx context.Context, dir string, create bool) error {
	if k.ConfigFile == "" || k.Cache == "" {
		return errors.New("kopia: no config file or cache for the local repository")
	}
	st, err := k.status(ctx)
	switch {
	case err == nil && st.Storage.Config.Path == dir:
		return k.sameRepository(ctx, dir, st.UniqueIDHex)
	case err == nil:
		// Connected to another folder (the setting changed).
		if _, err := k.run(ctx, "repository", "disconnect"); err != nil {
			return err
		}
	}
	verb := "connect"
	if create {
		verb = "create"
	}
	if _, err := k.run(ctx, "repository", verb, "filesystem", "--path="+dir, "--cache-directory="+k.Cache); err != nil {
		return err
	}
	if st, err = k.status(ctx); err != nil {
		return err
	}
	return k.sameRepository(ctx, dir, st.UniqueIDHex)
}

type kopiaStatus struct {
	UniqueIDHex string `json:"uniqueIDHex"`
	Storage     struct {
		Config struct {
			Path string `json:"path"`
		} `json:"config"`
	} `json:"storage"`
}

func (k *Kopia) status(ctx context.Context) (kopiaStatus, error) {
	var st kopiaStatus
	out, err := k.run(ctx, "repository", "status", "--json")
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return st, fmt.Errorf("kopia repository status: %w", err)
	}
	return st, nil
}

// sameRepository checks the unique ID Kopia reports against the format
// file in dir (plain JSON; the ID is base64).
func (k *Kopia) sameRepository(ctx context.Context, dir, reported string) error {
	var raw bytes.Buffer
	if err := k.Exec.Exec(ctx, k.Container, []string{"cat", dir + "/kopia.repository.f"}, &raw); err != nil {
		return fmt.Errorf("reading the repository's format file: %w", err)
	}
	var f struct {
		UniqueID []byte `json:"uniqueID"`
	}
	if err := json.Unmarshal(raw.Bytes(), &f); err != nil || len(f.UniqueID) == 0 {
		return fmt.Errorf("the format file in %s has no unique ID", dir)
	}
	if onDisk := hex.EncodeToString(f.UniqueID); onDisk != reported {
		return fmt.Errorf("kopia opened another repository (%.12s) than the one in %s (%.12s): refusing to use it", reported, dir, onDisk)
	}
	return nil
}

// Restore restores in parallel: over rclone each file is a request to
// Drive (about 5 s), so a one-at-a-time restore of a thousand files takes
// over an hour. Owners are left to the caller (kopia runs as root).
func (k *Kopia) Restore(ctx context.Context, id, subpath, target string) error {
	src := id
	if subpath = strings.Trim(subpath, "/"); subpath != "" {
		src += "/" + subpath
	}
	_, err := k.run(ctx, "snapshot", "restore", src, target, "--parallel=32", "--skip-owners")
	if err != nil && strings.Contains(err.Error(), "entry not found") {
		return fmt.Errorf("%s: %w", subpath, ErrNotInSnapshot)
	}
	return err
}

// The repository's size from content stats, which reads the local index
// cache (listing every blob on Drive took 45 minutes): Total Packed (after
// compression), or Total Bytes when nothing was compressed.
var (
	totalPacked = regexp.MustCompile(`(?m)^Total Packed:\s*(\d+)`)
	totalBytes  = regexp.MustCompile(`(?m)^Total Bytes:\s*(\d+)`)
)

func (k *Kopia) Stats(ctx context.Context) (Stats, error) {
	raw, err := k.run(ctx, "content", "stats", "--raw")
	if err != nil {
		return Stats{}, err
	}
	m := totalPacked.FindSubmatch(raw)
	if m == nil {
		m = totalBytes.FindSubmatch(raw)
	}
	if m == nil {
		return Stats{}, fmt.Errorf("kopia content stats: unexpected output %q", clip(raw))
	}
	n, _ := strconv.ParseInt(string(m[1]), 10, 64)
	return Stats{Size: n}, nil
}

func clip(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}

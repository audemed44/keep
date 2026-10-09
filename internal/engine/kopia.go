package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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
}

func (k *Kopia) Name() string { return "kopia" }

func (k *Kopia) run(ctx context.Context, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := k.Exec.Exec(ctx, k.Container, append([]string{"kopia"}, args...), &out)
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

func (k *Kopia) Configure(ctx context.Context, path string, pol Policy) error {
	ignores := pol.Ignores
	if len(ignores) == 0 {
		ignores = []string{noInherit}
	}
	raw, err := k.run(ctx, "policy", "show", path, "--json")
	if err != nil {
		return err
	}
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
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("kopia policy show: %w", err)
	}
	args := []string{"policy", "set", path}
	if !p.Scheduling.Manual {
		args = append(args, "--manual")
	}
	for _, ig := range p.Files.Ignore {
		if !slices.Contains(ignores, ig) {
			args = append(args, "--remove-ignore="+ig)
		}
	}
	for _, ig := range ignores {
		if !slices.Contains(p.Files.Ignore, ig) {
			args = append(args, "--add-ignore="+ig)
		}
	}
	// policy show reports inherited retention too, so it can't tell
	// whether the path has its own. Whenever anything changes, all six are
	// set, so a path Keep has configured always carries Keep's retention.
	r, cur := pol.Retention, p.Retention
	var keep []string
	same := true
	for _, x := range []struct {
		flag string
		have *int
		want int
	}{
		{"keep-latest", cur.KeepLatest, r.Latest}, {"keep-hourly", cur.KeepHourly, r.Hourly},
		{"keep-daily", cur.KeepDaily, r.Daily}, {"keep-weekly", cur.KeepWeekly, r.Weekly},
		{"keep-monthly", cur.KeepMonthly, r.Monthly}, {"keep-annual", cur.KeepAnnual, r.Annual},
	} {
		keep = append(keep, fmt.Sprintf("--%s=%d", x.flag, x.want))
		same = same && x.have != nil && *x.have == x.want
	}
	if !same || len(args) > 3 {
		args = append(args, keep...)
	}
	if len(args) == 3 {
		return nil // already as wanted
	}
	_, err = k.run(ctx, args...)
	return err
}

func (k *Kopia) Snapshot(ctx context.Context, paths []string, description string) (map[string]Snapshot, error) {
	var out bytes.Buffer
	cmd := append([]string{"kopia", "snapshot", "create"}, paths...)
	cmd = append(cmd, "--json", "--description="+description)
	err := k.Exec.Exec(ctx, k.Container, cmd, &out)
	// One manifest per line for each path that worked, even when others
	// failed (then the exit status is 1 and stderr says which).
	got := map[string]Snapshot{}
	dec := json.NewDecoder(&out)
	for {
		var m struct {
			ID     string `json:"id"`
			Source struct {
				Path string `json:"path"`
			} `json:"source"`
			StartTime time.Time `json:"startTime"`
			EndTime   time.Time `json:"endTime"`
			RootEntry struct {
				Summ struct {
					Size      int64 `json:"size"`
					Files     int64 `json:"files"`
					NumFailed int64 `json:"numFailed"`
				} `json:"summ"`
			} `json:"rootEntry"`
		}
		if dec.Decode(&m) != nil {
			break
		}
		if m.ID == "" {
			continue
		}
		got[m.Source.Path] = Snapshot{
			ID: m.ID, Path: m.Source.Path, Start: m.StartTime, End: m.EndTime,
			Size: m.RootEntry.Summ.Size, Files: m.RootEntry.Summ.Files, Errors: m.RootEntry.Summ.NumFailed,
		}
	}
	switch {
	case err != nil:
		return got, fmt.Errorf("kopia snapshot create: %w", err)
	case len(got) < len(paths):
		return got, fmt.Errorf("kopia snapshot create: no snapshot for %d of %d paths; output %q", len(paths)-len(got), len(paths), clip(out.Bytes()))
	}
	return got, nil
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

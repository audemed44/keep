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

func (k *Kopia) Configure(ctx context.Context, path string, ignores []string) error {
	if len(ignores) == 0 {
		ignores = []string{noInherit}
	}
	raw, err := k.run(ctx, "policy", "show", path, "--json")
	if err != nil {
		return err
	}
	var p struct {
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
	if len(args) == 3 {
		return nil // already as wanted
	}
	_, err = k.run(ctx, args...)
	return err
}

func (k *Kopia) Snapshot(ctx context.Context, path, description string) (Snapshot, error) {
	raw, err := k.run(ctx, "snapshot", "create", path, "--json", "--description="+description)
	if err != nil {
		return Snapshot{}, err
	}
	var m struct {
		ID        string    `json:"id"`
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
	if err := json.Unmarshal(raw, &m); err != nil || m.ID == "" {
		return Snapshot{}, fmt.Errorf("kopia snapshot create: unexpected output %q", clip(raw))
	}
	return Snapshot{
		ID: m.ID, Path: path, Start: m.StartTime, End: m.EndTime,
		Size: m.RootEntry.Summ.Size, Files: m.RootEntry.Summ.Files, Errors: m.RootEntry.Summ.NumFailed,
	}, nil
}

var blobTotal = regexp.MustCompile(`(?m)^Total:\s*(\d+)\s*$`)

func (k *Kopia) Stats(ctx context.Context) (Stats, error) {
	raw, err := k.run(ctx, "blob", "stats", "--raw")
	if err != nil {
		return Stats{}, err
	}
	m := blobTotal.FindSubmatch(raw)
	if m == nil {
		return Stats{}, fmt.Errorf("kopia blob stats: unexpected output %q", clip(raw))
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

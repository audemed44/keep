package runner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/audemed44/keep/internal/engine"
	"github.com/audemed44/keep/internal/store"
)

// verifyTimeout bounds a verify: listing every blob on Drive alone can
// take most of an hour.
const verifyTimeout = 6 * time.Hour

// verify checks the repository: its structure, and a sample of the files
// read back. Then it refreshes Keep's copy of the snapshot list and the
// repository's size.
func (r *Runner) verify(ctx context.Context, id int64) (status, summary string) {
	cfg, err := r.Config()
	if err != nil {
		r.log(ctx, id, "error", "Reading the settings: %v", err)
		return "failed", "settings: " + err.Error()
	}
	eng := r.Engine(cfg)
	r.setCurrent("verifying the repository")
	r.log(ctx, id, "info", "Verifying the %s repository, reading back %d%% of the files", eng.Name(), cfg.Verify.Percent)
	vctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	v, err := eng.Verify(vctx, cfg.Verify.Percent)
	cancel()
	if err != nil {
		r.log(ctx, id, "error", "%v", err)
		return "failed", "the check couldn't run: " + firstLine(err.Error())
	}
	for _, e := range v.Errors {
		r.log(ctx, id, "error", "%s", e)
	}
	summary = fmt.Sprintf("%d objects checked, %d files (%s) read back", v.Objects, v.Files, Bytes(v.Bytes))
	r.log(ctx, id, "info", "%s", summary)
	status = "ok"
	if v.ErrorCount > 0 {
		status = "failed"
		summary = fmt.Sprintf("%d problems found; %s", v.ErrorCount, summary)
	}

	if r.listSnapshots(ctx, id, eng) != nil && status == "ok" {
		status = "warn"
	}
	r.setCurrent("the repository size")
	if st, err := eng.Stats(ctx); err != nil {
		r.log(ctx, id, "warn", "Reading the repository size: %v", err)
	} else if err := r.Store.Put(ctx, "repo", RepoInfo{Size: st.Size, At: time.Now()}); err != nil {
		slog.Warn("saving the repository size", "err", err)
	}
	return status, summary
}

// listSnapshots refreshes Keep's copy of the repository's snapshot list.
func (r *Runner) listSnapshots(ctx context.Context, id int64, eng engine.Engine) error {
	r.setCurrent("listing snapshots")
	list, err := eng.List(ctx)
	if err != nil {
		r.log(ctx, id, "warn", "Listing snapshots: %v", err)
		return err
	}
	out := make([]store.Snapshot, len(list))
	for i, s := range list {
		out[i] = store.Snapshot{ID: s.ID, Path: s.Path, Description: s.Description, Start: s.Start, End: s.End, Size: s.Size, Files: s.Files}
	}
	if err := r.Store.ReplaceSnapshots(ctx, out, time.Now()); err != nil {
		r.log(ctx, id, "warn", "Saving the snapshot list: %v", err)
		return err
	}
	r.log(ctx, id, "info", "The repository holds %d snapshots", len(list))
	return nil
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

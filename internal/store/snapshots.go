package store

import (
	"context"
	"strings"
	"time"
)

// The repository's snapshots as the engine listed them last (in the weekly
// verify): listing costs an engine call, so pages read this copy.

type Snapshot struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	Description string    `json:"description,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Size        int64     `json:"size"`
	Files       int64     `json:"files"`
}

// ReplaceSnapshots stores a fresh listing in place of the old one.
func (s *Store) ReplaceSnapshots(ctx context.Context, list []Snapshot, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM snapshots`); err != nil {
		return err
	}
	for _, sn := range list {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO snapshots (id, path, description, start, end, size, files) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			sn.ID, sn.Path, sn.Description, ms(sn.Start), ms(sn.End), sn.Size, sn.Files); err != nil {
			return err
		}
	}
	b := `{"at":"` + at.Format(time.RFC3339Nano) + `"}`
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES ('snapshots-listed', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, b); err != nil {
		return err
	}
	return tx.Commit()
}

// SnapshotsListed is when the listing was taken (zero: never).
func (s *Store) SnapshotsListed(ctx context.Context) (time.Time, error) {
	var v struct {
		At time.Time `json:"at"`
	}
	err := s.Get(ctx, "snapshots-listed", &v)
	return v.At, err
}

// Snapshots lists the snapshots of the given paths (all when none),
// newest first.
func (s *Store) Snapshots(ctx context.Context, paths ...string) ([]Snapshot, error) {
	q := `SELECT id, path, description, start, end, size, files FROM snapshots`
	var args []any
	if len(paths) > 0 {
		q += ` WHERE path IN (?` + strings.Repeat(",?", len(paths)-1) + `)`
		for _, p := range paths {
			args = append(args, p)
		}
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY start DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		var sn Snapshot
		var start, end int64
		if err := rows.Scan(&sn.ID, &sn.Path, &sn.Description, &start, &end, &sn.Size, &sn.Files); err != nil {
			return nil, err
		}
		sn.Start, sn.End = fromMS(start), fromMS(end)
		out = append(out, sn)
	}
	return out, rows.Err()
}

// DeleteSnapshots drops snapshots the engine deleted.
func (s *Store) DeleteSnapshots(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

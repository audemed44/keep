// Package store keeps Keep's state in SQLite.
//
// The schema is created with CREATE ... IF NOT EXISTS; later changes go in
// migrations, which run once each in order and are recorded in
// PRAGMA user_version.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure Go, so the build stays static
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	started  INTEGER NOT NULL,
	finished INTEGER NOT NULL DEFAULT 0,
	trigger  TEXT NOT NULL,              -- schedule, manual or foyer
	status   TEXT NOT NULL,              -- running, ok, warn or failed
	summary  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS run_sources (
	run_id    INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	name      TEXT NOT NULL,
	strategy  TEXT NOT NULL,
	status    TEXT NOT NULL,             -- running, ok, warn or failed
	started   INTEGER NOT NULL DEFAULT 0,
	finished  INTEGER NOT NULL DEFAULT 0,
	size      INTEGER NOT NULL DEFAULT 0, -- bytes in the snapshots
	files     INTEGER NOT NULL DEFAULT 0,
	databases INTEGER NOT NULL DEFAULT 0, -- databases copied or dumped
	message   TEXT NOT NULL DEFAULT '',
	snapshots TEXT NOT NULL DEFAULT '[]', -- JSON: the engine's snapshot ids
	PRIMARY KEY (run_id, name)
);
CREATE INDEX IF NOT EXISTS run_sources_name ON run_sources (name, run_id);
CREATE TABLE IF NOT EXISTS run_log (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	at     INTEGER NOT NULL,
	level  TEXT NOT NULL,                -- info, warn or error
	text   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS run_log_run ON run_log (run_id, id);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// migrations run after the schema, once each: append, never edit or
// reorder. migrations[i] moves the database to user_version i+1.
var migrations = []string{}

// Open opens (or creates) the database.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-1024)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: writes come from one process, and every open SQLite
	// connection holds its own page cache. Closed when idle for a while.
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("database migration: %w", err)
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("%d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

// Get reads a JSON setting into v; a missing key leaves v as it was.
func (s *Store) Get(ctx context.Context, key string, v any) error {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), v)
}

// Put stores v as a JSON setting.
func (s *Store) Put(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, string(b))
	return err
}

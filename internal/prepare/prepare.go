// Package prepare makes a source consistent before its snapshot: it finds
// SQLite databases and copies them with VACUUM INTO, and dumps Postgres
// and MariaDB through their containers. Results go to the staging folder;
// Keep never writes to an app's files.
package prepare

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/engine"

	_ "modernc.org/sqlite"
)

var sqliteHeader = []byte("SQLite format 3\x00")

// FindSQLite lists the SQLite databases under root (relative,
// slash-separated), going by the file header rather than the name. It
// skips excluded paths, symlinks and the folder skip (staging).
func FindSQLite(root string, excludes []string, skip string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // unreadable entries are the engine's to report
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != root && (p == skip || config.Excluded(excludes, rel, true)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || config.Excluded(excludes, rel, false) {
			return nil
		}
		if isSQLite(p) {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

func isSQLite(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [16]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return bytes.Equal(hdr[:], sqliteHeader)
}

// LiveFiles are the files to leave out of a snapshot when a database at
// rel has been copied to staging: the database and its journals.
func LiveFiles(rel string) []string {
	return []string{rel, rel + "-wal", rel + "-shm", rel + "-journal"}
}

// CopySQLite copies a database to dest with VACUUM INTO: a consistent
// copy even while the app writes, through SQLite's own locking. The
// source is opened read-only. The copy is checked with quick_check.
func CopySQLite(ctx context.Context, src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	err := vacuumInto(ctx, src, dest, "mode=ro&_pragma=busy_timeout(30000)")
	if err != nil && !exists(src+"-wal") {
		// A WAL database needs to write its -shm file even to read, which
		// fails in a folder Keep can't write to. Without a -wal file no
		// connection has it open, so reading it as immutable is safe.
		if err2 := vacuumInto(ctx, src, dest, "mode=ro&immutable=1"); err2 == nil {
			err = nil
		}
	}
	if err != nil {
		return err
	}

	cp, err := sql.Open("sqlite", "file:"+escapeURI(dest)+"?mode=ro")
	if err != nil {
		return err
	}
	defer cp.Close()
	var result string
	if err := cp.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("checking the copy: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("the copy failed quick_check: %s", result)
	}
	return nil
}

func vacuumInto(ctx context.Context, src, dest, params string) error {
	_ = os.Remove(dest) // VACUUM INTO refuses an existing file
	db, err := sql.Open("sqlite", "file:"+escapeURI(src)+"?"+params)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		os.Remove(dest)
		return fmt.Errorf("VACUUM INTO: %w", err)
	}
	return nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// escapeURI escapes the characters that end a path in a file: URI.
func escapeURI(p string) string {
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(p)
}

// Default dump commands, run with sh -c in the database's container. They
// read the credentials the official images are started with, so Keep
// never holds them. Plain SQL dumps deduplicate well between runs.
const (
	postgresDump = `pg_dumpall --clean --if-exists -U "${POSTGRES_USER:-postgres}"`
	mariadbDump  = `MYSQL_PWD="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}" ` +
		`"$(command -v mariadb-dump || command -v mysqldump)" -uroot --all-databases ` +
		`--single-transaction --routines --events --triggers`
)

// DumpCommand is the command that writes a source's dump to stdout.
func DumpCommand(s config.Source) string {
	if s.Command != "" {
		return s.Command
	}
	if s.Strategy == config.MariaDB {
		return mariadbDump
	}
	return postgresDump
}

// Dump runs a source's dump command in its container and writes the
// output to dest. A failed dump leaves no file behind.
func Dump(ctx context.Context, ex engine.Execer, s config.Source, dest string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	cw := &countWriter{w: f}
	err = ex.Exec(ctx, s.Container, []string{"sh", "-c", DumpCommand(s)}, cw)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && cw.n == 0 {
		err = errors.New("the dump is empty")
	}
	if err != nil {
		os.Remove(dest)
		return 0, fmt.Errorf("dump in %s: %w", s.Container, err)
	}
	return cw.n, nil
}

// DumpName is the staged file a database dump goes to.
func DumpName(s config.Source) string {
	return path.Clean(s.Name + ".sql")
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

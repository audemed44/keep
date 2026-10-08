package prepare

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/audemed44/keep/internal/config"
	"github.com/audemed44/keep/internal/docker"
)

// walDB makes a WAL-mode database with n rows and keeps the connection
// open with checkpoints off, so the rows stay in the -wal file the way a
// running app leaves them.
func walDB(t *testing.T, p string, n int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+p+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE t (v TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if _, err := db.Exec(`INSERT INTO t VALUES (?)`, strings.Repeat("x", i)); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func count(t *testing.T, p string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFindSQLite(t *testing.T) {
	root := t.TempDir()
	walDB(t, filepath.Join(root, "app.db"), 1)
	os.MkdirAll(filepath.Join(root, "sub", "cache"), 0o755)
	os.MkdirAll(filepath.Join(root, "staging"), 0o755)
	walDB(t, filepath.Join(root, "sub", "data"), 1) // no extension: found by header
	walDB(t, filepath.Join(root, "sub", "cache", "c.db"), 1)
	walDB(t, filepath.Join(root, "staging", "s.db"), 1)
	os.WriteFile(filepath.Join(root, "fake.db"), []byte("not a database at all"), 0o644)
	os.Symlink(filepath.Join(root, "app.db"), filepath.Join(root, "link.db"))

	got, err := FindSQLite(root, []string{"cache/"}, filepath.Join(root, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"app.db", "sub/data"}) {
		t.Fatalf("got %v", got)
	}
}

func TestCopySQLiteWhileWriting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "app.db")
	db := walDB(t, src, 200)
	if info, err := os.Stat(src + "-wal"); err != nil || info.Size() == 0 {
		t.Fatal("the rows should still be in the WAL")
	}
	dest := filepath.Join(dir, "staging", "nested", "app.db")
	os.MkdirAll(filepath.Dir(dest), 0o755)
	os.WriteFile(dest, []byte("an old copy"), 0o600) // replaced
	if err := CopySQLite(context.Background(), src, dest); err != nil {
		t.Fatal(err)
	}
	if n := count(t, dest); n != 200 {
		t.Fatalf("the copy has %d rows", n)
	}
	if _, err := os.Stat(dest + "-wal"); err == nil {
		t.Fatal("the copy has a WAL; it should be one file")
	}
	// The app's connection still works.
	if _, err := db.Exec(`INSERT INTO t VALUES ('after')`); err != nil {
		t.Fatal(err)
	}
}

func TestCopySQLiteReadOnlyFolder(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "ro", "app.db")
	os.MkdirAll(filepath.Dir(src), 0o755)
	walDB(t, src, 5).Close() // the app isn't running: no -wal left
	os.Chmod(filepath.Dir(src), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Dir(src), 0o755) })
	dest := filepath.Join(dir, "out.db")
	if err := CopySQLite(context.Background(), src, dest); err != nil {
		t.Fatal(err)
	}
	if n := count(t, dest); n != 5 {
		t.Fatalf("the copy has %d rows", n)
	}
}

func TestCopySQLiteBroken(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bad.db")
	os.WriteFile(src, append([]byte("SQLite format 3\x00"), make([]byte, 100)...), 0o644)
	dest := filepath.Join(dir, "out.db")
	if err := CopySQLite(context.Background(), src, dest); err == nil {
		t.Fatal("a broken database copied")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("a failed copy left a file")
	}
}

func TestLiveFiles(t *testing.T) {
	if got := LiveFiles("a/b.db"); !slices.Equal(got, []string{"a/b.db", "a/b.db-wal", "a/b.db-shm", "a/b.db-journal"}) {
		t.Fatal(got)
	}
}

type fakeExec struct {
	out string
	err error
	cmd []string
}

func (f *fakeExec) Exec(_ context.Context, _ string, cmd []string, stdout io.Writer) error {
	f.cmd = cmd
	io.WriteString(stdout, f.out)
	return f.err
}

func TestDump(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "pg", "pg.sql")
	src := config.Source{Name: "pg", Strategy: config.Postgres, Container: "db"}
	f := &fakeExec{out: "CREATE TABLE x;\n"}
	n, err := Dump(context.Background(), f, src, dest)
	if err != nil || n != 16 {
		t.Fatalf("%d %v", n, err)
	}
	if f.cmd[0] != "sh" || !strings.Contains(f.cmd[2], "pg_dumpall") {
		t.Fatal(f.cmd)
	}
	info, _ := os.Stat(dest)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("dump mode %v: dumps hold app data", info.Mode())
	}

	f = &fakeExec{out: "partial", err: &docker.ExitError{Code: 1, Stderr: "access denied"}}
	if _, err := Dump(context.Background(), f, src, dest); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed dump left its file")
	}
	if _, err := Dump(context.Background(), &fakeExec{}, src, dest); err == nil {
		t.Fatal("an empty dump passed")
	}

	maria := config.Source{Name: "m", Strategy: config.MariaDB, Container: "db"}
	if !strings.Contains(DumpCommand(maria), "--single-transaction") {
		t.Fatal(DumpCommand(maria))
	}
	maria.Command = "custom"
	if DumpCommand(maria) != "custom" {
		t.Fatal("command override ignored")
	}
}

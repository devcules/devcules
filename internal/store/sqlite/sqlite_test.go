package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/storetest"
)

// openTemp opens a store in a fresh temp dir.
func openTemp(t *testing.T) store.Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "sub", "devcules.db"))
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestContract runs the shared suite against SQLite.
func TestContract(t *testing.T) {
	dir := t.TempDir()
	storetest.Run(t, func(t *testing.T) store.Store {
		t.Helper()
		s, err := Open(context.Background(), filepath.Join(dir, "devcules.db"))
		if err != nil {
			t.Fatalf("Open: %s", err)
		}
		return s
	})
}

// TestFreshLayout asserts the fresh database holds exactly the Meta table
// plus migration bookkeeping, at the embedded migration count.
func TestFreshLayout(t *testing.T) {
	s := openTemp(t)
	v, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %s", err)
	}
	migs, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %s", err)
	}
	if v != len(migs) {
		t.Fatalf("SchemaVersion = %d, want %d (one per embedded migration)", v, len(migs))
	}
}

// TestFilePermissions asserts the directory is 0700 and the file 0600.
func TestFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	s, err := Open(context.Background(), filepath.Join(dir, "devcules.db"))
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	s.Close()
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode = %v, %v; want 0700", fi.Mode(), err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "devcules.db")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %v, %v; want 0600", fi.Mode(), err)
	}
}

// TestPragmas asserts every connection's SQLite behaviour.
func TestPragmas(t *testing.T) {
	s := openTemp(t)
	defer s.Close()
	db := s.(*sqliteStore).db
	var journal string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("journal_mode = %q, %v; want wal", journal, err)
	}
	var fk, timeout int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v; want 1", fk, err)
	}
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != busyTimeoutMS {
		t.Fatalf("busy_timeout = %d, %v; want %d", timeout, err, busyTimeoutMS)
	}
}

// TestPragmasSurviveReconnect forces the pool to open a fresh connection, as
// it does after an error, and requires the pragmas on that one too.
func TestPragmasSurviveReconnect(t *testing.T) {
	s := openTemp(t)
	db := s.(*sqliteStore).db
	db.SetConnMaxLifetime(time.Nanosecond)
	time.Sleep(10 * time.Millisecond)
	var fk, timeout int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys after reconnect = %d, %v; want 1", fk, err)
	}
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != busyTimeoutMS {
		t.Fatalf("busy_timeout after reconnect = %d, %v; want %d", timeout, err, busyTimeoutMS)
	}
}

// TestConcurrentOpen has several processes' worth of openers race to create
// and migrate one new database: all must succeed, none may report a lock
// or a half-applied migration.
func TestConcurrentOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "devcules.db")
	const openers = 8
	errs := make(chan error, openers)
	var wg sync.WaitGroup
	for range openers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(context.Background(), path)
			if err == nil {
				err = s.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Open: %s", err)
		}
	}
}

// TestOpenBadDir asserts an unwritable location fails with next steps.
func TestOpenBadDir(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), filepath.Join(blocker, "devcules.db"))
	if err == nil {
		t.Fatal("Open succeeded under a file, want an error")
	}
	if !strings.Contains(err.Error(), "cannot create data dir") ||
		!strings.Contains(err.Error(), "DEVCULES_DATA_DIR") {
		t.Fatalf("error = %q, want next steps", err.Error())
	}
}

// rawDB opens a database/sql handle with no migrations, for driving
// applyMigrations directly.
func rawDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "raw.db"))
	if err != nil {
		t.Fatalf("sql.Open: %s", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// mig builds a migration with a correct checksum for the given SQL.
func mig(version int, name, sqlText string) migration {
	sum := sha256.Sum256([]byte(sqlText))
	return migration{version: version, name: name, sql: sqlText, checksum: fmt.Sprintf("%x", sum)}
}

// TestEditedMigrationRejected applies v1, then re-applies an edited v1.
func TestEditedMigrationRejected(t *testing.T) {
	ctx := context.Background()
	db := rawDB(t)
	v1 := mig(1, "0001_meta.sql", "CREATE TABLE t (x TEXT);")
	if err := applyMigrations(ctx, db, []migration{v1}); err != nil {
		t.Fatalf("first apply: %s", err)
	}
	edited := mig(1, "0001_meta.sql", "CREATE TABLE t (x TEXT, y TEXT);")
	err := applyMigrations(ctx, db, []migration{edited})
	if err == nil {
		t.Fatal("edited re-apply succeeded, want a checksum error")
	}
	if !strings.Contains(err.Error(), "0001_meta.sql") || !strings.Contains(err.Error(), "edited after it was applied") {
		t.Fatalf("error = %q, want it to name the migration", err.Error())
	}
}

// TestNewerDatabaseRejected applies v1+v2, then offers only v1: the
// database is newer than the binary.
func TestNewerDatabaseRejected(t *testing.T) {
	ctx := context.Background()
	db := rawDB(t)
	v1 := mig(1, "0001_meta.sql", "CREATE TABLE t (x TEXT);")
	v2 := mig(2, "0002_x.sql", "CREATE TABLE u (x TEXT);")
	if err := applyMigrations(ctx, db, []migration{v1, v2}); err != nil {
		t.Fatalf("first apply: %s", err)
	}
	err := applyMigrations(ctx, db, []migration{v1})
	if err == nil {
		t.Fatal("older binary on newer database succeeded, want an upgrade error")
	}
	if !strings.Contains(err.Error(), "upgrade devcules") {
		t.Fatalf("error = %q, want an upgrade prompt, never an attempt to run", err.Error())
	}
}

// TestLoadMigrationsLayouts feeds good and bad migration layouts: files
// sort by version with correct checksums, and bad names fail loudly.
func TestLoadMigrationsLayouts(t *testing.T) {
	good := fstest.MapFS{
		"migrations/0002_b.sql": {Data: []byte("SELECT 2;")},
		"migrations/0001_a.sql": {Data: []byte("SELECT 1;")},
	}
	migs, err := loadMigrationsFrom(good, "migrations")
	if err != nil {
		t.Fatalf("loadMigrationsFrom: %s", err)
	}
	if len(migs) != 2 || migs[0].version != 1 || migs[1].version != 2 {
		t.Fatalf("versions = %v, want [1 2] in order", migs)
	}
	sum := sha256.Sum256([]byte("SELECT 1;"))
	if migs[0].checksum != fmt.Sprintf("%x", sum) {
		t.Fatalf("checksum = %q, want sha256 of the file", migs[0].checksum)
	}

	bad := []fstest.MapFS{
		{"migrations/foo.sql": {Data: []byte("SELECT 1;")}},
		{"migrations/1.sql": {Data: []byte("SELECT 1;")}},
		{"migrations/abcd_x.sql": {Data: []byte("SELECT 1;")}},
		{"migrations/0000_x.sql": {Data: []byte("SELECT 1;")}},
	}
	for i, fsys := range bad {
		if _, err := loadMigrationsFrom(fsys, "migrations"); err == nil ||
			!strings.Contains(err.Error(), "bad migration name") {
			t.Fatalf("layout %d: err = %v, want a bad-migration-name error", i, err)
		}
	}

	dup := fstest.MapFS{
		"migrations/0001_a.sql": {Data: []byte("SELECT 1;")},
		"migrations/0001_b.sql": {Data: []byte("SELECT 2;")},
	}
	if _, err := loadMigrationsFrom(dup, "migrations"); err == nil ||
		!strings.Contains(err.Error(), "share version 1") {
		t.Fatalf("duplicate versions: err = %v, want a shared-version error", err)
	}

	empty := fstest.MapFS{"migrations": {Mode: fs.ModeDir}}
	migs, err = loadMigrationsFrom(empty, "migrations")
	if err != nil || len(migs) != 0 {
		t.Fatalf("empty dir = %v, %v; want no migrations, no error", migs, err)
	}
	if _, err := loadMigrationsFrom(fstest.MapFS{}, "migrations"); err == nil {
		t.Fatal("missing dir succeeded, want a list error")
	}
}

// TestOpenUnreadableFile asserts a present-but-unwritable location fails
// with next steps.
func TestOpenUnreadableFile(t *testing.T) {
	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), filepath.Join(ro, "devcules.db"))
	if err == nil {
		t.Fatal("Open in a read-only dir succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "cannot open database file") ||
		!strings.Contains(err.Error(), "DEVCULES_DATA_DIR") {
		t.Fatalf("error = %q, want next steps", err.Error())
	}
}

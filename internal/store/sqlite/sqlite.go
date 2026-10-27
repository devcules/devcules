package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// busyTimeoutMS is how long SQLite waits on a locked page before failing.
// Single-writer traffic retries inside this window instead of erroring out.
const busyTimeoutMS = 5000

// migration is one numbered .sql file with its content checksum.
type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

// sqliteStore is a store.Store on one SQLite file.
type sqliteStore struct {
	db *sql.DB
}

// Open connects to the SQLite database at path, creating the directory
// (0700) and the file (0600) when missing, setting pragmas, and applying
// pending migrations. path is a filesystem path, never a URL, so there is
// nothing secret to log. The Postgres URL is deliberately not handled
// here; that wiring belongs to a later module.
func Open(ctx context.Context, path string) (store.Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create data dir %s: set DEVCULES_DATA_DIR to a writable path (%s)", dir, err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open database file %s: set DEVCULES_DATA_DIR to a writable path (%s)", path, err)
	}
	f.Close()

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("cannot open database %s: %s", path, err)
	}
	// One connection: pragmas set below hold for every statement, and
	// M0.3 serializes writers instead of tuning a pool it cannot yet fill.
	db.SetMaxOpenConns(1)
	s := &sqliteStore{db: db}

	migs, err := loadMigrations()
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := retryBusy(ctx, func() error { return applyMigrations(ctx, db, migs) }); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// dsn makes the pragmas hold on every connection the pool opens, not just
// the first: busy_timeout comes first so WAL setup itself waits on a lock.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	return (&url.URL{Scheme: "file", Opaque: filepath.ToSlash(path), RawQuery: q.Encode()}).String()
}

// Ping verifies the database answers.
func (s *sqliteStore) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: ping failed: %w", err)
	}
	return nil
}

// Close disconnects. The file keeps everything; reopening is a no-op.
func (s *sqliteStore) Close() error {
	return s.db.Close()
}

// SchemaVersion returns the highest applied migration version.
func (s *sqliteStore) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: cannot read schema version: %w", err)
	}
	return v, nil
}

// Meta returns the key/value repo.
func (s *sqliteStore) Meta() store.MetaRepo {
	return metaRepo{db: s.db}
}

// metaRepo implements store.MetaRepo on the meta table.
type metaRepo struct {
	db *sql.DB
}

// Get returns the value for key, or an error wrapping store.ErrNotFound.
// The key name is included so callers know what is missing; values never
// appear in errors.
func (m metaRepo) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := m.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("store: meta key %q not found: %w", key, store.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: meta get %q failed: %w", key, err)
	}
	return value, nil
}

// Set creates or overwrites key. An empty key is rejected; an empty value
// is stored as-is.
func (m metaRepo) Set(ctx context.Context, key, value string) error {
	if key == "" {
		return fmt.Errorf("store: meta key must not be empty")
	}
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("store: meta set %q failed: %w", key, err)
	}
	return nil
}

// Delete removes key. A missing key is not an error.
func (m metaRepo) Delete(ctx context.Context, key string) error {
	if _, err := m.db.ExecContext(ctx, `DELETE FROM meta WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: meta delete %q failed: %w", key, err)
	}
	return nil
}

// bookkeepingDDL records applied migrations. It is not itself versioned:
// it must exist before any version can be recorded.
const bookkeepingDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  checksum TEXT NOT NULL,
  applied_at TEXT NOT NULL
);`

// loadMigrations reads the embedded .sql files in version order.
func loadMigrations() ([]migration, error) {
	return loadMigrationsFrom(migrationFiles, "migrations")
}

// loadMigrationsFrom reads .sql files from dir in fsys in version order.
// Names must start with a zero-padded version (0001_meta.sql); anything
// else is a build-time bug surfaced at open, not silently skipped. The
// filesystem is a parameter so tests can feed bad layouts.
func loadMigrationsFrom(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("store: cannot list migrations: %w", err)
	}
	var migs []migration
	for _, e := range entries {
		name := e.Name()
		if len(name) < 8 || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("store: bad migration name %q (want NNNN_name.sql)", name)
		}
		version, err := strconv.Atoi(name[:4])
		if err != nil || version < 1 {
			return nil, fmt.Errorf("store: bad migration name %q (want NNNN_name.sql)", name)
		}
		raw, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, fmt.Errorf("store: cannot read migration %q: %w", name, err)
		}
		sum := sha256.Sum256(raw)
		migs = append(migs, migration{
			version:  version,
			name:     name,
			sql:      string(raw),
			checksum: fmt.Sprintf("%x", sum),
		})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })
	for i := 1; i < len(migs); i++ {
		if migs[i].version == migs[i-1].version {
			return nil, fmt.Errorf("store: migrations %s and %s share version %d: give each migration its own number", migs[i-1].name, migs[i].name, migs[i].version)
		}
	}
	return migs, nil
}

// applyMigrations brings the database to the latest known migration. Each
// pending migration applies in its own transaction and is recorded with
// its checksum. An edited already-applied migration names itself; a
// database newer than this binary refuses with an upgrade prompt instead
// of attempting to run.
func applyMigrations(ctx context.Context, db *sql.DB, migs []migration) error {
	if _, err := db.ExecContext(ctx, bookkeepingDDL); err != nil {
		return fmt.Errorf("store: cannot create migration bookkeeping: %w", err)
	}
	applied := map[int]string{}
	rows, err := db.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("store: cannot read applied migrations: %w", err)
	}
	maxApplied := 0
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("store: cannot read applied migrations: %w", err)
		}
		applied[version] = checksum
		if version > maxApplied {
			maxApplied = version
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: cannot read applied migrations: %w", err)
	}

	maxKnown := 0
	for _, m := range migs {
		if m.version > maxKnown {
			maxKnown = m.version
		}
	}
	if maxApplied > maxKnown {
		return fmt.Errorf("store: database is schema version %d but this devcules only knows version %d: upgrade devcules", maxApplied, maxKnown)
	}

	for _, m := range migs {
		if sum, ok := applied[m.version]; ok {
			if sum != m.checksum {
				return fmt.Errorf("store: migration %s was edited after it was applied (checksum mismatch): restore the file or rebuild from a backup", m.name)
			}
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

// applyOne applies one migration under a write lock taken first (BEGIN
// IMMEDIATE), so two processes opening the same new database take turns: the
// loser re-checks inside the lock and finds the work already done.
func applyOne(ctx context.Context, db *sql.DB, m migration) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: migration %s cannot start: %w", m.name, err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("store: migration %s cannot start: %w", m.name, err)
	}
	var sum string
	switch err := conn.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = ?`, m.version).Scan(&sum); {
	case err == nil:
		conn.ExecContext(ctx, `ROLLBACK`)
		if sum != m.checksum {
			return fmt.Errorf("store: migration %s was edited after it was applied (checksum mismatch): restore the file or rebuild from a backup", m.name)
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		conn.ExecContext(ctx, `ROLLBACK`)
		return fmt.Errorf("store: migration %s cannot start: %w", m.name, err)
	}
	if _, err := conn.ExecContext(ctx, m.sql); err != nil {
		conn.ExecContext(ctx, `ROLLBACK`)
		return fmt.Errorf("store: migration %s failed to apply: %w", m.name, err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, checksum, applied_at) VALUES (?, ?, ?)`,
		m.version, m.checksum, time.Now().UTC().Format(time.RFC3339)); err != nil {
		conn.ExecContext(ctx, `ROLLBACK`)
		return fmt.Errorf("store: migration %s failed to record: %w", m.name, err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("store: migration %s failed to commit: %w", m.name, err)
	}
	return nil
}

// retryBusy runs fn again while SQLite reports the file busy, for up to
// busyTimeoutMS. busy_timeout alone is not enough when several openers
// race on a brand-new database: SQLite answers SQLITE_BUSY at once, without
// calling its busy handler, in a few lock-upgrade situations, so Open
// waits its turn here. fn must be safe to repeat.
func retryBusy(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(busyTimeoutMS * time.Millisecond)
	for wait := time.Millisecond; ; wait = min(wait*2, 100*time.Millisecond) {
		err := fn()
		if err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

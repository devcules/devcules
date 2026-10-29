package sqlite

import (
	"context"
	"crypto/rand"
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

// Users returns auth's login repo.
func (s *sqliteStore) Users() store.UsersRepo {
	return usersRepo{db: s.db}
}

// Sessions returns auth's session repo.
func (s *sqliteStore) Sessions() store.SessionsRepo {
	return sessionsRepo{db: s.db}
}

// Passkeys returns auth's passkey repo.
func (s *sqliteStore) Passkeys() store.PasskeysRepo {
	return passkeysRepo{db: s.db}
}

// Secrets returns the vault's sealed-secret repo.
func (s *sqliteStore) Secrets() store.SecretsRepo {
	return secretsRepo{db: s.db}
}

// usersRepo implements store.UsersRepo on the users table.
type usersRepo struct {
	db *sql.DB
}

// Create stores the hash for username. A taken username wraps
// store.ErrExists; the duplicate never overwrites the stored hash.
func (u usersRepo) Create(ctx context.Context, username, hash string) error {
	if username == "" {
		return fmt.Errorf("store: username must not be empty")
	}
	if hash == "" {
		return fmt.Errorf("store: password hash must not be empty")
	}
	_, err := u.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
		username, hash, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("store: user %q already exists: %w", username, store.ErrExists)
		}
		return fmt.Errorf("store: user create %q failed: %w", username, err)
	}
	return nil
}

// PasswordHash returns the stored hash, or an error wrapping
// store.ErrNotFound. Hashes never appear in errors.
func (u usersRepo) PasswordHash(ctx context.Context, username string) (string, error) {
	var hash string
	err := u.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE username = ?`, username).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("store: user %q not found: %w", username, store.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: user lookup %q failed: %w", username, err)
	}
	return hash, nil
}

// webauthnHandleBytes is the minted user handle length: 32 random
// bytes, well under WebAuthn's 64-byte ceiling.
const webauthnHandleBytes = 32

// EnsureWebAuthnHandle returns the user's handle, minting it on first
// call. The UPDATE only fills a NULL handle and every caller reads
// the stored value back, so concurrent first calls converge on one
// handle instead of racing. An unknown username wraps
// store.ErrNotFound.
func (u usersRepo) EnsureWebAuthnHandle(ctx context.Context, username string) ([]byte, error) {
	var fresh [webauthnHandleBytes]byte
	if _, err := rand.Read(fresh[:]); err != nil {
		return nil, fmt.Errorf("store: cannot mint webauthn handle: %w", err)
	}
	if _, err := u.db.ExecContext(ctx,
		`UPDATE users SET webauthn_handle = ? WHERE username = ? AND webauthn_handle IS NULL`,
		fresh[:], username); err != nil {
		return nil, fmt.Errorf("store: webauthn handle mint %q failed: %w", username, err)
	}
	var handle []byte
	err := u.db.QueryRowContext(ctx, `SELECT webauthn_handle FROM users WHERE username = ?`, username).Scan(&handle)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("store: user %q not found: %w", username, store.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: webauthn handle lookup %q failed: %w", username, err)
	}
	return handle, nil
}

// sessionsRepo implements store.SessionsRepo on the sessions table.
// Times cross the boundary as UTC RFC 3339 strings: second precision,
// which the contract fixtures match with Truncate(time.Second).
type sessionsRepo struct {
	db *sql.DB
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func parseTime(raw, column string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: session %s %q is not a time: %w", column, raw, err)
	}
	return t, nil
}

// Create stores a session. A duplicate hash wraps store.ErrExists.
func (r sessionsRepo) Create(ctx context.Context, s store.Session) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, username, created_at, last_seen_at) VALUES (?, ?, ?, ?)`,
		s.TokenHash, s.Username, formatTime(s.CreatedAt), formatTime(s.LastSeen))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("store: session already exists: %w", store.ErrExists)
		}
		return fmt.Errorf("store: session create failed: %w", err)
	}
	return nil
}

// Get returns the session, or an error wrapping store.ErrNotFound.
func (r sessionsRepo) Get(ctx context.Context, hash string) (store.Session, error) {
	var s store.Session
	var created, seen string
	err := r.db.QueryRowContext(ctx,
		`SELECT token_hash, username, created_at, last_seen_at FROM sessions WHERE token_hash = ?`, hash).
		Scan(&s.TokenHash, &s.Username, &created, &seen)
	if err == sql.ErrNoRows {
		return store.Session{}, fmt.Errorf("store: session not found: %w", store.ErrNotFound)
	}
	if err != nil {
		return store.Session{}, fmt.Errorf("store: session lookup failed: %w", err)
	}
	if s.CreatedAt, err = parseTime(created, "created_at"); err != nil {
		return store.Session{}, err
	}
	if s.LastSeen, err = parseTime(seen, "last_seen_at"); err != nil {
		return store.Session{}, err
	}
	return s, nil
}

// Touch moves the idle marker. A missing session wraps
// store.ErrNotFound.
func (r sessionsRepo) Touch(ctx context.Context, hash string, at time.Time) error {
	res, err := r.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, formatTime(at), hash)
	if err != nil {
		return fmt.Errorf("store: session touch failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: session touch failed: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: session not found: %w", store.ErrNotFound)
	}
	return nil
}

// Delete removes the session. A missing session is not an error.
func (r sessionsRepo) Delete(ctx context.Context, hash string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash); err != nil {
		return fmt.Errorf("store: session delete failed: %w", err)
	}
	return nil
}

// passkeysRepo implements store.PasskeysRepo on the passkeys table.
// Transports cross as one comma-joined string: the values come from
// the WebAuthn transport enum, which holds no commas. Times cross as
// UTC RFC 3339 strings, as with sessions.
type passkeysRepo struct {
	db *sql.DB
}

func joinTransports(transports []string) string {
	return strings.Join(transports, ",")
}

func splitTransports(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Create stores a passkey. A taken credential id wraps
// store.ErrExists; the duplicate never overwrites the stored row.
func (r passkeysRepo) Create(ctx context.Context, p store.Passkey) error {
	switch {
	case len(p.ID) == 0:
		return fmt.Errorf("store: passkey id must not be empty")
	case p.Username == "":
		return fmt.Errorf("store: passkey username must not be empty")
	case len(p.PublicKey) == 0:
		return fmt.Errorf("store: passkey public key must not be empty")
	case p.Name == "":
		return fmt.Errorf("store: passkey name must not be empty")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO passkeys (credential_id, username, public_key, sign_count, aaguid, transports,
		  backup_eligible, backup_state, name, created_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Username, p.PublicKey, p.SignCount, p.AAGUID, joinTransports(p.Transports),
		boolInt(p.BackupEligible), boolInt(p.BackupState), p.Name,
		formatTime(p.CreatedAt), formatTime(p.LastUsedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("store: passkey already exists: %w", store.ErrExists)
		}
		return fmt.Errorf("store: passkey create failed: %w", err)
	}
	return nil
}

// ListByUser returns the user's passkeys, oldest first. The
// credential id breaks created_at ties: two keys registered in one
// second still come back in a stable order.
func (r passkeysRepo) ListByUser(ctx context.Context, username string) ([]store.Passkey, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT credential_id, username, public_key, sign_count, aaguid, transports,
			backup_eligible, backup_state, name, created_at, last_used_at
		 FROM passkeys WHERE username = ? ORDER BY created_at, credential_id`, username)
	if err != nil {
		return nil, fmt.Errorf("store: passkey list failed: %w", err)
	}
	var out []store.Passkey
	for rows.Next() {
		var p store.Passkey
		if err := scanPasskey(rows, &p); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: passkey list failed: %w", err)
	}
	return out, nil
}

// Get returns the passkey, or an error wrapping store.ErrNotFound.
func (r passkeysRepo) Get(ctx context.Context, id []byte) (store.Passkey, error) {
	var p store.Passkey
	err := scanPasskey(r.db.QueryRowContext(ctx,
		`SELECT credential_id, username, public_key, sign_count, aaguid, transports,
			backup_eligible, backup_state, name, created_at, last_used_at
		 FROM passkeys WHERE credential_id = ?`, id), &p)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Passkey{}, fmt.Errorf("store: passkey not found: %w", store.ErrNotFound)
	}
	if err != nil {
		return store.Passkey{}, fmt.Errorf("store: passkey lookup failed: %w", err)
	}
	return p, nil
}

// UpdateAfterLogin records the new sign count, backup state and use
// time. A missing passkey wraps store.ErrNotFound.
func (r passkeysRepo) UpdateAfterLogin(ctx context.Context, id []byte, signCount uint32, backedUp bool, lastUsed time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE passkeys SET sign_count = ?, backup_state = ?, last_used_at = ? WHERE credential_id = ?`,
		signCount, boolInt(backedUp), formatTime(lastUsed), id)
	if err != nil {
		return fmt.Errorf("store: passkey update failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: passkey update failed: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: passkey not found: %w", store.ErrNotFound)
	}
	return nil
}

// Rename changes the label. An empty name is an error; a missing
// passkey wraps store.ErrNotFound.
func (r passkeysRepo) Rename(ctx context.Context, id []byte, name string) error {
	if name == "" {
		return fmt.Errorf("store: passkey name must not be empty")
	}
	res, err := r.db.ExecContext(ctx, `UPDATE passkeys SET name = ? WHERE credential_id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("store: passkey rename failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: passkey rename failed: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: passkey not found: %w", store.ErrNotFound)
	}
	return nil
}

// Delete removes the passkey. A missing passkey is not an error.
func (r passkeysRepo) Delete(ctx context.Context, id []byte) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM passkeys WHERE credential_id = ?`, id); err != nil {
		return fmt.Errorf("store: passkey delete failed: %w", err)
	}
	return nil
}

// passkeyScanner is the Scan both QueryRow and Rows offer, so one
// function reads a passkey row from either.
type passkeyScanner interface {
	Scan(dest ...any) error
}

func scanPasskey(row passkeyScanner, p *store.Passkey) error {
	var signCount int64
	var eligible, backedUp int
	var transports, created, used string
	if err := row.Scan(&p.ID, &p.Username, &p.PublicKey, &signCount, &p.AAGUID,
		&transports, &eligible, &backedUp, &p.Name, &created, &used); err != nil {
		return err
	}
	p.SignCount = uint32(signCount)
	p.Transports = splitTransports(transports)
	p.BackupEligible = eligible != 0
	p.BackupState = backedUp != 0
	var err error
	if p.CreatedAt, err = parseTime(created, "created_at"); err != nil {
		return err
	}
	if p.LastUsedAt, err = parseTime(used, "last_used_at"); err != nil {
		return err
	}
	return nil
}

// secretsRepo implements store.SecretsRepo on the secrets table.
// Values are sealed by the vault before they arrive; errors name the
// secret, never its bytes.
type secretsRepo struct {
	db *sql.DB
}

// Put creates or overwrites the sealed value for name.
func (r secretsRepo) Put(ctx context.Context, name string, sealed []byte, at time.Time) error {
	switch {
	case name == "":
		return fmt.Errorf("store: secret name must not be empty")
	case len(sealed) == 0:
		return fmt.Errorf("store: secret %q value must not be empty", name)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO secrets (name, sealed, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET sealed = excluded.sealed, updated_at = excluded.updated_at`,
		name, sealed, formatTime(at))
	if err != nil {
		return fmt.Errorf("store: secret put %q failed: %w", name, err)
	}
	return nil
}

// Get returns the sealed value, or an error wrapping store.ErrNotFound.
func (r secretsRepo) Get(ctx context.Context, name string) ([]byte, error) {
	var sealed []byte
	err := r.db.QueryRowContext(ctx, `SELECT sealed FROM secrets WHERE name = ?`, name).Scan(&sealed)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("store: secret %q not found: %w", name, store.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: secret get %q failed: %w", name, err)
	}
	return sealed, nil
}

// Names lists every stored name, sorted.
func (r secretsRepo) Names(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT name FROM secrets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: secret names failed: %w", err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("store: secret names failed: %w", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: secret names failed: %w", err)
	}
	return names, nil
}

// Delete removes name. A missing name is not an error.
func (r secretsRepo) Delete(ctx context.Context, name string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, name); err != nil {
		return fmt.Errorf("store: secret delete %q failed: %w", name, err)
	}
	return nil
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

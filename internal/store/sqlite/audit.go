package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// auditRepo implements store.AuditRepo on the audit_log table, whose
// triggers refuse UPDATE and DELETE (migration 0006).
type auditRepo struct {
	db *sql.DB
}

const auditColumns = `seq, at, actor, action, target, ip, detail, prev_hash, hash`

// Append inserts e only when its Seq is the next one. The check and
// the insert are one statement, so two writers cannot both win.
func (r auditRepo) Append(ctx context.Context, e store.AuditEntry) error {
	if e.Seq < 1 {
		return fmt.Errorf("store: audit seq %d must be 1 or more", e.Seq)
	}
	detail := e.Detail
	if detail == nil {
		detail = map[string]string{}
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("store: audit %d detail: %w", e.Seq, err)
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO audit_log (`+auditColumns+`)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE (SELECT COALESCE(MAX(seq), 0) FROM audit_log) = ?`,
		e.Seq, e.At.UTC().Format(nanoTime), e.Actor, e.Action, e.Target, e.IP, string(raw),
		nonNil(e.PrevHash), nonNil(e.Hash), e.Seq-1)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("store: audit seq %d is taken: %w", e.Seq, store.ErrConflict)
		}
		return fmt.Errorf("store: audit append %d failed: %w", e.Seq, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: audit append %d failed: %w", e.Seq, err)
	}
	if n == 0 {
		return fmt.Errorf("store: audit seq %d is not next: %w", e.Seq, store.ErrConflict)
	}
	return nil
}

// nonNil keeps NOT NULL blob columns happy with an empty hash.
func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

func scanAudit(row rowScanner) (store.AuditEntry, error) {
	var e store.AuditEntry
	var at, detail string
	if err := row.Scan(&e.Seq, &at, &e.Actor, &e.Action, &e.Target, &e.IP, &detail, &e.PrevHash, &e.Hash); err != nil {
		return e, err
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return e, fmt.Errorf("store: audit %d at %q is not a time: %w", e.Seq, at, err)
	}
	e.At = t
	if err := json.Unmarshal([]byte(detail), &e.Detail); err != nil {
		return e, fmt.Errorf("store: audit %d detail: %w", e.Seq, err)
	}
	return e, nil
}

// Last returns the newest entry, or an error wrapping ErrNotFound.
func (r auditRepo) Last(ctx context.Context) (store.AuditEntry, error) {
	e, err := scanAudit(r.db.QueryRowContext(ctx,
		`SELECT `+auditColumns+` FROM audit_log ORDER BY seq DESC LIMIT 1`))
	if err == sql.ErrNoRows {
		return store.AuditEntry{}, fmt.Errorf("store: audit log is empty: %w", store.ErrNotFound)
	}
	if err != nil {
		return store.AuditEntry{}, fmt.Errorf("store: audit last failed: %w", err)
	}
	return e, nil
}

// After returns entries past after, oldest first.
func (r auditRepo) After(ctx context.Context, after int64, limit int) ([]store.AuditEntry, error) {
	return r.query(ctx, `SELECT `+auditColumns+` FROM audit_log WHERE seq > ? ORDER BY seq LIMIT ?`, after, limit)
}

// Before returns entries under before (or the newest), newest first.
func (r auditRepo) Before(ctx context.Context, before int64, limit int) ([]store.AuditEntry, error) {
	if before <= 0 {
		return r.query(ctx, `SELECT `+auditColumns+` FROM audit_log ORDER BY seq DESC LIMIT ?`, limit)
	}
	return r.query(ctx, `SELECT `+auditColumns+` FROM audit_log WHERE seq < ? ORDER BY seq DESC LIMIT ?`, before, limit)
}

func (r auditRepo) query(ctx context.Context, q string, args ...any) ([]store.AuditEntry, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: audit read failed: %w", err)
	}
	defer rows.Close()
	var out []store.AuditEntry
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, fmt.Errorf("store: audit read failed: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: audit read failed: %w", err)
	}
	return out, nil
}

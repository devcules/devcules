package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
)

type enrolCodesRepo struct{ db *sql.DB }

func (r enrolCodesRepo) Create(ctx context.Context, c store.EnrolCode) error {
	if c.ID == "" || len(c.CodeHash) == 0 {
		return fmt.Errorf("store: enrol code id/hash must not be empty")
	}
	used := ""
	if !c.UsedAt.IsZero() {
		used = c.UsedAt.UTC().Format(nanoTime)
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO enrol_codes (id, code_hash, label, created_at, expires_at, used_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.CodeHash, c.Label, c.CreatedAt.UTC().Format(nanoTime), c.ExpiresAt.UTC().Format(nanoTime), used)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("store: enrol code %q already exists: %w", c.ID, store.ErrExists)
		}
		return fmt.Errorf("store: enrol code create %q failed: %w", c.ID, err)
	}
	return nil
}

func (r enrolCodesRepo) GetByHash(ctx context.Context, hash []byte) (store.EnrolCode, error) {
	var created, expires, used string
	var c store.EnrolCode
	err := r.db.QueryRowContext(ctx, `SELECT id, code_hash, label, created_at, expires_at, used_at FROM enrol_codes WHERE code_hash = ?`, hash).Scan(&c.ID, &c.CodeHash, &c.Label, &created, &expires, &used)
	if err == sql.ErrNoRows {
		return store.EnrolCode{}, fmt.Errorf("store: enrol code not found: %w", store.ErrNotFound)
	}
	if err != nil {
		return store.EnrolCode{}, fmt.Errorf("store: enrol code lookup failed: %w", err)
	}
	c.CreatedAt, _ = time.Parse(nanoTime, created)
	c.ExpiresAt, _ = time.Parse(nanoTime, expires)
	if used != "" {
		c.UsedAt, _ = time.Parse(nanoTime, used)
	}
	return c, nil
}

func (r enrolCodesRepo) Use(ctx context.Context, id string, at time.Time) error {
	res, err := r.db.ExecContext(ctx, `UPDATE enrol_codes SET used_at = ? WHERE id = ? AND used_at = ''`, at.UTC().Format(nanoTime), id)
	if err != nil {
		return fmt.Errorf("store: enrol use %q failed: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: enrol code %q already used or not found: %w", id, store.ErrExists)
	}
	return nil
}

func (r enrolCodesRepo) DeleteExpired(ctx context.Context, before time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM enrol_codes WHERE expires_at < ? AND used_at = ''`, before.UTC().Format(nanoTime))
	if err != nil {
		return 0, fmt.Errorf("store: enrol prune failed: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r enrolCodesRepo) List(ctx context.Context) ([]store.EnrolCode, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, code_hash, label, created_at, expires_at, used_at FROM enrol_codes ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("store: enrol list failed: %w", err)
	}
	defer rows.Close()
	var out []store.EnrolCode
	for rows.Next() {
		var created, expires, used string
		var c store.EnrolCode
		if err := rows.Scan(&c.ID, &c.CodeHash, &c.Label, &created, &expires, &used); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(nanoTime, created)
		c.ExpiresAt, _ = time.Parse(nanoTime, expires)
		if used != "" {
			c.UsedAt, _ = time.Parse(nanoTime, used)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

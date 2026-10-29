package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
)

type machinesRepo struct{ db *sql.DB }

func (r machinesRepo) Create(ctx context.Context, m store.Machine) error {
	if m.ID == "" || m.Name == "" || len(m.PubKey) == 0 {
		return fmt.Errorf("store: machine id/name/pubkey must not be empty")
	}
	enrolled := m.EnrolledAt.UTC().Format(nanoTime)
	lastSeen := ""
	if !m.LastSeen.IsZero() {
		lastSeen = m.LastSeen.UTC().Format(nanoTime)
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO machines (id, name, pubkey, enrolled_at, last_seen) VALUES (?, ?, ?, ?, ?)`, m.ID, m.Name, m.PubKey, enrolled, lastSeen)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("store: machine %q already exists: %w", m.ID, store.ErrExists)
		}
		return fmt.Errorf("store: machine create %q failed: %w", m.ID, err)
	}
	return nil
}

func (r machinesRepo) Get(ctx context.Context, id string) (store.Machine, error) {
	var enrolled, lastSeen string
	var m store.Machine
	err := r.db.QueryRowContext(ctx, `SELECT id, name, pubkey, enrolled_at, last_seen FROM machines WHERE id = ?`, id).Scan(&m.ID, &m.Name, &m.PubKey, &enrolled, &lastSeen)
	if err == sql.ErrNoRows {
		return store.Machine{}, fmt.Errorf("store: machine %q not found: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return store.Machine{}, fmt.Errorf("store: machine get %q failed: %w", id, err)
	}
	m.EnrolledAt, _ = time.Parse(nanoTime, enrolled)
	if lastSeen != "" {
		m.LastSeen, _ = time.Parse(nanoTime, lastSeen)
	}
	return m, nil
}

func (r machinesRepo) GetByName(ctx context.Context, name string) (store.Machine, error) {
	var enrolled, lastSeen string
	var m store.Machine
	err := r.db.QueryRowContext(ctx, `SELECT id, name, pubkey, enrolled_at, last_seen FROM machines WHERE name = ?`, name).Scan(&m.ID, &m.Name, &m.PubKey, &enrolled, &lastSeen)
	if err == sql.ErrNoRows {
		return store.Machine{}, fmt.Errorf("store: machine name %q not found: %w", name, store.ErrNotFound)
	}
	if err != nil {
		return store.Machine{}, fmt.Errorf("store: machine by-name %q failed: %w", name, err)
	}
	m.EnrolledAt, _ = time.Parse(nanoTime, enrolled)
	if lastSeen != "" {
		m.LastSeen, _ = time.Parse(nanoTime, lastSeen)
	}
	return m, nil
}

func (r machinesRepo) List(ctx context.Context) ([]store.Machine, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, pubkey, enrolled_at, last_seen FROM machines ORDER BY enrolled_at`)
	if err != nil {
		return nil, fmt.Errorf("store: machines list failed: %w", err)
	}
	defer rows.Close()
	var out []store.Machine
	for rows.Next() {
		var enrolled, lastSeen string
		var m store.Machine
		if err := rows.Scan(&m.ID, &m.Name, &m.PubKey, &enrolled, &lastSeen); err != nil {
			return nil, err
		}
		m.EnrolledAt, _ = time.Parse(nanoTime, enrolled)
		if lastSeen != "" {
			m.LastSeen, _ = time.Parse(nanoTime, lastSeen)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r machinesRepo) Touch(ctx context.Context, id string, at time.Time) error {
	res, err := r.db.ExecContext(ctx, `UPDATE machines SET last_seen = ? WHERE id = ?`, at.UTC().Format(nanoTime), id)
	if err != nil {
		return fmt.Errorf("store: machine touch %q failed: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: machine %q not found: %w", id, store.ErrNotFound)
	}
	return nil
}

func (r machinesRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM machines WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: machine delete %q failed: %w", id, err)
	}
	return nil
}

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// approvalsRepo implements store.ApprovalsRepo on the approvals table.
type approvalsRepo struct {
	db *sql.DB
}

const approvalColumns = `id, launch_id, runner, agent, tool, command, rule, irreversible, status, created_at, answered_at, answered_by`

// Create stores a new approval.
func (r approvalsRepo) Create(ctx context.Context, a store.Approval) error {
	if a.ID == "" {
		return fmt.Errorf("store: approval id must not be empty")
	}
	irreversible := 0
	if a.Irreversible {
		irreversible = 1
	}
	answered := ""
	if !a.AnsweredAt.IsZero() {
		answered = a.AnsweredAt.UTC().Format(nanoTime)
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO approvals (`+approvalColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.LaunchID, a.Runner, a.Agent, a.Tool, a.Command, a.Rule, irreversible, a.Status,
		a.CreatedAt.UTC().Format(nanoTime), answered, a.AnsweredBy)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("store: approval %q already exists: %w", a.ID, store.ErrExists)
		}
		return fmt.Errorf("store: approval create %q failed: %w", a.ID, err)
	}
	return nil
}

func scanApproval(row rowScanner) (store.Approval, error) {
	var a store.Approval
	var irreversible int
	var created, answered string
	if err := row.Scan(&a.ID, &a.LaunchID, &a.Runner, &a.Agent, &a.Tool, &a.Command, &a.Rule, &irreversible,
		&a.Status, &created, &answered, &a.AnsweredBy); err != nil {
		return a, err
	}
	a.Irreversible = irreversible != 0
	var err error
	if a.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return a, fmt.Errorf("store: approval created_at %q is not a time: %w", created, err)
	}
	if answered != "" {
		if a.AnsweredAt, err = time.Parse(time.RFC3339Nano, answered); err != nil {
			return a, fmt.Errorf("store: approval answered_at %q is not a time: %w", answered, err)
		}
	}
	return a, nil
}

// Get returns one approval.
func (r approvalsRepo) Get(ctx context.Context, id string) (store.Approval, error) {
	a, err := scanApproval(r.db.QueryRowContext(ctx, `SELECT `+approvalColumns+` FROM approvals WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return store.Approval{}, fmt.Errorf("store: approval %q not found: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return store.Approval{}, fmt.Errorf("store: approval get %q failed: %w", id, err)
	}
	return a, nil
}

// Answer moves a pending approval to status; the first answer wins.
func (r approvalsRepo) Answer(ctx context.Context, id, status, by string, at time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE approvals SET status = ?, answered_at = ?, answered_by = ? WHERE id = ? AND status = ?`,
		status, at.UTC().Format(nanoTime), by, id, store.ApprovalPending)
	if err != nil {
		return fmt.Errorf("store: approval answer %q failed: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: approval answer %q failed: %w", id, err)
	} else if n == 1 {
		return nil
	}
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	return fmt.Errorf("store: approval %q was already answered: %w", id, store.ErrConflict)
}

// List returns up to limit approvals, newest first.
func (r approvalsRepo) List(ctx context.Context, status string, limit int) ([]store.Approval, error) {
	q := `SELECT ` + approvalColumns + ` FROM approvals`
	args := []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC, rowid DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: approval list failed: %w", err)
	}
	defer rows.Close()
	out := []store.Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, fmt.Errorf("store: approval list failed: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: approval list failed: %w", err)
	}
	return out, nil
}

// ExpirePending expires pending approvals created before before.
func (r approvalsRepo) ExpirePending(ctx context.Context, before, at time.Time) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`UPDATE approvals SET status = ?, answered_at = ?, answered_by = '' WHERE status = ? AND created_at < ? RETURNING id`,
		store.ApprovalExpired, at.UTC().Format(nanoTime), store.ApprovalPending, before.UTC().Format(nanoTime))
	if err != nil {
		return nil, fmt.Errorf("store: approval expiry failed: %w", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: approval expiry failed: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: approval expiry failed: %w", err)
	}
	return ids, nil
}

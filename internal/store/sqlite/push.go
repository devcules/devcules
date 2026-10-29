package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"time"

	"github.com/devcules/devcules/internal/store"
)

// pushRepo implements store.PushSubscriptionsRepo on the
// push_subscriptions table. Errors never carry an endpoint: it is a
// capability URL anyone could push to.
type pushRepo struct {
	db *sql.DB
}

// Put creates or replaces the subscription for its endpoint.
func (r pushRepo) Put(ctx context.Context, s store.PushSubscription) error {
	if s.Endpoint == "" || len(s.P256dh) == 0 || len(s.Auth) == 0 {
		return fmt.Errorf("store: a push subscription needs an endpoint and both keys")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (endpoint, p256dh, auth, label, created_at, last_ok_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth, label = excluded.label,
		   created_at = excluded.created_at, last_ok_at = excluded.last_ok_at`,
		s.Endpoint, s.P256dh, s.Auth, s.Label, s.CreatedAt.UTC().Format(nanoTime), optionalNano(s.LastOKAt))
	if err != nil {
		return fmt.Errorf("store: push subscription put failed: %w", err)
	}
	return nil
}

func optionalNano(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(nanoTime)
}

// List returns every subscription, oldest first.
func (r pushRepo) List(ctx context.Context) ([]store.PushSubscription, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT endpoint, p256dh, auth, label, created_at, last_ok_at FROM push_subscriptions ORDER BY created_at, rowid`)
	if err != nil {
		return nil, fmt.Errorf("store: push subscription list failed: %w", err)
	}
	defer rows.Close()
	out := []store.PushSubscription{}
	for rows.Next() {
		var s store.PushSubscription
		var created, ok string
		if err := rows.Scan(&s.Endpoint, &s.P256dh, &s.Auth, &s.Label, &created, &ok); err != nil {
			return nil, fmt.Errorf("store: push subscription list failed: %w", err)
		}
		if s.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("store: push subscription created_at %q is not a time: %w", created, err)
		}
		if ok != "" {
			if s.LastOKAt, err = time.Parse(time.RFC3339Nano, ok); err != nil {
				return nil, fmt.Errorf("store: push subscription last_ok_at %q is not a time: %w", ok, err)
			}
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: push subscription list failed: %w", err)
	}
	return out, nil
}

// Delete removes one subscription; a missing one is fine.
func (r pushRepo) Delete(ctx context.Context, endpoint string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint); err != nil {
		return fmt.Errorf("store: push subscription delete failed: %w", err)
	}
	return nil
}

// Delivered records an accepted push.
func (r pushRepo) Delivered(ctx context.Context, endpoint string, at time.Time) error {
	res, err := r.db.ExecContext(ctx, `UPDATE push_subscriptions SET last_ok_at = ? WHERE endpoint = ?`, at.UTC().Format(nanoTime), endpoint)
	if err != nil {
		return fmt.Errorf("store: push subscription update failed: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: push subscription update failed: %w", err)
	} else if n == 0 {
		return fmt.Errorf("store: push subscription not found: %w", store.ErrNotFound)
	}
	return nil
}

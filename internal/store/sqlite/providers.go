package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
)

type providersRepo struct{ db *sql.DB }

const providerColumns = `id, name, format, also_anthropic, base_url, api, model, is_default, created_at`

func checkProvider(p store.ProviderProfile) error {
	if p.ID == "" || p.Name == "" || p.Format == "" || p.BaseURL == "" {
		return fmt.Errorf("store: provider id/name/format/base_url must not be empty")
	}
	return nil
}

func apiOrAuto(api string) string {
	if api == "" {
		return "auto"
	}
	return api
}

func (r providersRepo) Create(ctx context.Context, p store.ProviderProfile) error {
	if err := checkProvider(p); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: provider create %q failed: %w", p.ID, err)
	}
	defer tx.Rollback()
	if p.Default {
		if _, err := tx.ExecContext(ctx, `UPDATE providers SET is_default = 0 WHERE is_default = 1`); err != nil {
			return fmt.Errorf("store: provider create %q failed: %w", p.ID, err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO providers (`+providerColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Format, p.AlsoAnthropic, p.BaseURL, apiOrAuto(p.API), p.Model, p.Default, p.CreatedAt.UTC().Format(nanoTime))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: providers.id") {
			return fmt.Errorf("store: provider %q already exists: %w", p.ID, store.ErrExists)
		}
		return fmt.Errorf("store: provider create %q failed: %w", p.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: provider create %q failed: %w", p.ID, err)
	}
	return nil
}

func scanProvider(row interface{ Scan(...any) error }) (store.ProviderProfile, error) {
	var p store.ProviderProfile
	var created string
	if err := row.Scan(&p.ID, &p.Name, &p.Format, &p.AlsoAnthropic, &p.BaseURL, &p.API, &p.Model, &p.Default, &created); err != nil {
		return store.ProviderProfile{}, err
	}
	p.CreatedAt, _ = time.Parse(nanoTime, created)
	return p, nil
}

func (r providersRepo) Get(ctx context.Context, id string) (store.ProviderProfile, error) {
	p, err := scanProvider(r.db.QueryRowContext(ctx, `SELECT `+providerColumns+` FROM providers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return store.ProviderProfile{}, fmt.Errorf("store: provider %q not found: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return store.ProviderProfile{}, fmt.Errorf("store: provider get %q failed: %w", id, err)
	}
	return p, nil
}

func (r providersRepo) List(ctx context.Context) ([]store.ProviderProfile, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerColumns+` FROM providers ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("store: provider list failed: %w", err)
	}
	defer rows.Close()
	var out []store.ProviderProfile
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("store: provider scan failed: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r providersRepo) Update(ctx context.Context, p store.ProviderProfile) error {
	if err := checkProvider(p); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE providers SET name = ?, format = ?, also_anthropic = ?, base_url = ?, api = ?, model = ? WHERE id = ?`,
		p.Name, p.Format, p.AlsoAnthropic, p.BaseURL, apiOrAuto(p.API), p.Model, p.ID)
	if err != nil {
		return fmt.Errorf("store: provider update %q failed: %w", p.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: provider %q not found: %w", p.ID, store.ErrNotFound)
	}
	return nil
}

func (r providersRepo) SetDefault(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: provider default %q failed: %w", id, err)
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM providers WHERE id = ?`, id).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: provider %q not found: %w", id, store.ErrNotFound)
		}
		return fmt.Errorf("store: provider default %q failed: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE providers SET is_default = 0 WHERE is_default = 1`); err != nil {
		return fmt.Errorf("store: provider default %q failed: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE providers SET is_default = 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: provider default %q failed: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: provider default %q failed: %w", id, err)
	}
	return nil
}

func (r providersRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: provider delete %q failed: %w", id, err)
	}
	return nil
}

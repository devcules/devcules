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

type projectsRepo struct{ db *sql.DB }

func (r projectsRepo) Create(ctx context.Context, p store.Project) error {
	if p.ID == "" || p.Name == "" || p.Target == "" || p.Dir == "" {
		return fmt.Errorf("store: project id/name/target/dir must not be empty")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO projects (id, name, kind, target, dir, repo, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Kind, p.Target, p.Dir, p.Repo, p.CreatedAt.UTC().Format(nanoTime))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("store: project %q already exists: %w", p.Name, store.ErrExists)
		}
		return fmt.Errorf("store: project create %q failed: %w", p.ID, err)
	}
	return nil
}

func scanProject(row interface{ Scan(...any) error }) (store.Project, error) {
	var p store.Project
	var created string
	if err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.Target, &p.Dir, &p.Repo, &created); err != nil {
		return store.Project{}, err
	}
	p.CreatedAt, _ = time.Parse(nanoTime, created)
	return p, nil
}

func (r projectsRepo) Get(ctx context.Context, id string) (store.Project, error) {
	p, err := scanProject(r.db.QueryRowContext(ctx, `SELECT id, name, kind, target, dir, repo, created_at FROM projects WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Project{}, fmt.Errorf("store: project %q not found: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return store.Project{}, fmt.Errorf("store: project get %q failed: %w", id, err)
	}
	return p, nil
}

func (r projectsRepo) List(ctx context.Context) ([]store.Project, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, kind, target, dir, repo, created_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: project list failed: %w", err)
	}
	defer rows.Close()
	var out []store.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("store: project scan failed: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r projectsRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: project delete %q failed: %w", id, err)
	}
	return nil
}

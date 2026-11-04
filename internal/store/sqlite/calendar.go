package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/devcules/devcules/internal/store"
)

type calendarRepo struct{ db *sql.DB }

const calendarCols = `id, title, start_at, end_at, all_day, location, url, notes, reminder_min, rrule, created_at, updated_at`

// List answers plans that may touch the window: a one-off that overlaps
// it, and every recurring plan whatever its first instance's dates (a
// weekly series begun years before from still lands inside; the caller
// expands and filters it).
func (r calendarRepo) List(ctx context.Context, from, to time.Time) ([]*store.CalendarEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+calendarCols+` FROM calendar_events
		 WHERE rrule != '' OR (end_at >= ? AND start_at <= ?)
		 ORDER BY start_at`,
		from.UTC().Format(nanoTime), to.UTC().Format(nanoTime))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.CalendarEvent
	for rows.Next() {
		e, err := scanCalendarEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r calendarRepo) Get(ctx context.Context, id string) (*store.CalendarEvent, error) {
	e, err := scanCalendarEvent(r.db.QueryRowContext(ctx,
		`SELECT `+calendarCols+` FROM calendar_events WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return e, err
}

func (r calendarRepo) Upsert(ctx context.Context, e *store.CalendarEvent) error {
	if e.ID == "" || e.Title == "" {
		return fmt.Errorf("store: calendar event id/title must not be empty")
	}
	allDay := 0
	if e.AllDay {
		allDay = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO calendar_events (`+calendarCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET title=excluded.title, start_at=excluded.start_at,
		   end_at=excluded.end_at, all_day=excluded.all_day, location=excluded.location,
		   url=excluded.url, notes=excluded.notes, reminder_min=excluded.reminder_min,
		   rrule=excluded.rrule, updated_at=excluded.updated_at`,
		e.ID, e.Title, e.Start.UTC().Format(nanoTime), e.End.UTC().Format(nanoTime), allDay,
		e.Location, e.URL, e.Notes, e.ReminderMin, e.RRule,
		e.CreatedAt.UTC().Format(nanoTime), e.UpdatedAt.UTC().Format(nanoTime))
	return err
}

func (r calendarRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM calendar_events WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func scanCalendarEvent(row interface{ Scan(...any) error }) (*store.CalendarEvent, error) {
	var e store.CalendarEvent
	var allDay int
	var start, end, created, updated string
	if err := row.Scan(&e.ID, &e.Title, &start, &end, &allDay, &e.Location, &e.URL, &e.Notes,
		&e.ReminderMin, &e.RRule, &created, &updated); err != nil {
		return nil, err
	}
	e.AllDay = allDay == 1
	e.Start, _ = time.Parse(nanoTime, start)
	e.End, _ = time.Parse(nanoTime, end)
	e.CreatedAt, _ = time.Parse(nanoTime, created)
	e.UpdatedAt, _ = time.Parse(nanoTime, updated)
	return &e, nil
}

-- Desk calendar events: the user's own plans (click-to-create on the
-- grid, quick-add, recurring). Served alongside the read-only ICS feeds.
-- darwin/go has no per-user concept yet — one owner per devcules — so
-- user is a constant column kept for parity with the upstream schema.
CREATE TABLE calendar_events (
  id           TEXT PRIMARY KEY,
  user         TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL,
  start_at     TEXT NOT NULL,
  end_at       TEXT NOT NULL,
  all_day      INTEGER NOT NULL DEFAULT 0,
  location     TEXT NOT NULL DEFAULT '',
  url          TEXT NOT NULL DEFAULT '',
  notes        TEXT NOT NULL DEFAULT '',
  reminder_min INTEGER NOT NULL DEFAULT 0,
  rrule        TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE INDEX calendar_events_start ON calendar_events (start_at);

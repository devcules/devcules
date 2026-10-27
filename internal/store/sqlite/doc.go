// Package sqlite implements store.Store on SQLite through database/sql,
// on the pure-Go modernc.org/sqlite driver (no cgo), with no ORM.
//
// Its job is the file, the pragmas and the migrations: the database lives
// at the given path, every connection runs with WAL, foreign_keys=ON and
// a busy timeout, and numbered migrations in migrations/ apply in order,
// each in its own transaction, recorded with checksums. It does not define
// the schema (that is the .sql files), serve Postgres (DEVCULES_DATABASE_URL
// is for a later module), or wire itself into any role.
package sqlite

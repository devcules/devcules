// Package elements reads the machine's accessibility tree: the OS's
// own description of every control on screen, with roles, text and
// pixel boxes (M4.3). One small walker script per OS (embedded here and
// run by the helper, so the helper's accessibility permission applies)
// prints a flat TSV; Go parses it into typed elements whose centres
// are valid input targets in the screen package's pixel space.
//
// It does no HTTP and no model calls. Text read from the screen is
// untrusted: whatever consumes a Tree fences it as data before it
// reaches a model (PLAN §4). Password field values never leave the
// walker at all.
package elements

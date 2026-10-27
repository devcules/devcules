// Package bustest holds the shared bus.Bus contract suite.
//
// Every Bus implementation runs it: Run wires one constructor and the
// delivery rules in docs/bus.md land everywhere. It does not test any
// backend's internals; those live in the implementation's own tests. It
// never sleeps for ordering: presence uses deadlines, absence needs none,
// and drops are asserted by counting, not by timing.
package bustest

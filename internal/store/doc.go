// Package store defines the storage interface for Devcules and nothing else.
//
// The Store interface is the whole surface a role needs: lifecycle
// (Ping, Close), the applied schema version, and one tiny key/value repo
// (Meta). Each later module adds its own repo and migration together with
// its contract tests; an interface change owned by another module goes in
// as its own small PR first (PLAN.md §9). How to add a repo is described
// in docs/store.md.
package store

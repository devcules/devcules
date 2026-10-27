// Package storetest holds the shared store.Store contract suite.
//
// Every Store implementation runs it: Run wires one constructor and the
// same behaviour lands everywhere. It does not test any backend's
// internals (migrations, pragmas, file layout); those live in the
// implementation's own tests.
package storetest

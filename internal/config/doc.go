// Package config reads the process configuration from the environment
// and validates it at start.
//
// Its job ends at a validated Config: a plain struct the rest of the
// program can use without further checks. It does not use the vault key
// (internal/vault decodes it in M2.1), generate keys or tokens (a later
// concern), open files beyond the *_FILE secret reads, or start anything.
package config

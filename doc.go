// Package devcules is the root package of Devcules, a self-hosted control
// room for AI agents.
//
// M0.1 lays the foundation only: the CI/CD pipeline and the repository
// files. It implements no Devcules behaviour: there is no server, runner,
// storage, redaction or UI yet. Those arrive as later modules under
// internal/ and plugins/, kept apart by the architecture test in
// arch_test.go.
package devcules

// Package helper is the machine side of the M4.5 task link: it serves
// the machframes protocol over one link to serve, driving the local
// screen, input, accessibility-tree, OCR and SAM-grounding drivers
// behind it. The action budget is enforced HERE, at the machine, so
// no amount of serve-side bugs can spend more than 40 screen actions
// of a task; a machine at zero refuses input with mach.error.
//
// It does no HTTP and holds no secrets: the launch token it dials
// with is per-launch, and grounding calls serve's router, never the
// public model endpoint.
package helper

// Package api holds the HTTP handlers for Devcules.
//
// M0.5 serves health only: /healthz proves the process is up and
// /readyz proves the store answers. Later modules add the REST and
// WebSocket handlers here; nothing else lives in this package yet.
package api

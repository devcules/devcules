// Package api holds the HTTP handlers for Devcules.
//
// M1.1 serves health plus the auth surface: /healthz proves the
// process is up, /readyz proves the store answers, GET /sign-in
// serves the no-JS login form, and POST /auth/sign-in, POST
// /auth/sign-out and GET /auth/me open, close, and report sessions.
// Everything else needs a session and answers 401 without one;
// state-changing routes sit behind the stdlib origin check.
// Later modules add the REST and WebSocket handlers here.
package api

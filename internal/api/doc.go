// Package api holds the HTTP handlers for Devcules.
//
// M1.2 serves health, the auth surface and the embedded PWA:
// /healthz proves the process is up, /readyz proves the store
// answers, POST /auth/sign-in opens a session (204, no redirect:
// the React app is in charge), POST /auth/sign-out closes it and
// GET /auth/me reports it. The static app is public (it holds no
// data and checks the session itself); page navigations outside
// /auth/* fall back to it, and everything else needs a session.
// State-changing routes sit behind the stdlib origin check, and
// every response carries the security headers. Later modules add
// the REST and WebSocket handlers here.
package api

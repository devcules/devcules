// Package dial is the runner side of the link protocol: it dials
// serve's GET /link, authenticates with the runner token, runs the
// hello/welcome handshake, and keeps the link alive across serve
// restarts with exponential backoff and full jitter.
//
// The dialer owns reconnecting: Run blocks until its context ends,
// redialling whenever a link drops. A 401 keeps backing off (the
// operator fixes the token file) and never hammers serve. Handlers
// registered per frame type receive runner-bound frames; M1.4
// registers terminal frame types here without touching the dialer.
// Like the hub, dial never logs the token.
package dial

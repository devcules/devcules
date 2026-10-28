// Package hub is the serve side of the link protocol: it accepts
// runner WebSocket connections at GET /link, authenticates them with
// the runner token, runs the hello/welcome handshake, and routes
// frames to handlers registered per type.
//
// The hub owns liveness: it pings every link and drops links that miss
// two pongs. It owns identity: a second runner with the same name
// replaces the old link, so a restarted runner never fights its ghost.
// It never logs the token — only its SHA-256 hash is kept — and it
// never touches sessions or cookies: /link authenticates with a bearer
// token precisely so browsers (which always send Origin) cannot ride
// a logged-in session into a socket.
//
// Frame types beyond hello/welcome arrive in M1.4 with terminals; new
// types register through On without changing this package.
package hub

// Package link defines the serve/runner link protocol frames and the
// codec both ends share. It does no networking: the hub (serve side)
// and dial (runner side) own their connections and use this package
// only to encode and strictly decode frames.
//
// A frame is one WebSocket text message:
//
//	{"v":1,"type":"…","id":"…","data":{…}}
//
// v pins the protocol version so a mismatch fails loudly instead of
// half-working. type names the handler on both ends (M1.4 adds frame
// types without touching this package). id lets later request/reply
// pairs correlate. data stays opaque here; each handler decodes its
// own shape strictly.
package link

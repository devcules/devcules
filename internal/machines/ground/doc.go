// Package ground turns a screenshot and a plain-language prompt into a
// click point using SAM 3.1 (M4.4). When the accessibility tree sees
// nothing (canvas apps, remote desktops, locked UIs), Ground sends the
// captured frame plus a short noun phrase ("the Pay button") through
// serve's model router to Meta's sam-3.1, decodes the box-and-mask
// line it answers with, and takes the mask's largest region centre as
// the input target.
//
// It speaks the router's /llm/meta/v1/responses endpoint with a
// per-launch token, never a provider key, and parses the special-token
// grammar the model answers in — frame marker, object records of one
// box and one base85-coded mask — in pure Go, from the documented
// wire contract. It does no HTTP serving and holds no state.
package ground

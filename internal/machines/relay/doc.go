// Package machrelay is the serve side of the M4.5 machine task view:
// it routes the machframes protocol (screen, input, tree, words,
// grounding, budget, done-verify) between the browser REST layer and
// the linked machine helpers, correlating every answer by the
// request's frame id exactly like the terminal relay.
//
// It does no HTML and holds no secrets: requests carry only what the
// browser asked for (a phrase, an input event, a budget), and the
// machine does every privileged thing — capture, inject, ground —
// locally behind the link. The action budget is enforced at the
// machine (the helper refuses at zero); serve only tracks a mirror
// for the UI (remaining actions), because the machine's number is
// the honest one and a serve restart loses nothing the machine keeps.
//
// There are no streams to retire: unlike terminal output, every
// machine answer is one request/reply pair, so OnChange is unused.
package machrelay

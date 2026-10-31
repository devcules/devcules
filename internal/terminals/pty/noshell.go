package pty

import (
	"context"
	"encoding/json"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/terminals"
)

// noShellReason is the refusal every terminal-frame handler answers
// when the host cannot run a PTY: the name tells the browser exactly
// what is missing, so the UI stops saying "the machine did not
// answer" and says what to install instead.
const noShellReason = "tmux is not installed on this machine: install it (apt install tmux, brew install tmux) and restart devcules machine (or re-run the one-liner)"

// ServeNoShell answers every terminal frame with an immediate
// refusal. It runs on machines that have no PTY driver (no tmux):
// the link then says why the shell is unavailable at once instead of
// the relay seeing silence and answering "did not answer" 5 s later.
func ServeNoShell(ctx context.Context, d *dial.Client, reason string) {
	if reason == "" {
		reason = noShellReason
	}
	refuse := func(t string) {
		d.On(t, func(_ context.Context, req link.Frame) {
			data, _ := json.Marshal(terminals.Failure{Reason: reason})
			_ = d.Send(link.Frame{V: link.Version, Type: terminals.TypeError, ID: req.ID, Data: data})
		})
	}
	for _, t := range []string{
		terminals.TypeOpen,
		terminals.TypeAttach,
		terminals.TypeInput,
		terminals.TypeResize,
		terminals.TypeDetach,
		terminals.TypeClose,
		terminals.TypeList,
		terminals.TypeAgents,
		terminals.TypeBrowse,
	} {
		refuse(t)
	}
}

package approvals

// Mode is a runner's access mode.
type Mode string

// The four access modes (PLAN.md §4.3). An unset mode is "".
const (
	ModeOff      Mode = "off"
	ModeCritical Mode = "critical"
	ModeAll      Mode = "all"
	ModeFull     Mode = "full"
)

// Modes lists the modes in the order the settings show them.
var Modes = []Mode{ModeOff, ModeCritical, ModeAll, ModeFull}

// Valid reports a known mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeOff, ModeCritical, ModeAll, ModeFull:
		return true
	}
	return false
}

// Verdict is what the policy says about one call.
type Verdict string

// The policy's three answers.
const (
	Allow Verdict = "allow"
	Ask   Verdict = "ask"
	Deny  Verdict = "deny"
)

// Unbound is the target of launches not tied to a runner.
const Unbound = "unbound"

// Target names whose mode applies to a launch on runner.
func Target(runner string) string {
	if runner == "" {
		return Unbound
	}
	return "runner:" + runner
}

// Decide is the policy table: the mode, and whether the call is
// critical (a rule matched, or the agent marked it irreversible). The
// reason is for the agent, which shows it to the person at the
// keyboard.
func Decide(mode Mode, critical bool) (Verdict, string) {
	switch mode {
	case ModeOff:
		return Deny, "agents are off on this runner"
	case ModeFull:
		return Allow, "full access on this runner"
	case ModeAll:
		return Ask, "this runner asks for every tool call"
	case ModeCritical:
		if critical {
			return Ask, "a critical step needs approval"
		}
		return Allow, "not a critical step"
	}
	return Deny, "no access mode is set for this runner yet: pick one in Devcules, Settings, Access modes"
}

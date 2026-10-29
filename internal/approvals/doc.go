// Package approvals decides which agent tool calls need a person, holds
// them until that person answers from the phone, and records every
// answer in the audit log.
//
// Every tool call an agent's hook reports (devcules hook) passes the
// policy: the access mode of the runner the launch runs on, and the
// critical rules. The modes are Agents off (every call is denied), Ask
// on critical (calls matching a critical rule, or marked irreversible
// by the agent, wait for a person; the rest run), Ask for everything,
// and Full access. There is no implicit default: until the owner picks
// a mode for a runner, its calls are denied with a message saying so.
// Launches not tied to a runner have a mode of their own.
//
// A call that must wait becomes a pending approval: stored with its
// command redacted, audited as approval.ask, and pushed to the owner's
// devices (internal/push) as a short notice that holds no command
// text. The hook waits for the answer, Allow once or Deny, which the
// first person to answer decides; one left unanswered expires after
// Timeout and counts as Deny. Answers are audited as approval.allow,
// approval.deny or approval.expire, and mode and rule changes as
// approval.mode and approval.rules.
//
// The critical rules are regular expressions over the command text,
// built in from PLAN.md and editable (built-ins can be switched off,
// and owners add their own). They are a tripwire for honest agents,
// not a sandbox: a hostile agent can always spell a command another
// way, which is what Ask for everything and the runner's workspace
// limits are for.
//
// It does not run or block the call itself (the agent's hook does),
// keep a queue across launches, or send anything but the push notice.
package approvals

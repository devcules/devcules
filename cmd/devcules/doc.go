// Command devcules is the single Devcules binary. It runs in one role per
// invocation: serve, runner or machine, chosen by the subcommand (which wins
// over DEVCULES_ROLE) or, with no subcommand, by DEVCULES_ROLE itself.
//
// M0.2 parses and validates the configuration and reports what each role
// would start. It runs nothing: there is no server, store, link or hook
// yet. Those arrive with their modules; health checks against the /healthz
// endpoint that M0.5 adds.
package main

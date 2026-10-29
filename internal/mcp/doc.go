// Package mcp is the Devcules MCP server: the tools agents and
// assistants call to see and use Devcules, at POST /mcp on serve.
//
// It speaks the Model Context Protocol over Streamable HTTP in both
// eras. A request whose params._meta carries
// "io.modelcontextprotocol/protocolVersion" is served under the
// stateless 2026-07-28 revision: no handshake, version and client
// capabilities on every request, the MCP-Protocol-Version, Mcp-Method
// and Mcp-Name headers checked against the body, server/discover, and
// resultType on every result. Anything else is a legacy client
// (2025-11-25, 2025-06-18, 2025-03-26): initialize negotiates a
// version, notifications/initialized and ping are answered, and the
// MCP-Protocol-Version header (2025-03-26 when absent) must name a
// supported version. Neither era gets a session id, a GET stream or
// DELETE: GET and DELETE answer 405.
//
// Every call needs a launch token (internal/launch), sent as
// Authorization: Bearer, the same credential the agent uses for the
// model router. Tool calls are audited as mcp.call by the launch,
// rate-limited per launch, and logged without their arguments.
//
// Tools are registered by the code that owns them; this package ships
// the protocol, the registry and the read-only built-ins (status,
// launch_info, my_trace, list_terminals, check_redaction). It does not
// stream (every answer is application/json), does not send
// notifications, and does not implement resources, prompts or
// subscriptions.
package mcp

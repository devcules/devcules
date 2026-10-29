package mcp

import (
	"encoding/json"
	"net/http"
)

// Protocol versions. The first is the current stateless revision; the
// rest are legacy (initialize-based) revisions served on the same
// endpoint.
const (
	Version20260728 = "2026-07-28"
	Version20251125 = "2025-11-25"
	Version20250618 = "2025-06-18"
	Version20250326 = "2025-03-26"
)

// modernVersions are served statelessly; legacyVersions through
// initialize. Newest first.
var (
	modernVersions = []string{Version20260728}
	legacyVersions = []string{Version20251125, Version20250618, Version20250326}
)

// Request _meta keys of the 2026-07-28 revision.
const (
	metaVersion      = "io.modelcontextprotocol/protocolVersion"
	metaCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaClientInfo   = "io.modelcontextprotocol/clientInfo"
	metaServerInfo   = "io.modelcontextprotocol/serverInfo"
)

// JSON-RPC and MCP error codes.
const (
	codeParse              = -32700
	codeInvalidRequest     = -32600
	codeMethodNotFound     = -32601
	codeInvalidParams      = -32602
	codeInternal           = -32603
	codeHeaderMismatch     = -32020
	codeUnsupportedVersion = -32022
)

// request is one incoming JSON-RPC message. ID stays raw: it may be a
// string or a number and must come back exactly as sent.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	// Result and Error mark a response, which a client must not send.
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// isNotification reports a message with no id.
func (r request) isNotification() bool {
	return len(r.ID) == 0
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// failure is a JSON-RPC error with the HTTP status it travels under.
type failure struct {
	status int
	err    rpcError
}

func fail(status, code int, msg string) *failure {
	return &failure{status: status, err: rpcError{Code: code, Message: msg}}
}

func invalidParams(msg string) *failure {
	return fail(http.StatusOK, codeInvalidParams, msg)
}

// implementation is serverInfo / clientInfo.
type implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

package mcp

import "errors"

// ErrNotConnected indicates that a ToolSet method requiring an active MCP
// session was called before Connect succeeded.
var ErrNotConnected = errors.New("mcp: not connected")

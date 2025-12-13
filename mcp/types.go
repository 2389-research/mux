// ABOUTME: Defines MCP protocol types - JSON-RPC 2.0 messages, tool info,
// ABOUTME: and server configuration structures.
package mcp

import (
	"encoding/json"
	"sync/atomic"
)

var requestID uint64

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// NewRequest creates a new request with auto-incrementing ID.
func NewRequest(method string, params any) *Request {
	return &Request{
		JSONRPC: "2.0",
		ID:      atomic.AddUint64(&requestID, 1),
		Method:  method,
		Params:  params,
	}
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

// ToolInfo describes an MCP tool.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolsListResult is the response from tools/list.
type ToolsListResult struct {
	Tools []ToolInfo `json:"tools"`
}

// ToolCallParams is the input for tools/call.
type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolCallResult is the response from tools/call.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is an MCP content block.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

// ServerConfig describes an MCP server.
type ServerConfig struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// InitializeParams for MCP handshake.
type InitializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    any        `json:"capabilities"`
	ClientInfo      ClientInfo `json:"clientInfo"`
}

// ClientInfo identifies the MCP client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

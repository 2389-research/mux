// ABOUTME: Defines the Client interface for MCP server communication.
// ABOUTME: Factory function creates appropriate transport implementation.

// Package mcp implements Model Context Protocol clients with stdio and HTTP/SSE
// transports, and adapts MCP tools into the mux tool registry.
package mcp

import (
	"context"
	"fmt"
)

// Client is the interface for MCP server communication.
//
// A client is single use. Close and a failed handshake are both terminal: every
// later operation reports ErrTransportClosed, and reconnecting means building a
// new client with NewClient.
type Client interface {
	// Start initializes the connection and performs MCP handshake. It succeeds
	// once per client: a client that is already connected reports "client
	// already running", and one that was closed, or whose handshake failed,
	// reports ErrTransportClosed instead of connecting again.
	Start(ctx context.Context) error

	// ListTools retrieves available tools from the server.
	ListTools(ctx context.Context) ([]ToolInfo, error)

	// CallTool executes a tool on the server. Cancelling ctx ends this call;
	// it does not close the connection, which stays usable for the next one.
	CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error)

	// Notifications returns a channel for server-initiated messages. The
	// channel is closed when the client closes.
	// Returns nil for transports that don't support notifications (stdio).
	Notifications() <-chan Notification

	// Close shuts down the connection and fails every call still in flight
	// with ErrTransportClosed. It is terminal and safe to call more than once.
	Close() error
}

// NewClient creates an MCP client based on transport config.
func NewClient(config ServerConfig) (Client, error) {
	switch config.Transport {
	case "stdio", "":
		return newStdioClient(config), nil
	case "http", "streamable-http":
		return newHTTPClient(config), nil
	default:
		return nil, fmt.Errorf("unsupported transport: %s", config.Transport)
	}
}

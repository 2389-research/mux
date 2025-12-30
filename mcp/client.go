// ABOUTME: Defines the Client interface for MCP server communication.
// ABOUTME: Factory function creates appropriate transport implementation.
package mcp

import (
	"context"
	"fmt"
)

// Client is the interface for MCP server communication.
type Client interface {
	// Start initializes the connection and performs MCP handshake.
	Start(ctx context.Context) error

	// ListTools retrieves available tools from the server.
	ListTools(ctx context.Context) ([]ToolInfo, error)

	// CallTool executes a tool on the server.
	CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error)

	// Notifications returns a channel for server-initiated messages.
	// Returns nil for transports that don't support notifications (stdio).
	Notifications() <-chan Notification

	// Close shuts down the connection.
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

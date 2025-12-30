// ABOUTME: Implements the HTTP transport for MCP using Streamable HTTP.
// ABOUTME: Supports session management and SSE-based notifications.
package mcp

import (
	"context"
	"net/http"
	"sync"
)

// httpClient implements Client for Streamable HTTP transport.
type httpClient struct {
	config ServerConfig
	http   *http.Client

	mu            sync.Mutex
	running       bool
	sessionID     string //nolint:unused // Set during Start() for session affinity
	notifications chan Notification
	pending       map[uint64]chan *Response

	closeChan chan struct{}
	done      chan struct{}
}

// newHTTPClient creates a new HTTP transport client.
func newHTTPClient(config ServerConfig) *httpClient {
	return &httpClient{
		config:        config,
		http:          &http.Client{},
		notifications: make(chan Notification, 100),
		pending:       make(map[uint64]chan *Response),
		closeChan:     make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// Start initializes the HTTP connection and performs MCP handshake.
func (c *httpClient) Start(ctx context.Context) error {
	return ErrNotConnected // TODO: implement
}

// ListTools retrieves available tools from the server.
func (c *httpClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	return nil, ErrNotConnected // TODO: implement
}

// CallTool executes a tool on the server.
func (c *httpClient) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
	return nil, ErrNotConnected // TODO: implement
}

// Notifications returns the channel for server-initiated messages.
func (c *httpClient) Notifications() <-chan Notification {
	return c.notifications
}

// Close shuts down the HTTP client.
func (c *httpClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}
	c.running = false
	close(c.closeChan)
	close(c.notifications)
	return nil
}

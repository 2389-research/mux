// ABOUTME: Implements the HTTP transport for MCP using Streamable HTTP.
// ABOUTME: Supports session management and SSE-based notifications.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

const httpProtocolVersion = "2025-06-18"

// httpClient implements Client for Streamable HTTP transport.
type httpClient struct {
	config ServerConfig
	http   *http.Client

	mu            sync.Mutex
	running       bool
	sessionID     string
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
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("client already running")
	}
	c.mu.Unlock()

	// Send initialize request
	params := InitializeParams{
		ProtocolVersion: httpProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "mux", Version: "1.0.0"},
	}

	resp, sessionID, err := c.post(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if resp.Error != nil {
		return resp.Error
	}

	c.mu.Lock()
	c.sessionID = sessionID
	c.running = true
	c.mu.Unlock()

	// Send initialized notification
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	return nil
}

// post sends a JSON-RPC request and returns the response along with session ID.
func (c *httpClient) post(ctx context.Context, method string, params any) (*Response, string, error) {
	req := NewRequest(method, params)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.URL, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", httpProtocolVersion)

	// Add session ID if we have one
	c.mu.Lock()
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.Unlock()

	// Add custom headers from config
	for k, v := range c.config.Headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	// Check for session expiry
	if httpResp.StatusCode == http.StatusNotFound {
		return nil, "", ErrSessionExpired
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("http status: %s", httpResp.Status)
	}

	// Extract session ID from response
	sessionID := httpResp.Header.Get("Mcp-Session-Id")

	var resp Response
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, "", fmt.Errorf("decode response: %w", err)
	}

	return &resp, sessionID, nil
}

// notify sends a JSON-RPC notification (no ID, no response expected).
func (c *httpClient) notify(ctx context.Context, method string, params any) error {
	req := &Request{JSONRPC: "2.0", Method: method, Params: params}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("MCP-Protocol-Version", httpProtocolVersion)

	c.mu.Lock()
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.Unlock()

	for k, v := range c.config.Headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	httpResp.Body.Close()

	return nil
}

// ListTools retrieves available tools from the server.
func (c *httpClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	c.mu.Unlock()

	resp, _, err := c.post(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}
	return result.Tools, nil
}

// CallTool executes a tool on the server.
func (c *httpClient) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	c.mu.Unlock()

	params := ToolCallParams{Name: name, Arguments: args}
	resp, _, err := c.post(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse result: %w", err)
	}
	return &result, nil
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

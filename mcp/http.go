// ABOUTME: Implements the HTTP transport for MCP using Streamable HTTP.
// ABOUTME: Supports session management and SSE-based notifications.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

const httpProtocolVersion = "2025-06-18"

// notificationBufferSize is the buffer size for the notifications channel.
const notificationBufferSize = 100

// httpClient implements Client for Streamable HTTP transport.
type httpClient struct {
	config ServerConfig
	http   *http.Client

	mu        sync.Mutex
	state     transportState
	cause     error // terminal cause, recorded once when state becomes closed
	sessionID string

	// lifeCtx ends when the transport does. It is the one signal in-flight
	// requests wait on, so Close never has to outlast a caller's deadline.
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	notifications chan Notification
	notifyMu      sync.Mutex // guards notifyClosed and the send to notifications
	notifyClosed  bool       // set true under notifyMu before close(notifications)
}

// newHTTPClient creates a new HTTP transport client.
func newHTTPClient(config ServerConfig) *httpClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &httpClient{
		config:        config,
		http:          &http.Client{},
		lifeCtx:       ctx,
		lifeCancel:    cancel,
		notifications: make(chan Notification, notificationBufferSize),
	}
}

// Start initializes the HTTP connection and performs MCP handshake. The client
// only reports itself connected once the whole handshake lands, and any
// failure along the way closes it for good.
func (c *httpClient) Start(ctx context.Context) error {
	c.mu.Lock()
	switch c.state {
	case transportStarting, transportRunning:
		c.mu.Unlock()
		return errClientRunning
	case transportClosed:
		err := transportError(c.state, c.cause)
		c.mu.Unlock()
		return err
	}
	c.state = transportStarting
	c.mu.Unlock()

	// Send initialize request
	params := InitializeParams{
		ProtocolVersion: httpProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "mux", Version: "1.0.0"},
	}

	resp, sessionID, err := c.post(ctx, "initialize", params)
	if err != nil {
		return c.startFailed(fmt.Errorf("initialize: %w", err))
	}
	if resp.Error != nil {
		return c.startFailed(resp.Error)
	}

	// Publish the session while still starting: the initialized notification
	// needs the header, and callers cannot reach the server yet.
	if err := c.publishSession(sessionID); err != nil {
		return err
	}

	// Send initialized notification
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return c.startFailed(fmt.Errorf("initialized notification: %w", err))
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != transportStarting {
		return handshakeInterrupted(c.state, c.cause)
	}
	c.state = transportRunning
	return nil
}

// publishSession records the session ID handed out by initialize, or reports
// that Close won the race against the handshake.
func (c *httpClient) publishSession(sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != transportStarting {
		return handshakeInterrupted(c.state, c.cause)
	}
	c.sessionID = sessionID
	return nil
}

// startFailed closes the client on a handshake failure and returns err as the
// caller gave it, so Start still reports what went wrong while every later
// operation reports the transport as closed.
func (c *httpClient) startFailed(err error) error {
	c.terminate(fmt.Errorf("%w: %w", ErrTransportClosed, err))
	return err
}

// requestContext derives a context that ends with the caller's context or with
// the transport, whichever comes first, so Close releases in-flight requests.
func (c *httpClient) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	reqCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.lifeCtx, cancel)
	return reqCtx, func() {
		stop()
		cancel()
	}
}

// requestFailed reports a request the transport itself canceled as the
// terminal error, so a caller racing Close learns why instead of reading a
// bare cancellation.
func (c *httpClient) requestFailed(ctx context.Context, err error) error {
	if ctx.Err() != nil || c.lifeCtx.Err() == nil {
		return err
	}
	if terminal := c.terminalError(); terminal != nil {
		return terminal
	}
	return err
}

// post sends a JSON-RPC request and returns the response along with session ID.
func (c *httpClient) post(ctx context.Context, method string, params any) (*Response, string, error) {
	req := NewRequest(method, params)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, "", fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := c.requestContext(ctx)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", c.config.URL, bytes.NewReader(body))
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
		return nil, "", c.requestFailed(ctx, fmt.Errorf("http request: %w", err))
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
	contentType := httpResp.Header.Get("Content-Type")

	var resp Response

	if strings.HasPrefix(contentType, "text/event-stream") {
		// Parse SSE and find response with matching ID
		reader := newSSEReader(httpResp.Body)
		for {
			event, err := reader.Next()
			if err == io.EOF {
				return nil, "", fmt.Errorf("response not found in SSE stream")
			}
			if err != nil {
				return nil, "", c.requestFailed(ctx, fmt.Errorf("read SSE: %w", err))
			}

			if event.Event == "message" {
				// First try to parse as a notification (has Method, no ID in JSON-RPC)
				var notif Notification
				if err := json.Unmarshal([]byte(event.Data), &notif); err == nil && notif.Method != "" {
					c.notifyMu.Lock()
					if !c.notifyClosed {
						select {
						case c.notifications <- notif:
						default:
						}
					}
					c.notifyMu.Unlock()
					continue
				}

				// Otherwise parse as response
				var candidate Response
				if err := json.Unmarshal([]byte(event.Data), &candidate); err != nil {
					continue // Skip malformed messages
				}
				if candidate.ID == req.ID {
					resp = candidate
					break
				}
			}
		}
	} else {
		if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
			return nil, "", fmt.Errorf("decode response: %w", err)
		}
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

	reqCtx, cancel := c.requestContext(ctx)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", c.config.URL, bytes.NewReader(body))
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
		return c.requestFailed(ctx, fmt.Errorf("http request: %w", err))
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 400 {
		return fmt.Errorf("notification failed: %s", httpResp.Status)
	}

	return nil
}

// ready reports why the transport cannot carry a caller's request, or nil when
// the handshake has completed and the transport is open.
func (c *httpClient) ready() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := transportError(c.state, c.cause); err != nil {
		return err
	}
	if c.state != transportRunning {
		return ErrNotConnected
	}
	return nil
}

// terminalError returns the error a closed transport reports, or nil while the
// transport is still open.
func (c *httpClient) terminalError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != transportClosed {
		return nil
	}
	return transportError(c.state, c.cause)
}

// terminate moves the client to its terminal state once, recording why, and
// releases everything the transport owns: in-flight requests through lifeCtx
// and notification consumers through the closed channel.
func (c *httpClient) terminate(cause error) {
	c.mu.Lock()
	if c.state == transportClosed {
		c.mu.Unlock()
		return
	}
	c.state = transportClosed
	c.cause = cause
	c.sessionID = ""
	c.mu.Unlock()

	c.lifeCancel()

	c.notifyMu.Lock()
	c.notifyClosed = true
	close(c.notifications)
	c.notifyMu.Unlock()
}

// ListTools retrieves available tools from the server.
func (c *httpClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}

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
	if err := c.ready(); err != nil {
		return nil, err
	}

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

// Close shuts down the HTTP client. Safe to call multiple times. The client is
// single use: a closed client never reopens.
func (c *httpClient) Close() error {
	c.terminate(ErrTransportClosed)
	return nil
}

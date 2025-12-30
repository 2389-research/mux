# Streamable HTTP Transport for MCP

**Date:** 2025-12-29
**Status:** Approved
**Version:** v0.4.0 (breaking change)

## Overview

Add Streamable HTTP transport support to mux's MCP client, implementing the 2025-06-18 MCP specification. This enables mux to consume remote MCP servers over HTTP, not just local subprocess (stdio) servers.

## Decisions

| Decision | Choice |
|----------|--------|
| Architecture | Factory + interface (`NewClient` returns `Client` interface) |
| Breaking change | Yes - `NewClient` signature changes |
| Session support | Full (track session ID, handle expiry) |
| Notifications | Channel-based (`Notifications() <-chan Notification`) |
| Reconnect | Manual (return error, consumer reconnects) |

## Interface Design

```go
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
```

## Config Extension

```go
type ServerConfig struct {
    // Existing fields
    Name      string            `json:"name"`
    Transport string            `json:"transport"`  // "stdio" (default) or "http"
    Command   string            `json:"command"`    // stdio only
    Args      []string          `json:"args"`       // stdio only
    Env       map[string]string `json:"env"`

    // HTTP-specific
    URL     string            `json:"url"`     // e.g. "https://example.com/mcp"
    Headers map[string]string `json:"headers"` // custom headers (auth, etc.)
}
```

## HTTP Client Internals

### Struct

```go
type httpClient struct {
    config    ServerConfig
    http      *http.Client
    sessionID string
    baseURL   string

    mu            sync.Mutex
    running       bool
    notifications chan Notification
    pending       map[uint64]chan *Response

    closeChan chan struct{}
    done      chan struct{}
}
```

### Request Flow (client → server)

1. POST JSON-RPC request to endpoint
2. Headers: `Accept: application/json, text/event-stream`, `Mcp-Session-Id`, `MCP-Protocol-Version: 2025-06-18`
3. Response is either:
   - `Content-Type: application/json` → parse single response
   - `Content-Type: text/event-stream` → read SSE until response arrives

### Notification Flow (server → client)

1. After `Start()`, open GET request to endpoint
2. Server returns SSE stream
3. Parse `event: message` / `data: {...}` lines
4. Send parsed notifications to channel
5. On disconnect, close channel

### Session Lifecycle

```
Start()
  → POST initialize (no session ID)
  → Server returns Mcp-Session-Id header
  → Store session ID
  → Open GET stream for notifications

CallTool() / ListTools()
  → POST with Mcp-Session-Id header
  → If 404: return ErrSessionExpired

Close()
  → DELETE with Mcp-Session-Id (optional)
  → Close GET stream
  → Close channels
```

## SSE Parsing

### Message Format

```
event: message
data: {"jsonrpc": "2.0", "id": 1, "result": {...}}

```

### Notification Type

```go
type Notification struct {
    Method string          `json:"method"`
    Params json.RawMessage `json:"params"`
}
```

### Routing Logic

- If message has `id` field → route to pending request channel
- If message has `method` field (no `id`) → send to notifications channel

## Error Handling

```go
var (
    ErrSessionExpired  = errors.New("mcp: session expired")
    ErrNotConnected    = errors.New("mcp: client not connected")
    ErrTransportClosed = errors.New("mcp: transport closed")
)
```

## File Organization

```
mcp/
  types.go      # shared types, Notification type
  client.go     # Client interface + NewClient factory
  stdio.go      # stdioClient (refactored from old client.go)
  http.go       # httpClient implementation
  sse.go        # SSE parsing helpers
  adapter.go    # ToolAdapter (unchanged)
```

## Protocol Versions

- HTTP transport: `2025-06-18` (Streamable HTTP)
- Stdio transport: `2024-11-05` (backwards compatible)

## Testing Strategy

### Unit Tests

- SSE parsing correctness
- Session ID tracking
- Error handling (404 → ErrSessionExpired)

### Integration Tests

- Mock MCP server implementing Streamable HTTP
- Full flow: Start → ListTools → CallTool → Close
- Notification delivery via GET stream

## Breaking Changes

1. `NewClient(config)` now returns `(Client, error)` instead of `*Client`
2. Consumers must update to handle error and use interface type
3. Direct field access on old `*Client` no longer possible

## References

- [MCP Transports Spec (2025-06-18)](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)
- [Why MCP Deprecated SSE](https://blog.fka.dev/blog/2025-06-06-why-mcp-deprecated-sse-and-go-with-streamable-http/)

---

# Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add Streamable HTTP transport to MCP client alongside existing stdio transport

**Architecture:** Factory pattern with Client interface; stdio refactored to implement interface, new HTTP client added

**Tech Stack:** Go stdlib (net/http, bufio for SSE parsing), no external dependencies

---

## Task 1: Add Notification Type and HTTP Config Fields

**Files:**
- Modify: `mcp/types.go:79-99`

**Step 1: Write the failing test**

```go
// In mcp/mcp_test.go, add:
func TestNotificationType(t *testing.T) {
    jsonData := `{"method":"tools/changed","params":{"reason":"update"}}`
    var notif mcp.Notification
    if err := json.Unmarshal([]byte(jsonData), &notif); err != nil {
        t.Fatalf("unmarshal error: %v", err)
    }
    if notif.Method != "tools/changed" {
        t.Errorf("expected 'tools/changed', got %q", notif.Method)
    }
    if notif.Params == nil {
        t.Error("expected non-nil params")
    }
}

func TestServerConfigHTTPFields(t *testing.T) {
    config := mcp.ServerConfig{
        Name:      "remote",
        Transport: "http",
        URL:       "https://example.com/mcp",
        Headers:   map[string]string{"Authorization": "Bearer token"},
    }
    if config.URL != "https://example.com/mcp" {
        t.Errorf("expected URL, got %q", config.URL)
    }
    if config.Headers["Authorization"] != "Bearer token" {
        t.Error("expected Authorization header")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp -run "TestNotificationType|TestServerConfigHTTPFields" -v`
Expected: FAIL - Notification type undefined, ServerConfig missing URL/Headers

**Step 3: Write minimal implementation**

In `mcp/types.go`, add after ServerConfig:

```go
// Notification is a server-initiated message (no ID field).
type Notification struct {
    Method string          `json:"method"`
    Params json.RawMessage `json:"params,omitempty"`
}
```

Update ServerConfig to add HTTP fields:

```go
type ServerConfig struct {
    Name      string            `json:"name"`
    Transport string            `json:"transport"`
    Command   string            `json:"command"`
    Args      []string          `json:"args,omitempty"`
    Env       map[string]string `json:"env,omitempty"`

    // HTTP transport fields
    URL     string            `json:"url,omitempty"`
    Headers map[string]string `json:"headers,omitempty"`
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp -run "TestNotificationType|TestServerConfigHTTPFields" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/types.go mcp/mcp_test.go
git commit -m "feat(mcp): add Notification type and HTTP config fields"
```

---

## Task 2: Add Error Sentinel Values

**Files:**
- Modify: `mcp/types.go`
- Test: `mcp/mcp_test.go`

**Step 1: Write the failing test**

```go
func TestMCPErrors(t *testing.T) {
    if mcp.ErrSessionExpired.Error() != "mcp: session expired" {
        t.Errorf("unexpected error message: %s", mcp.ErrSessionExpired.Error())
    }
    if mcp.ErrNotConnected.Error() != "mcp: client not connected" {
        t.Errorf("unexpected error message: %s", mcp.ErrNotConnected.Error())
    }
    if mcp.ErrTransportClosed.Error() != "mcp: transport closed" {
        t.Errorf("unexpected error message: %s", mcp.ErrTransportClosed.Error())
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp -run TestMCPErrors -v`
Expected: FAIL - undefined errors

**Step 3: Write minimal implementation**

In `mcp/types.go`, add at top after imports:

```go
import (
    "encoding/json"
    "errors"
    "sync/atomic"
)

// Sentinel errors for MCP client operations.
var (
    ErrSessionExpired  = errors.New("mcp: session expired")
    ErrNotConnected    = errors.New("mcp: client not connected")
    ErrTransportClosed = errors.New("mcp: transport closed")
)
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp -run TestMCPErrors -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/types.go mcp/mcp_test.go
git commit -m "feat(mcp): add sentinel errors for transport operations"
```

---

## Task 3: Define Client Interface

**Files:**
- Create: `mcp/client.go` (new file with interface)
- Rename: `mcp/client.go` → `mcp/stdio.go` (existing implementation)

**Step 1: Write the failing test**

```go
func TestClientInterface(t *testing.T) {
    config := mcp.ServerConfig{
        Name:      "test",
        Transport: "stdio",
        Command:   "echo",
    }

    // NewClient should return (Client, error)
    client, err := mcp.NewClient(config)
    if err != nil {
        t.Fatalf("NewClient failed: %v", err)
    }
    if client == nil {
        t.Fatal("expected non-nil client")
    }

    // Verify interface methods exist
    var _ mcp.Client = client
}

func TestNewClientUnsupportedTransport(t *testing.T) {
    config := mcp.ServerConfig{
        Name:      "test",
        Transport: "unsupported",
    }
    _, err := mcp.NewClient(config)
    if err == nil {
        t.Fatal("expected error for unsupported transport")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp -run "TestClientInterface|TestNewClientUnsupportedTransport" -v`
Expected: FAIL - Client interface undefined, NewClient returns wrong type

**Step 3: Rename client.go to stdio.go and refactor**

First, rename the file:
```bash
git mv mcp/client.go mcp/stdio.go
```

Update `mcp/stdio.go`:
- Change struct from `Client` to `stdioClient` (unexported)
- Remove the old `NewClient` function
- Add `Notifications() <-chan Notification` method (returns nil for stdio)

```go
// In mcp/stdio.go, change:
type stdioClient struct {
    // ... same fields as before
}

func newStdioClient(config ServerConfig) *stdioClient {
    return &stdioClient{
        config:    config,
        pending:   make(map[uint64]chan *Response),
        closeChan: make(chan struct{}),
        done:      make(chan struct{}),
    }
}

// Notifications returns nil for stdio transport (no server-initiated messages).
func (c *stdioClient) Notifications() <-chan Notification {
    return nil
}
```

Create new `mcp/client.go`:

```go
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
        return nil, fmt.Errorf("http transport not yet implemented")
    default:
        return nil, fmt.Errorf("unsupported transport: %s", config.Transport)
    }
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp -run "TestClientInterface|TestNewClientUnsupportedTransport" -v`
Expected: PASS

**Step 5: Run all tests to ensure refactoring didn't break anything**

Run: `go test ./mcp -v`
Expected: All existing tests pass

**Step 6: Commit**

```bash
git add mcp/client.go mcp/stdio.go mcp/mcp_test.go
git commit -m "refactor(mcp): extract Client interface, rename impl to stdioClient"
```

---

## Task 4: Fix Downstream Test Breakage

**Files:**
- Modify: `mcp/mcp_test.go` (update tests for new signature)

**Step 1: Update tests that use old NewClient**

Tests like `TestClientCreation` need updating to handle `(Client, error)` return:

```go
func TestClientCreation(t *testing.T) {
    config := mcp.ServerConfig{
        Name:      "test",
        Transport: "stdio",
        Command:   "echo",
        Args:      []string{"hello"},
    }
    client, err := mcp.NewClient(config)
    if err != nil {
        t.Fatalf("NewClient failed: %v", err)
    }
    if client == nil {
        t.Fatal("expected non-nil client")
    }
}
```

**Step 2: Run all tests**

Run: `go test ./mcp -v`
Expected: PASS

**Step 3: Run full test suite**

Run: `go test ./... -v`
Expected: May have failures in other packages using mcp.NewClient

**Step 4: Fix any other packages**

Search for usages and update them to handle `(Client, error)`.

**Step 5: Commit**

```bash
git add -A
git commit -m "fix(mcp): update all callers for NewClient signature change"
```

---

## Task 5: Implement SSE Parser

**Files:**
- Create: `mcp/sse.go`
- Test: `mcp/sse_test.go`

**Step 1: Write the failing test**

Create `mcp/sse_test.go`:

```go
package mcp

import (
    "strings"
    "testing"
)

func TestParseSSEEvents(t *testing.T) {
    input := `event: message
data: {"jsonrpc":"2.0","id":1,"result":{"tools":[]}}

event: message
data: {"jsonrpc":"2.0","method":"notifications/tools/changed"}

`
    reader := strings.NewReader(input)
    events, err := parseSSEEvents(reader)
    if err != nil {
        t.Fatalf("parseSSEEvents failed: %v", err)
    }

    if len(events) != 2 {
        t.Fatalf("expected 2 events, got %d", len(events))
    }

    if events[0].Event != "message" {
        t.Errorf("event 0: expected 'message', got %q", events[0].Event)
    }
    if events[0].Data != `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}` {
        t.Errorf("event 0: unexpected data: %s", events[0].Data)
    }
}

func TestParseSSEEventMultilineData(t *testing.T) {
    input := `event: message
data: {"jsonrpc":"2.0",
data: "id":1}

`
    reader := strings.NewReader(input)
    events, err := parseSSEEvents(reader)
    if err != nil {
        t.Fatalf("parseSSEEvents failed: %v", err)
    }

    if len(events) != 1 {
        t.Fatalf("expected 1 event, got %d", len(events))
    }

    // Multi-line data should be concatenated with newlines
    expected := `{"jsonrpc":"2.0",
"id":1}`
    if events[0].Data != expected {
        t.Errorf("expected %q, got %q", expected, events[0].Data)
    }
}

func TestSSEEventEmpty(t *testing.T) {
    input := ``
    reader := strings.NewReader(input)
    events, err := parseSSEEvents(reader)
    if err != nil {
        t.Fatalf("parseSSEEvents failed: %v", err)
    }
    if len(events) != 0 {
        t.Errorf("expected 0 events, got %d", len(events))
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp -run "TestParseSSE" -v`
Expected: FAIL - parseSSEEvents undefined

**Step 3: Write minimal implementation**

Create `mcp/sse.go`:

```go
// ABOUTME: Implements SSE (Server-Sent Events) parsing for Streamable HTTP.
// ABOUTME: Parses event streams per the SSE specification.
package mcp

import (
    "bufio"
    "io"
    "strings"
)

// sseEvent represents a parsed SSE event.
type sseEvent struct {
    Event string
    Data  string
}

// parseSSEEvents reads SSE events from a reader.
// Events are separated by blank lines.
func parseSSEEvents(r io.Reader) ([]sseEvent, error) {
    var events []sseEvent
    scanner := bufio.NewScanner(r)

    var currentEvent sseEvent
    var dataLines []string

    for scanner.Scan() {
        line := scanner.Text()

        if line == "" {
            // Blank line = end of event
            if currentEvent.Event != "" || len(dataLines) > 0 {
                currentEvent.Data = strings.Join(dataLines, "\n")
                events = append(events, currentEvent)
                currentEvent = sseEvent{}
                dataLines = nil
            }
            continue
        }

        if strings.HasPrefix(line, "event:") {
            currentEvent.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
        } else if strings.HasPrefix(line, "data:") {
            dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
        }
        // Ignore other fields (id:, retry:, comments)
    }

    return events, scanner.Err()
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp -run "TestParseSSE" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/sse.go mcp/sse_test.go
git commit -m "feat(mcp): add SSE parser for Streamable HTTP transport"
```

---

## Task 6: Add SSE Stream Reader

**Files:**
- Modify: `mcp/sse.go`
- Test: `mcp/sse_test.go`

**Step 1: Write the failing test**

```go
func TestNewSSEReader(t *testing.T) {
    input := `event: message
data: {"jsonrpc":"2.0","id":1,"result":{}}

event: message
data: {"jsonrpc":"2.0","method":"notification"}

`
    reader := strings.NewReader(input)
    sseReader := newSSEReader(reader)

    // Read first event
    event, err := sseReader.Next()
    if err != nil {
        t.Fatalf("first Next() failed: %v", err)
    }
    if event.Event != "message" {
        t.Errorf("expected 'message', got %q", event.Event)
    }

    // Read second event
    event, err = sseReader.Next()
    if err != nil {
        t.Fatalf("second Next() failed: %v", err)
    }
    if !strings.Contains(event.Data, "notification") {
        t.Errorf("expected notification in data, got %s", event.Data)
    }

    // Read EOF
    _, err = sseReader.Next()
    if err != io.EOF {
        t.Errorf("expected EOF, got %v", err)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp -run TestNewSSEReader -v`
Expected: FAIL - newSSEReader undefined

**Step 3: Write minimal implementation**

Add to `mcp/sse.go`:

```go
// sseReader reads SSE events one at a time from a stream.
type sseReader struct {
    scanner   *bufio.Scanner
    event     sseEvent
    dataLines []string
}

// newSSEReader creates a streaming SSE reader.
func newSSEReader(r io.Reader) *sseReader {
    return &sseReader{
        scanner: bufio.NewScanner(r),
    }
}

// Next returns the next SSE event, or io.EOF when done.
func (r *sseReader) Next() (*sseEvent, error) {
    r.event = sseEvent{}
    r.dataLines = nil

    for r.scanner.Scan() {
        line := r.scanner.Text()

        if line == "" {
            // Blank line = end of event
            if r.event.Event != "" || len(r.dataLines) > 0 {
                r.event.Data = strings.Join(r.dataLines, "\n")
                return &r.event, nil
            }
            continue
        }

        if strings.HasPrefix(line, "event:") {
            r.event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
        } else if strings.HasPrefix(line, "data:") {
            r.dataLines = append(r.dataLines, strings.TrimPrefix(line, "data: "))
        }
    }

    if err := r.scanner.Err(); err != nil {
        return nil, err
    }
    return nil, io.EOF
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp -run TestNewSSEReader -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/sse.go mcp/sse_test.go
git commit -m "feat(mcp): add streaming SSE reader"
```

---

## Task 7: Create HTTP Client Skeleton

**Files:**
- Create: `mcp/http.go`
- Test: `mcp/http_test.go`

**Step 1: Write the failing test**

Create `mcp/http_test.go`:

```go
package mcp

import (
    "testing"
)

func TestNewHTTPClient(t *testing.T) {
    config := ServerConfig{
        Name:      "remote",
        Transport: "http",
        URL:       "https://example.com/mcp",
        Headers:   map[string]string{"Authorization": "Bearer token"},
    }

    client := newHTTPClient(config)
    if client == nil {
        t.Fatal("expected non-nil client")
    }

    // Verify it implements Client interface
    var _ Client = client
}

func TestHTTPClientNotifications(t *testing.T) {
    config := ServerConfig{
        Name:      "remote",
        Transport: "http",
        URL:       "https://example.com/mcp",
    }

    client := newHTTPClient(config)
    ch := client.Notifications()
    if ch == nil {
        t.Error("expected non-nil notifications channel")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp -run "TestNewHTTPClient|TestHTTPClientNotifications" -v`
Expected: FAIL - newHTTPClient undefined

**Step 3: Write minimal implementation**

Create `mcp/http.go`:

```go
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
    config    ServerConfig
    http      *http.Client
    sessionID string

    mu            sync.Mutex
    running       bool
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
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp -run "TestNewHTTPClient|TestHTTPClientNotifications" -v`
Expected: PASS

**Step 5: Update NewClient factory**

In `mcp/client.go`, update the factory:

```go
case "http", "streamable-http":
    return newHTTPClient(config), nil
```

**Step 6: Test factory integration**

Run: `go test ./mcp -run TestClientInterface -v`
Expected: PASS (with http transport too)

**Step 7: Commit**

```bash
git add mcp/http.go mcp/http_test.go mcp/client.go
git commit -m "feat(mcp): add HTTP client skeleton"
```

---

## Task 8: Implement HTTP Client Start (Initialize)

**Files:**
- Modify: `mcp/http.go`
- Test: `mcp/http_test.go`

**Step 1: Write the failing test (with mock server)**

```go
func TestHTTPClientStart(t *testing.T) {
    // Create a test server that handles initialize
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != "POST" {
            t.Errorf("expected POST, got %s", r.Method)
        }

        // Verify headers
        accept := r.Header.Get("Accept")
        if !strings.Contains(accept, "application/json") {
            t.Errorf("expected Accept to include application/json")
        }

        // Read and verify request body
        var req Request
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            t.Fatalf("failed to decode request: %v", err)
        }
        if req.Method != "initialize" {
            t.Errorf("expected 'initialize', got %q", req.Method)
        }

        // Return session ID in header
        w.Header().Set("Mcp-Session-Id", "test-session-123")
        w.Header().Set("Content-Type", "application/json")

        // Return initialize response
        resp := Response{
            JSONRPC: "2.0",
            ID:      req.ID,
            Result:  json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{}}`),
        }
        json.NewEncoder(w).Encode(resp)
    }))
    defer server.Close()

    config := ServerConfig{
        Name:      "test",
        Transport: "http",
        URL:       server.URL,
    }

    client := newHTTPClient(config)
    ctx := context.Background()

    err := client.Start(ctx)
    if err != nil {
        t.Fatalf("Start failed: %v", err)
    }

    // Verify session ID was captured
    if client.sessionID != "test-session-123" {
        t.Errorf("expected session ID 'test-session-123', got %q", client.sessionID)
    }

    client.Close()
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp -run TestHTTPClientStart -v`
Expected: FAIL - Start returns ErrNotConnected

**Step 3: Write implementation**

Update `mcp/http.go` Start method:

```go
import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "sync"
)

const httpProtocolVersion = "2025-06-18"

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
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp -run TestHTTPClientStart -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/http.go mcp/http_test.go
git commit -m "feat(mcp): implement HTTP client Start with session handling"
```

---

## Task 9: Implement HTTP Client ListTools and CallTool

**Files:**
- Modify: `mcp/http.go`
- Test: `mcp/http_test.go`

**Step 1: Write the failing tests**

```go
func TestHTTPClientListTools(t *testing.T) {
    // Create test server
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var req Request
        json.NewDecoder(r.Body).Decode(&req)

        w.Header().Set("Content-Type", "application/json")

        switch req.Method {
        case "initialize":
            w.Header().Set("Mcp-Session-Id", "test-session")
            resp := Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
            json.NewEncoder(w).Encode(resp)
        case "tools/list":
            result := `{"tools":[{"name":"test_tool","description":"A test tool"}]}`
            resp := Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(result)}
            json.NewEncoder(w).Encode(resp)
        }
    }))
    defer server.Close()

    config := ServerConfig{Transport: "http", URL: server.URL}
    client := newHTTPClient(config)
    ctx := context.Background()

    if err := client.Start(ctx); err != nil {
        t.Fatalf("Start failed: %v", err)
    }
    defer client.Close()

    tools, err := client.ListTools(ctx)
    if err != nil {
        t.Fatalf("ListTools failed: %v", err)
    }

    if len(tools) != 1 {
        t.Fatalf("expected 1 tool, got %d", len(tools))
    }
    if tools[0].Name != "test_tool" {
        t.Errorf("expected 'test_tool', got %q", tools[0].Name)
    }
}

func TestHTTPClientCallTool(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var req Request
        json.NewDecoder(r.Body).Decode(&req)

        w.Header().Set("Content-Type", "application/json")

        switch req.Method {
        case "initialize":
            w.Header().Set("Mcp-Session-Id", "test-session")
            resp := Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
            json.NewEncoder(w).Encode(resp)
        case "tools/call":
            result := `{"content":[{"type":"text","text":"tool result"}]}`
            resp := Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(result)}
            json.NewEncoder(w).Encode(resp)
        }
    }))
    defer server.Close()

    config := ServerConfig{Transport: "http", URL: server.URL}
    client := newHTTPClient(config)
    ctx := context.Background()

    if err := client.Start(ctx); err != nil {
        t.Fatalf("Start failed: %v", err)
    }
    defer client.Close()

    result, err := client.CallTool(ctx, "test_tool", map[string]any{"input": "test"})
    if err != nil {
        t.Fatalf("CallTool failed: %v", err)
    }

    if len(result.Content) != 1 {
        t.Fatalf("expected 1 content block, got %d", len(result.Content))
    }
    if result.Content[0].Text != "tool result" {
        t.Errorf("expected 'tool result', got %q", result.Content[0].Text)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp -run "TestHTTPClientListTools|TestHTTPClientCallTool" -v`
Expected: FAIL - methods return ErrNotConnected

**Step 3: Write implementation**

```go
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
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp -run "TestHTTPClientListTools|TestHTTPClientCallTool" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/http.go mcp/http_test.go
git commit -m "feat(mcp): implement HTTP client ListTools and CallTool"
```

---

## Task 10: Implement SSE Response Handling

**Files:**
- Modify: `mcp/http.go`
- Test: `mcp/http_test.go`

**Step 1: Write the failing test**

```go
func TestHTTPClientSSEResponse(t *testing.T) {
    // Server returns SSE instead of JSON
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var req Request
        json.NewDecoder(r.Body).Decode(&req)

        switch req.Method {
        case "initialize":
            w.Header().Set("Mcp-Session-Id", "test-session")
            w.Header().Set("Content-Type", "application/json")
            resp := Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
            json.NewEncoder(w).Encode(resp)
        case "tools/list":
            // Return as SSE
            w.Header().Set("Content-Type", "text/event-stream")
            result := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"sse_tool"}]}}`, req.ID)
            fmt.Fprintf(w, "event: message\ndata: %s\n\n", result)
        }
    }))
    defer server.Close()

    config := ServerConfig{Transport: "http", URL: server.URL}
    client := newHTTPClient(config)
    ctx := context.Background()

    if err := client.Start(ctx); err != nil {
        t.Fatalf("Start failed: %v", err)
    }
    defer client.Close()

    tools, err := client.ListTools(ctx)
    if err != nil {
        t.Fatalf("ListTools failed: %v", err)
    }

    if len(tools) != 1 || tools[0].Name != "sse_tool" {
        t.Errorf("unexpected tools: %+v", tools)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp -run TestHTTPClientSSEResponse -v`
Expected: FAIL - JSON decode fails on SSE content

**Step 3: Write implementation**

Update `post` method to handle SSE responses:

```go
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
        return nil, "", fmt.Errorf("http request: %w", err)
    }
    defer httpResp.Body.Close()

    if httpResp.StatusCode == http.StatusNotFound {
        return nil, "", ErrSessionExpired
    }

    if httpResp.StatusCode != http.StatusOK {
        return nil, "", fmt.Errorf("http status: %s", httpResp.Status)
    }

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
                return nil, "", fmt.Errorf("read SSE: %w", err)
            }

            if event.Event == "message" {
                var candidate Response
                if err := json.Unmarshal([]byte(event.Data), &candidate); err != nil {
                    continue // Skip malformed messages
                }
                if candidate.ID == req.ID {
                    resp = candidate
                    break
                }
                // Handle notifications
                if candidate.ID == 0 && candidate.Result == nil {
                    var notif Notification
                    if err := json.Unmarshal([]byte(event.Data), &notif); err == nil && notif.Method != "" {
                        select {
                        case c.notifications <- notif:
                        default:
                        }
                    }
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
```

Add import for `strings` and `io` at top of file.

**Step 4: Run test to verify it passes**

Run: `go test ./mcp -run TestHTTPClientSSEResponse -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/http.go mcp/http_test.go
git commit -m "feat(mcp): handle SSE responses in HTTP client"
```

---

## Task 11: Add Session Expiry Handling

**Files:**
- Modify: `mcp/http.go`
- Test: `mcp/http_test.go`

**Step 1: Write the failing test**

```go
func TestHTTPClientSessionExpiry(t *testing.T) {
    callCount := 0
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        callCount++
        var req Request
        json.NewDecoder(r.Body).Decode(&req)

        if req.Method == "initialize" {
            w.Header().Set("Mcp-Session-Id", "test-session")
            w.Header().Set("Content-Type", "application/json")
            resp := Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
            json.NewEncoder(w).Encode(resp)
            return
        }

        // Simulate session expiry on second call
        if callCount > 2 {
            w.WriteHeader(http.StatusNotFound)
            return
        }

        w.Header().Set("Content-Type", "application/json")
        resp := Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"tools":[]}`)}
        json.NewEncoder(w).Encode(resp)
    }))
    defer server.Close()

    config := ServerConfig{Transport: "http", URL: server.URL}
    client := newHTTPClient(config)
    ctx := context.Background()

    if err := client.Start(ctx); err != nil {
        t.Fatalf("Start failed: %v", err)
    }
    defer client.Close()

    // First call should work
    _, err := client.ListTools(ctx)
    if err != nil {
        t.Fatalf("first ListTools failed: %v", err)
    }

    // Second call should get session expired
    _, err = client.ListTools(ctx)
    if err != ErrSessionExpired {
        t.Errorf("expected ErrSessionExpired, got %v", err)
    }
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./mcp -run TestHTTPClientSessionExpiry -v`
Expected: PASS (already implemented in post method)

**Step 3: Commit if test passes**

```bash
git add mcp/http_test.go
git commit -m "test(mcp): add session expiry test for HTTP client"
```

---

## Task 12: Run Full Test Suite and Fix Issues

**Step 1: Run all mcp tests**

Run: `go test ./mcp -v`
Expected: All tests pass

**Step 2: Run full project test suite**

Run: `go test ./... -v`
Expected: All tests pass

**Step 3: Run linting**

Run: `golangci-lint run ./...`
Expected: No errors

**Step 4: Commit any fixes**

```bash
git add -A
git commit -m "fix: address test and lint issues"
```

---

## Task 13: Update Downstream Consumers

**Files:**
- Check all files importing `mcp` package

**Step 1: Search for usages**

Run: `grep -r "mcp.NewClient" --include="*.go" .`

**Step 2: Update each file**

Change from:
```go
client := mcp.NewClient(config)
```

To:
```go
client, err := mcp.NewClient(config)
if err != nil {
    return fmt.Errorf("create client: %w", err)
}
```

**Step 3: Run tests for affected packages**

Run: `go test ./... -v`

**Step 4: Commit**

```bash
git add -A
git commit -m "fix: update all NewClient callers for new signature"
```

---

## Task 14: Final Integration Test

**Files:**
- Test: `mcp/http_test.go`

**Step 1: Write comprehensive integration test**

```go
func TestHTTPClientFullFlow(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var req Request
        json.NewDecoder(r.Body).Decode(&req)

        w.Header().Set("Mcp-Session-Id", "integration-test")
        w.Header().Set("Content-Type", "application/json")

        var resp Response
        resp.JSONRPC = "2.0"
        resp.ID = req.ID

        switch req.Method {
        case "initialize":
            resp.Result = json.RawMessage(`{"protocolVersion":"2025-06-18"}`)
        case "tools/list":
            resp.Result = json.RawMessage(`{"tools":[{"name":"add","description":"Add numbers"}]}`)
        case "tools/call":
            resp.Result = json.RawMessage(`{"content":[{"type":"text","text":"42"}]}`)
        }

        json.NewEncoder(w).Encode(resp)
    }))
    defer server.Close()

    config := ServerConfig{
        Name:      "integration",
        Transport: "http",
        URL:       server.URL,
        Headers:   map[string]string{"X-Test": "true"},
    }

    client, err := NewClient(config)
    if err != nil {
        t.Fatalf("NewClient failed: %v", err)
    }

    ctx := context.Background()

    // Start
    if err := client.Start(ctx); err != nil {
        t.Fatalf("Start failed: %v", err)
    }
    defer client.Close()

    // ListTools
    tools, err := client.ListTools(ctx)
    if err != nil {
        t.Fatalf("ListTools failed: %v", err)
    }
    if len(tools) != 1 || tools[0].Name != "add" {
        t.Errorf("unexpected tools: %+v", tools)
    }

    // CallTool
    result, err := client.CallTool(ctx, "add", map[string]any{"a": 1, "b": 2})
    if err != nil {
        t.Fatalf("CallTool failed: %v", err)
    }
    if result.Content[0].Text != "42" {
        t.Errorf("unexpected result: %s", result.Content[0].Text)
    }

    // Notifications channel exists
    ch := client.Notifications()
    if ch == nil {
        t.Error("expected non-nil notifications channel")
    }

    // Close
    if err := client.Close(); err != nil {
        t.Errorf("Close failed: %v", err)
    }
}
```

**Step 2: Run test**

Run: `go test ./mcp -run TestHTTPClientFullFlow -v`
Expected: PASS

**Step 3: Commit**

```bash
git add mcp/http_test.go
git commit -m "test(mcp): add comprehensive HTTP client integration test"
```

---

## Task 15: Tag v0.4.0 Release

**Step 1: Run final test suite**

Run: `go test ./... -v`
Expected: All pass

**Step 2: Update CHANGELOG**

Add v0.4.0 section to CHANGELOG.md

**Step 3: Commit and tag**

```bash
git add CHANGELOG.md
git commit -m "docs: add v0.4.0 to CHANGELOG"
git tag -a v0.4.0 -m "feat: add Streamable HTTP transport for MCP"
git push origin main --tags
```

---

## Summary

15 tasks total:
1. Add Notification type and HTTP config fields
2. Add error sentinel values
3. Define Client interface (refactor to interface + factory)
4. Fix downstream test breakage
5. Implement SSE parser
6. Add SSE stream reader
7. Create HTTP client skeleton
8. Implement HTTP client Start
9. Implement ListTools and CallTool
10. Implement SSE response handling
11. Add session expiry handling
12. Run full test suite
13. Update downstream consumers
14. Final integration test
15. Tag v0.4.0 release

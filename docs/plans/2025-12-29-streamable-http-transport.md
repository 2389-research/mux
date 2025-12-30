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

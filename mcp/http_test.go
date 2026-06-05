// ABOUTME: Tests for the HTTP transport client implementation.
// ABOUTME: Validates httpClient creation and interface compliance.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHTTPClientStart(t *testing.T) {
	// Create a test server that handles initialize
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// Read and verify request body
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		// Handle initialize request
		if req.Method == "initialize" {
			// Verify headers for initialize request
			accept := r.Header.Get("Accept")
			if !strings.Contains(accept, "application/json") {
				t.Errorf("expected Accept to include application/json")
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
			return
		}

		// Handle initialized notification (no response expected, but we accept it)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusOK)
			return
		}

		t.Errorf("unexpected method: %s", req.Method)
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
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
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
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
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
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "tools/list":
			// Return as SSE
			w.Header().Set("Content-Type", "text/event-stream")
			result := `{"jsonrpc":"2.0","id":` + idToString(req.ID) + `,"result":{"tools":[{"name":"sse_tool"}]}}`
			w.Write([]byte("event: message\ndata: " + result + "\n\n"))
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

// idToString converts a JSON-RPC ID to string for embedding in JSON.
func idToString(id any) string {
	if id == nil {
		return "null"
	}
	b, _ := json.Marshal(id)
	return string(b)
}

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

		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Simulate session expiry on third call (after initialize + initialized)
		if callCount > 3 {
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
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
			return
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

func TestHTTPClientDoubleClose(t *testing.T) {
	// Double close should not panic
	config := ServerConfig{Transport: "http", URL: "http://localhost"}
	client := newHTTPClient(config)

	// Close twice - should not panic
	if err := client.Close(); err != nil {
		t.Errorf("first Close failed: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("second Close failed: %v", err)
	}
}

func TestHTTPClientNotConnected(t *testing.T) {
	// Calling methods before Start() should return ErrNotConnected
	config := ServerConfig{Transport: "http", URL: "http://localhost"}
	client := newHTTPClient(config)
	ctx := context.Background()

	_, err := client.ListTools(ctx)
	if err != ErrNotConnected {
		t.Errorf("ListTools: expected ErrNotConnected, got %v", err)
	}

	_, err = client.CallTool(ctx, "test", nil)
	if err != ErrNotConnected {
		t.Errorf("CallTool: expected ErrNotConnected, got %v", err)
	}
}

func TestHTTPCloseDuringNotificationStress(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		// ready signals that the tools/list SSE handler has started streaming,
		// so Close fires while notifications are actively being sent.
		ready := make(chan struct{})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req Request
			json.NewDecoder(r.Body).Decode(&req)

			switch req.Method {
			case "initialize":
				w.Header().Set("Mcp-Session-Id", "test-session")
				w.Header().Set("Content-Type", "application/json")
				resp := Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
				json.NewEncoder(w).Encode(resp)
			case "notifications/initialized":
				w.WriteHeader(http.StatusOK)
			case "tools/list":
				// Stream many notifications then the final result.
				// The panic window is: Close closes c.notifications while this
				// handler is still flushing notification events to the SSE reader,
				// which then attempts to send on the now-closed channel.
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				fl, _ := w.(http.Flusher)
				// Signal that streaming has begun so Close fires mid-stream.
				close(ready)
				for i := 0; i < 30; i++ {
					fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"note\",\"params\":{}}\n\n")
					if fl != nil {
						fl.Flush()
					}
				}
				fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"tools\":[]}}\n\n", idToString(req.ID))
				if fl != nil {
					fl.Flush()
				}
			}
		}))

		config := ServerConfig{Transport: "http", URL: srv.URL}
		client := newHTTPClient(config)
		ctx := context.Background()

		// Start is mandatory: sets running=true and primes sessionID so that
		// ListTools enters post() and opens the SSE reader — reaching the
		// notification-send path that races with Close.
		if err := client.Start(ctx); err != nil {
			srv.Close()
			t.Fatalf("iter %d: Start failed: %v", iter, err)
		}

		// ListTools drives the SSE stream (30 notification sends to c.notifications).
		go func() { _, _ = client.ListTools(ctx) }()

		// Wait until the SSE stream has started, then Close races with active
		// notification sends — this is the exact send-on-closed-channel window.
		<-ready
		client.Close()
		srv.Close()
	}
}

// ABOUTME: Tests for the HTTP transport client implementation.
// ABOUTME: Validates httpClient creation and interface compliance.
package mcp

import (
	"context"
	"encoding/json"
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

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

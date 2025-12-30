// ABOUTME: Tests for the HTTP transport client implementation.
// ABOUTME: Validates httpClient creation and interface compliance.
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

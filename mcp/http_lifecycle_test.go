// ABOUTME: Lifecycle regressions for the HTTP transport - terminal Close and
// ABOUTME: failed handshakes, and cancellation of in-flight requests, over real HTTP.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestHTTPClosedCannotStart pins the terminal contract: a closed client never
// reaches the network again.
func TestHTTPClosedCannotStart(t *testing.T) {
	c := newHTTPClient(ServerConfig{URL: "http://127.0.0.1:1"})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Start(context.Background()); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Start after Close = %v, want ErrTransportClosed", err)
	}
	if _, err := c.ListTools(context.Background()); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("ListTools after Close = %v, want ErrTransportClosed", err)
	}
}

// TestHTTPCloseCancelsPost reproduces the second trigger in mux#xxrt: Close
// returned while a request under context.Background stayed blocked on a server
// that never answered.
func TestHTTPCloseCancelsPost(t *testing.T) {
	entered := make(chan struct{}, 1)
	// release keeps a failing run from wedging the server shutdown: only the
	// transport is under test, so the handler must not outlive the test.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	c := newHTTPClient(ServerConfig{URL: server.URL})
	done := make(chan error, 1)
	go func() {
		_, _, err := c.post(context.Background(), "initialize", nil)
		done <- err
	}()

	<-entered
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("request outlived Close and succeeded")
		}
	case <-time.After(watchdog):
		t.Fatal("Close left the request blocked")
	}
}

// TestHTTPFailedInitializedIsTerminal reproduces the first trigger in
// mux#xxrt: publishing running before the initialized notification left a
// half-initialized client that still served ListTools and CallTool.
func TestHTTPFailedInitializedIsTerminal(t *testing.T) {
	var toolRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "test-session")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
		case "notifications/initialized":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			toolRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"tools":[]}`)})
		}
	}))
	defer server.Close()

	c := newHTTPClient(ServerConfig{URL: server.URL})
	if err := c.Start(context.Background()); err == nil {
		t.Fatal("expected Start to fail on the rejected initialized notification")
	}

	if _, err := c.ListTools(context.Background()); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("ListTools after failed handshake = %v, want ErrTransportClosed", err)
	}
	if _, err := c.CallTool(context.Background(), "test", nil); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("CallTool after failed handshake = %v, want ErrTransportClosed", err)
	}
	if err := c.Start(context.Background()); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("retry Start = %v, want ErrTransportClosed", err)
	}
	if n := toolRequests.Load(); n != 0 {
		t.Fatalf("half-initialized client sent %d requests", n)
	}
}

// TestHTTPRestartAfterCloseRejected reproduces the third trigger in mux#xxrt:
// Start/Close/Start/Close left a client that served requests while its
// notification channel was permanently closed.
func TestHTTPRestartAfterCloseRejected(t *testing.T) {
	var sessions atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "initialize":
			sessions.Add(1)
			w.Header().Set("Mcp-Session-Id", "test-session")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"tools":[]}`)})
		}
	}))
	defer server.Close()

	c := newHTTPClient(ServerConfig{URL: server.URL})
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := c.Start(context.Background()); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Start after Close = %v, want ErrTransportClosed", err)
	}
	if _, err := c.ListTools(context.Background()); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("ListTools after Close = %v, want ErrTransportClosed", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if n := sessions.Load(); n != 1 {
		t.Fatalf("server saw %d sessions, want 1", n)
	}
}

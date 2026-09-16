// ABOUTME: Lifecycle regressions for the stdio transport - restart rejection,
// ABOUTME: reader termination and cancellable writes, all against real child processes.
package mcp

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// watchdog bounds every wait in these tests: the behaviour under test is that
// the transport resolves callers promptly, so a timeout is a failure, never a
// tuning knob.
const watchdog = 5 * time.Second

// TestStdioClosedCannotStart pins the terminal contract: a closed client never
// launches a child process, it reports ErrTransportClosed.
func TestStdioClosedCannotStart(t *testing.T) {
	c := newStdioClient(ServerConfig{Command: "must-not-execute"})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Start(context.Background()); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Start after Close = %v, want ErrTransportClosed", err)
	}
}

// TestStdioRestartAfterCloseRejected reproduces mux#s53f: Start -> Close ->
// Start used to reuse the already-closed lifecycle channels and panic with
// "close of closed channel". A closed client is terminal and starts no second
// child process.
func TestStdioRestartAfterCloseRejected(t *testing.T) {
	c := newStdioClient(ServerConfig{Command: "node", Args: []string{"testdata/mock_server.js"}})
	ctx := context.Background()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	first := c.cmd
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := c.Start(ctx); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Start after Close = %v, want ErrTransportClosed", err)
	}
	if c.cmd != first {
		t.Fatal("rejected Start launched a second child process")
	}
}

// TestStdioStartAfterInitializeFailureRejected reproduces the retry half of
// mux#s53f: a handshake failure tears the client down, so the retry that
// callers naturally attempt used to re-enter Start and panic in cleanup.
func TestStdioStartAfterInitializeFailureRejected(t *testing.T) {
	c := newStdioClient(ServerConfig{Command: "node", Args: []string{"testdata/init_fail_server.js"}})
	ctx := context.Background()

	if err := c.Start(ctx); err == nil {
		c.Close()
		t.Fatal("expected initialize to fail (server refuses it)")
	}
	defer c.Close()

	first := c.cmd
	if err := c.Start(ctx); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Start after failed handshake = %v, want ErrTransportClosed", err)
	}
	if c.cmd != first {
		t.Fatal("rejected Start launched a second child process")
	}
}

// TestStdioReaderEOFFailsPendingCalls reproduces mux#64sw: when the child exits
// with calls outstanding, the reader used to leave them waiting for a caller
// deadline that context.Background never supplies.
func TestStdioReaderEOFFailsPendingCalls(t *testing.T) {
	c := newStdioClient(ServerConfig{Command: "node", Args: []string{"testdata/eof_server.js"}})
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	calls := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := c.CallTool(context.Background(), "test_tool", nil)
			calls <- err
		}()
	}

	for i := 0; i < 2; i++ {
		select {
		case err := <-calls:
			if !errors.Is(err, ErrTransportClosed) {
				t.Fatalf("pending call = %v, want ErrTransportClosed", err)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("pending call reported a caller deadline, not a transport failure: %v", err)
			}
		case <-time.After(watchdog):
			t.Fatal("pending call stranded after the child exited")
		}
	}

	if _, err := c.CallTool(context.Background(), "test_tool", nil); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("call after reader exit = %v, want ErrTransportClosed", err)
	}

	c.mu.Lock()
	stranded := len(c.pending)
	c.mu.Unlock()
	if stranded != 0 {
		t.Fatalf("pending map still holds %d calls", stranded)
	}
}

// TestStdioReaderScanErrorFailsPendingCalls covers the other half of mux#64sw:
// a response above the scanner ceiling kills the reader, and the caller must
// learn why instead of blocking until its own deadline.
func TestStdioReaderScanErrorFailsPendingCalls(t *testing.T) {
	c := newStdioClient(ServerConfig{
		Command:          "node",
		Args:             []string{"testdata/mock_server.js"},
		MaxResponseBytes: 4096, // well under the >100 KiB big_tool payload
	})
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	done := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), "big_tool", nil)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrTransportClosed) {
			t.Fatalf("call = %v, want ErrTransportClosed", err)
		}
		if !errors.Is(err, bufio.ErrTooLong) {
			t.Fatalf("call = %v, want the scanner cause retained", err)
		}
	case <-time.After(watchdog):
		t.Fatal("oversized response stranded the call")
	}

	if _, err := c.CallTool(context.Background(), "test_tool", nil); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("call after scanner failure = %v, want ErrTransportClosed", err)
	}
}

// TestStdioBlockedWriteHonorsCallDeadline reproduces the first half of
// mux#vfwt: a child that stops reading its stdin used to swallow the caller's
// deadline, because the write ran under the lifecycle mutex with no
// cancellation of its own.
func TestStdioBlockedWriteHonorsCallDeadline(t *testing.T) {
	c := newStdioClient(ServerConfig{Command: "node", Args: []string{"testdata/blocked_stdin_server.js"}})
	// Start under a context the test never cancels: releasing the write must
	// not depend on killing the child through the Start context.
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "test_tool", map[string]any{"payload": strings.Repeat("x", 4<<20)})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("CallTool = %v, want context deadline", err)
		}
	case <-time.After(watchdog):
		t.Fatal("blocked write ignored the call deadline")
	}

	// An interrupted write leaves a truncated frame in the pipe, so the
	// connection is finished rather than silently desynchronized.
	if _, err := c.CallTool(context.Background(), "test_tool", nil); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("call after interrupted write = %v, want ErrTransportClosed", err)
	}
}

// TestStdioCloseReleasesBlockedWrite reproduces the second half of mux#vfwt:
// Close used to queue behind the lifecycle mutex that the blocked write held,
// so neither could finish.
func TestStdioCloseReleasesBlockedWrite(t *testing.T) {
	c := newStdioClient(ServerConfig{Command: "node", Args: []string{"testdata/blocked_stdin_server.js"}})
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// No deadline: only Close can release this call.
	done := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), "test_tool", map[string]any{"payload": strings.Repeat("x", 4<<20)})
		done <- err
	}()
	waitForWriteInFlight(t, c)

	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(watchdog):
		t.Fatal("Close blocked behind the stdin write")
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrTransportClosed) {
			t.Fatalf("CallTool = %v, want ErrTransportClosed", err)
		}
	case <-time.After(watchdog):
		t.Fatal("Close left the write blocked")
	}

	if c.cmd.ProcessState == nil {
		t.Fatal("child process was not reaped")
	}
}

// waitForWriteInFlight blocks until a send holds the write gate, so a test can
// act on a write that has already reached the pipe instead of guessing at a
// sleep.
func waitForWriteInFlight(t *testing.T, c *stdioClient) {
	t.Helper()
	deadline := time.Now().Add(watchdog)
	for time.Now().Before(deadline) {
		if len(c.writeGate) == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no write reached the transport")
}

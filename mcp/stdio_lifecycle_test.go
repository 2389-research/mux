// ABOUTME: Lifecycle regressions for the stdio transport - restart rejection,
// ABOUTME: reader termination and cancellable writes, all against real child processes.
package mcp

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
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
	if first.ProcessState == nil {
		t.Fatal("Close left the first child unreaped")
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
//
// The deadline bounds the caller's wait, not the frame: an ordinary per-call
// timeout must leave the connection framed correctly and usable, since one
// oversized argument is no reason to lose the MCP server for the session.
func TestStdioBlockedWriteHonorsCallDeadline(t *testing.T) {
	c := newStdioClient(ServerConfig{
		Command: "node",
		// The child drains stdin again 750ms after the handshake, well past the
		// 50ms deadline below.
		Args: []string{"testdata/blocked_stdin_server.js", "750"},
	})
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

	// The abandoned frame is still written in full, so the next call gets a
	// real answer on a stream the server can still parse.
	ctx, cancel = context.WithTimeout(context.Background(), watchdog)
	defer cancel()
	result, err := c.CallTool(ctx, "test_tool", nil)
	if err != nil {
		t.Fatalf("call after abandoned wait = %v, want success", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	// The child reports how it parsed the stream: initialize, the initialized
	// notification, the abandoned call and this one, none of them truncated.
	if got := result.Content[0].Text; got != "frames=4 malformed=0" {
		t.Fatalf("server parsed %q, want \"frames=4 malformed=0\"", got)
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
	handshakeFrames := c.accepted.Load()

	// No deadline: only Close can release this call.
	done := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), "test_tool", map[string]any{"payload": strings.Repeat("x", 4<<20)})
		done <- err
	}()
	waitForFrameAccepted(t, c, handshakeFrames+1)

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

	// Close is the one interruption that can truncate a frame, so nobody gets
	// to write after it: the stream stays closed rather than desynchronized.
	if _, err := c.CallTool(context.Background(), "test_tool", nil); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("call after interrupted write = %v, want ErrTransportClosed", err)
	}

	if c.cmd.ProcessState == nil {
		t.Fatal("child process was not reaped")
	}
}

// TestStdioAbandonChildReapsProcess covers the cleanup a Start performs when
// Close beat it to the lifecycle: the child it already launched is killed and
// reaped on the spot, never left behind.
func TestStdioAbandonChildReapsProcess(t *testing.T) {
	cmd := exec.Command("cat")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	abandonChild(cmd, stdin, stdout)

	if cmd.ProcessState == nil {
		t.Fatal("abandoned child was killed but never reaped")
	}
	if cmd.ProcessState.Success() {
		t.Fatalf("abandoned child exited on its own terms: %v", cmd.ProcessState)
	}
}

// TestStdioCloseRacingStart covers the concurrent lifecycle mux#s53f asks for:
// Close landing while Start has a child launched but not yet published. Either
// order is legal, but the client must end up closed with no surviving child.
func TestStdioCloseRacingStart(t *testing.T) {
	for i := 0; i < 50; i++ {
		c := newStdioClient(ServerConfig{Command: "cat"})
		started := make(chan error, 1)
		go func() { started <- c.Start(context.Background()) }()

		// Close only once Start owns the lifecycle, so it lands in the window
		// where a child may exist that no field points at yet.
		waitForStarting(t, c)
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		select {
		case err := <-started:
			if err != nil && !errors.Is(err, ErrTransportClosed) {
				t.Fatalf("Start racing Close = %v, want nil or ErrTransportClosed", err)
			}
		case <-time.After(watchdog):
			t.Fatal("Start never returned after Close")
		}

		if _, err := c.CallTool(context.Background(), "test_tool", nil); !errors.Is(err, ErrTransportClosed) {
			t.Fatalf("call after raced Start = %v, want ErrTransportClosed", err)
		}

		c.mu.Lock()
		cmd := c.cmd
		c.mu.Unlock()
		if cmd != nil && cmd.ProcessState == nil {
			t.Fatal("published child was not reaped")
		}
	}
}

// waitForStarting blocks until Start has claimed the lifecycle.
func waitForStarting(t *testing.T, c *stdioClient) {
	t.Helper()
	deadline := time.Now().Add(watchdog)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		state := c.state
		c.mu.Unlock()
		if state != transportIdle {
			return
		}
	}
	t.Fatal("Start never claimed the lifecycle")
}

// waitForFrameAccepted blocks until the writer goroutine has taken ownership of
// want frames, so a test can act on a write that has already reached the pipe
// instead of guessing at a sleep.
func waitForFrameAccepted(t *testing.T, c *stdioClient, want uint64) {
	t.Helper()
	deadline := time.Now().Add(watchdog)
	for time.Now().Before(deadline) {
		if c.accepted.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("writer accepted %d frames, want %d", c.accepted.Load(), want)
}

// ABOUTME: Implements the stdio transport for MCP - manages JSON-RPC 2.0 communication
// ABOUTME: with MCP servers over stdin/stdout pipes.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// shutdownTimeout bounds each blocking step of teardown: waiting for the reader
// goroutine to notice the closed pipe, and reaping the child process.
const shutdownTimeout = 5 * time.Second

// stdioClient communicates with an MCP server over stdin/stdout.
//
// A client is single use. Close and a failed handshake are both terminal: the
// state never leaves transportClosed, and callers reconnect by constructing a
// new client.
type stdioClient struct {
	config  ServerConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	scanner *bufio.Scanner

	mu      sync.Mutex
	pending map[uint64]chan *Response
	state   transportState
	cause   error // terminal cause, recorded once when state becomes closed
	piping  bool  // the reader and writer goroutines were launched

	// lifeCtx is canceled when the transport terminates, so pending calls and
	// blocked writes learn about it from one signal.
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	writes     chan *frame   // handoff to the writer goroutine; unbuffered, so frames keep their order
	accepted   atomic.Uint64 // frames the writer has taken ownership of
	writerDone chan struct{} // closed when writeFrames returns
	readerDone chan struct{} // closed when readResponses returns
	reapOnce   sync.Once
}

// frame is one JSON line on its way to the child. The writer goroutine owns a
// frame from the moment it accepts one until the write finishes, and reports
// the outcome on done, which is buffered so a caller that stopped waiting never
// blocks the writer.
type frame struct {
	data []byte
	done chan error
}

// newStdioClient creates a new MCP client using stdio transport.
func newStdioClient(config ServerConfig) *stdioClient {
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return &stdioClient{
		config:     config,
		pending:    make(map[uint64]chan *Response),
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
		writes:     make(chan *frame),
		writerDone: make(chan struct{}),
		readerDone: make(chan struct{}),
	}
}

// Notifications returns nil for stdio transport (no SSE support).
func (c *stdioClient) Notifications() <-chan Notification {
	return nil
}

// Start launches the MCP server and initializes the connection. A client can be
// started once: a closed client, or one whose handshake failed, reports
// ErrTransportClosed instead of launching another server.
func (c *stdioClient) Start(ctx context.Context) error {
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

	// MCP servers are configured by the user, command execution is intentional
	cmd := exec.CommandContext(ctx, c.config.Command, c.config.Args...) //nolint:gosec // G204: intentional - MCP servers are user-configured
	// Inherit current environment, then overlay custom env vars
	cmd.Env = os.Environ()
	for k, v := range c.config.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return c.startFailed(fmt.Errorf("stdin pipe: %w", err))
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return c.startFailed(fmt.Errorf("stdout pipe: %w", err))
	}

	if err := cmd.Start(); err != nil {
		return c.startFailed(fmt.Errorf("start server: %w", err))
	}

	scanner := bufio.NewScanner(stdout)
	// MCP tool results routinely exceed bufio.Scanner's 64KB default; raise the
	// per-line ceiling so large responses are not silently truncated. The
	// ceiling is configurable via ServerConfig.MaxResponseBytes; zero falls
	// back to DefaultMaxResponseBytes (16 MiB).
	maxResponseBytes := c.config.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponseBytes)

	c.mu.Lock()
	if c.state != transportStarting {
		// Close raced this Start. Own the child we just launched instead of
		// leaking it, and hand the caller the terminal error.
		err := handshakeInterrupted(c.state, c.cause)
		c.mu.Unlock()
		abandonChild(cmd, stdin, stdout)
		return err
	}
	c.cmd, c.stdin, c.stdout, c.scanner = cmd, stdin, stdout, scanner
	c.piping = true
	c.mu.Unlock()

	go c.readResponses()
	go c.writeFrames()

	if err := c.initialize(ctx); err != nil {
		// initialize failed - tear down the goroutine and child process so we
		// do not leak them; the caller only sees the error. The client stays
		// closed so a retry cannot reuse a half-open transport.
		c.shutdown(fmt.Errorf("%w: initialize failed: %w", ErrTransportClosed, err))
		return err
	}

	c.mu.Lock()
	if c.state != transportStarting {
		err := handshakeInterrupted(c.state, c.cause)
		c.mu.Unlock()
		return err
	}
	c.state = transportRunning
	c.mu.Unlock()
	return nil
}

// abandonChild disposes of a child process that will never be published,
// because Close terminated the client while Start was launching it. Killing and
// reaping it here is what keeps a raced Start from leaking a process.
func abandonChild(cmd *exec.Cmd, stdin io.WriteCloser, stdout io.ReadCloser) {
	_ = stdin.Close()
	_ = stdout.Close()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// startFailed records err as the terminal cause and reports it: a client that
// could not start is closed, not idle.
func (c *stdioClient) startFailed(err error) error {
	c.shutdown(fmt.Errorf("%w: %w", ErrTransportClosed, err))
	return err
}

func (c *stdioClient) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "mux", Version: "1.0.0"},
	}
	_, err := c.call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	return c.notify(ctx, "notifications/initialized", nil)
}

// ListTools retrieves available tools from the server.
func (c *stdioClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}
	return result.Tools, nil
}

// CallTool executes a tool on the server.
func (c *stdioClient) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
	params := ToolCallParams{Name: name, Arguments: args}
	resp, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}
	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse result: %w", err)
	}
	return &result, nil
}

func (c *stdioClient) call(ctx context.Context, method string, params any) (*Response, error) {
	req := NewRequest(method, params)
	respChan := make(chan *Response, 1)

	c.mu.Lock()
	// The handshake runs while starting, so only idle and closed are rejected.
	if err := transportError(c.state, c.cause); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.pending[req.ID] = respChan
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
	}()

	if err := c.send(ctx, req); err != nil {
		return nil, err
	}

	select {
	case resp := <-respChan:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.lifeCtx.Done():
		// The transport died under this call - the reader exited, a write was
		// interrupted, or Close ran - so report that instead of waiting for a
		// caller deadline that may never arrive.
		return nil, c.terminalError()
	}
}

func (c *stdioClient) notify(ctx context.Context, method string, params any) error {
	req := &Request{JSONRPC: "2.0", Method: method, Params: params}
	return c.send(ctx, req)
}

// send hands one JSON frame to the writer goroutine and waits for it to land.
// The caller's context bounds only that wait: a frame the writer has accepted
// is always written to completion, so a per-call deadline leaves the stream
// correctly framed and the transport usable for the next call. Nothing here
// holds the lifecycle mutex, so a child that stops reading its stdin blocks
// only other writers - never Close.
func (c *stdioClient) send(ctx context.Context, req *Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	c.mu.Lock()
	if err := transportError(c.state, c.cause); err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()

	f := &frame{data: data, done: make(chan error, 1)}
	select {
	case c.writes <- f:
	case <-ctx.Done():
		// Nothing was handed over, so nothing is half-written.
		return ctx.Err()
	case <-c.lifeCtx.Done():
		return c.terminalError()
	}

	select {
	case err := <-f.done:
		return err
	case <-ctx.Done():
		// The writer still owns this frame and finishes it; only the wait ends.
		return ctx.Err()
	case <-c.lifeCtx.Done():
		return c.terminalError()
	}
}

// writeFrames owns the child's stdin for the life of the transport. Frames are
// written one at a time and to completion, so callers never truncate a JSON
// line by giving up. A write blocked in a full pipe is released by Close, which
// closes the pipe underneath it.
func (c *stdioClient) writeFrames() {
	defer close(c.writerDone)
	for {
		select {
		case f := <-c.writes:
			c.accepted.Add(1)
			n, err := c.stdin.Write(f.data)
			switch {
			case err != nil:
				// The pipe failed: the child is gone, or Close closed it.
				c.terminate(fmt.Errorf("%w: write: %w", ErrTransportClosed, err))
				f.done <- c.terminalError()
			case n < len(f.data):
				c.terminate(fmt.Errorf("%w: %w", ErrTransportClosed, io.ErrShortWrite))
				f.done <- c.terminalError()
			default:
				f.done <- nil
			}
		case <-c.lifeCtx.Done():
			return
		}
	}
}

func (c *stdioClient) readResponses() {
	defer close(c.readerDone)
	for {
		if !c.scanner.Scan() {
			// Scanner stopped - the transport is finished either way, so record
			// the cause and let every pending call fail with it.
			cause := c.scanner.Err()
			if cause == nil {
				cause = io.EOF
			}
			// terminate reports whether this call ended the transport: when
			// Close already did, the pipe error it caused is not news.
			if c.terminate(fmt.Errorf("%w: %w", ErrTransportClosed, cause)) && c.scanner.Err() != nil {
				fmt.Fprintf(os.Stderr, "mcp: stdio read error: %v\n", cause)
			}
			return
		}
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: failed to unmarshal response: %v (line: %s)\n", err, string(line))
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		c.mu.Unlock()
		if ok {
			// The channel is buffered and the caller may already have given up;
			// never block the reader, and never send while holding the mutex.
			select {
			case ch <- &resp:
			default:
			}
		}
	}
}

// terminate moves the client to the closed state exactly once, recording cause
// and releasing everything waiting on the transport: canceling lifeCtx wakes
// pending calls, and closing the pipes wakes the reader and any blocked write.
// It reports whether this call performed the transition.
func (c *stdioClient) terminate(cause error) bool {
	c.mu.Lock()
	if c.state == transportClosed {
		c.mu.Unlock()
		return false
	}
	c.state = transportClosed
	c.cause = cause
	stdin, stdout := c.stdin, c.stdout
	c.mu.Unlock()

	c.lifeCancel()
	if stdin != nil {
		_ = stdin.Close()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	return true
}

// terminalError reports why the transport ended.
func (c *stdioClient) terminalError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cause != nil {
		return c.cause
	}
	return ErrTransportClosed
}

// shutdown terminates the transport with cause and releases the child process.
func (c *stdioClient) shutdown(cause error) {
	c.terminate(cause)
	c.reapOnce.Do(c.reap)
}

// waitForExit waits for a transport goroutine to finish, warning instead of
// blocking forever if it does not.
func waitForExit(done <-chan struct{}, name string) {
	select {
	case <-done:
	case <-time.After(shutdownTimeout):
		fmt.Fprintf(os.Stderr, "mcp: warning: %s goroutine did not exit within timeout\n", name)
	}
}

// reap waits for the transport goroutines to notice the closed pipes, then
// kills and reaps the child. It runs once per client, outside the lifecycle
// mutex.
func (c *stdioClient) reap() {
	c.mu.Lock()
	cmd, piping := c.cmd, c.piping
	c.mu.Unlock()

	if piping {
		// Wait for the transport goroutines to notice the closed pipes before
		// killing the child, so nothing touches its descriptors afterwards.
		// Five seconds is ample for a scanner to see EOF or a blocked write to
		// fail; past that we kill the process anyway rather than hang Close.
		waitForExit(c.readerDone, "readResponses")
		waitForExit(c.writerDone, "writeFrames")
	}

	// Kill the process and reap it so it does not linger as a zombie.
	// Wait runs under its own timeout: SIGKILL is uncatchable on Unix so Wait
	// normally returns promptly, but a process stuck in uninterruptible sleep
	// (D state on Linux, or platform-specific edge cases) could otherwise hang
	// Close indefinitely. After the timeout we give up reaping and move on —
	// the OS will eventually clean up; better a transient zombie than a
	// permanently blocked Close.
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		waitDone := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(waitDone)
		}()
		select {
		case <-waitDone:
		case <-time.After(shutdownTimeout):
			fmt.Fprintf(os.Stderr, "mcp: warning: cmd.Wait did not return within timeout; process may be unreapable\n")
		}
	}
}

// Close shuts down the client. It is terminal - the client cannot be restarted -
// and safe to call more than once.
func (c *stdioClient) Close() error {
	c.shutdown(ErrTransportClosed)
	return nil
}

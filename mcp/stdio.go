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
	reading bool  // a readResponses goroutine was launched

	// lifeCtx is canceled when the transport terminates, so pending calls and
	// blocked writes learn about it from one signal.
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	writeGate  chan struct{} // capacity 1: serializes writes without the lifecycle mutex
	readerDone chan struct{} // closed when readResponses returns
	reapOnce   sync.Once
}

// newStdioClient creates a new MCP client using stdio transport.
func newStdioClient(config ServerConfig) *stdioClient {
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return &stdioClient{
		config:     config,
		pending:    make(map[uint64]chan *Response),
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
		writeGate:  make(chan struct{}, 1),
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
		err := transportError(c.state, c.cause)
		c.mu.Unlock()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	c.cmd, c.stdin, c.stdout, c.scanner = cmd, stdin, stdout, scanner
	c.reading = true
	c.mu.Unlock()

	go c.readResponses()

	if err := c.initialize(ctx); err != nil {
		// initialize failed - tear down the goroutine and child process so we
		// do not leak them; the caller only sees the error. The client stays
		// closed so a retry cannot reuse a half-open transport.
		c.shutdown(fmt.Errorf("%w: initialize failed: %w", ErrTransportClosed, err))
		return err
	}

	c.mu.Lock()
	if c.state != transportStarting {
		err := transportError(c.state, c.cause)
		c.mu.Unlock()
		return err
	}
	c.state = transportRunning
	c.mu.Unlock()
	return nil
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

// send writes one JSON frame to the child. Writes are serialized by writeGate
// rather than the lifecycle mutex, so a child that stops reading its stdin
// blocks only other writers - never Close.
func (c *stdioClient) send(ctx context.Context, req *Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	select {
	case c.writeGate <- struct{}{}:
		defer func() { <-c.writeGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-c.lifeCtx.Done():
		return c.terminalError()
	}

	c.mu.Lock()
	if err := transportError(c.state, c.cause); err != nil {
		c.mu.Unlock()
		return err
	}
	stdin := c.stdin
	c.mu.Unlock()

	// A caller that gives up mid-frame leaves a truncated JSON line in the
	// pipe, which no later message can recover from, so an interrupted write
	// ends the connection. Terminating also closes stdin, which releases the
	// blocked Write below.
	stop := context.AfterFunc(ctx, func() {
		c.terminate(fmt.Errorf("%w: write canceled: %w", ErrTransportClosed, ctx.Err()))
	})
	defer stop()

	n, err := stdin.Write(data)
	switch {
	case err != nil:
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// The pipe failed on its own (the child is gone, or Close raced us).
		c.terminate(fmt.Errorf("%w: write: %w", ErrTransportClosed, err))
		return c.terminalError()
	case n < len(data):
		c.terminate(fmt.Errorf("%w: %w", ErrTransportClosed, io.ErrShortWrite))
		return c.terminalError()
	}
	return nil
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

// reap waits for the reader goroutine to notice the closed pipe, then kills and
// reaps the child. It runs once per client, outside the lifecycle mutex.
func (c *stdioClient) reap() {
	c.mu.Lock()
	cmd, reading := c.cmd, c.reading
	c.mu.Unlock()

	if reading {
		// Wait for readResponses to exit (with timeout).
		// Use a more robust timeout - 5 seconds should be sufficient
		// for the scanner to detect the closed pipe and exit
		select {
		case <-c.readerDone:
			// Clean exit
		case <-time.After(shutdownTimeout):
			// Timeout - goroutine may be stuck, but we'll kill the process anyway
			fmt.Fprintf(os.Stderr, "mcp: warning: readResponses goroutine did not exit within timeout\n")
		}
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

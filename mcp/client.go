// ABOUTME: Implements the MCP client - manages JSON-RPC 2.0 communication
// ABOUTME: with MCP servers over stdio transport.
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

// Client communicates with an MCP server.
type Client struct {
	config    ServerConfig
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	scanner   *bufio.Scanner
	mu        sync.Mutex
	pending   map[uint64]chan *Response
	running   bool
	closeChan chan struct{}
	done      chan struct{} // Signal goroutine exit
}

// NewClient creates a new MCP client.
func NewClient(config ServerConfig) *Client {
	return &Client{
		config:    config,
		pending:   make(map[uint64]chan *Response),
		closeChan: make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Start launches the MCP server and initializes the connection.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("client already running")
	}

	c.cmd = exec.CommandContext(ctx, c.config.Command, c.config.Args...)
	for k, v := range c.config.Env {
		c.cmd.Env = append(c.cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var err error
	c.stdin, err = c.cmd.StdinPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("stdin pipe: %w", err)
	}

	c.stdout, err = c.cmd.StdoutPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("start server: %w", err)
	}

	c.scanner = bufio.NewScanner(c.stdout)
	c.running = true
	c.mu.Unlock()

	go c.readResponses()
	return c.initialize(ctx)
}

func (c *Client) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "mux", Version: "1.0.0"},
	}
	_, err := c.call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	return c.notify("notifications/initialized", nil)
}

// ListTools retrieves available tools from the server.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
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
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
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

func (c *Client) call(ctx context.Context, method string, params any) (*Response, error) {
	req := NewRequest(method, params)
	respChan := make(chan *Response, 1)

	c.mu.Lock()
	c.pending[req.ID] = respChan
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
	}()

	if err := c.send(req); err != nil {
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
	case <-c.closeChan:
		return nil, fmt.Errorf("client closed")
	}
}

func (c *Client) notify(method string, params any) error {
	req := &Request{JSONRPC: "2.0", Method: method, Params: params}
	return c.send(req)
}

func (c *Client) send(req *Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return fmt.Errorf("client closed")
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *Client) readResponses() {
	defer close(c.done)
	for {
		if !c.scanner.Scan() {
			// Scanner stopped - either EOF, error, or closed
			return
		}
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			fmt.Printf("mcp: failed to unmarshal response: %v (line: %s)\n", err, string(line))
			continue
		}
		c.mu.Lock()
		if ch, ok := c.pending[resp.ID]; ok {
			ch <- &resp
		}
		c.mu.Unlock()
	}
}

// Close shuts down the client.
func (c *Client) Close() error {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	c.running = false
	close(c.closeChan)

	// Close stdin and stdout to unblock scanner
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.stdout != nil {
		c.stdout.Close()
	}
	c.mu.Unlock()

	// Wait for readResponses to exit (with timeout)
	// Use a more robust timeout - 5 seconds should be sufficient
	// for the scanner to detect the closed pipe and exit
	select {
	case <-c.done:
		// Clean exit
	case <-time.After(5 * time.Second):
		// Timeout - goroutine may be stuck, but we'll kill the process anyway
		fmt.Fprintf(os.Stderr, "mcp: warning: readResponses goroutine did not exit within timeout\n")
	}

	// Kill process
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	return nil
}

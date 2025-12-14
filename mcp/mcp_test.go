package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/2389-research/mux/mcp"
	"github.com/2389-research/mux/tool"
)

func TestJSONRPCRequest(t *testing.T) {
	req := mcp.NewRequest("tools/list", nil)
	if req.JSONRPC != "2.0" {
		t.Errorf("expected '2.0', got %q", req.JSONRPC)
	}
	if req.Method != "tools/list" {
		t.Errorf("expected 'tools/list', got %q", req.Method)
	}
	if req.ID == 0 {
		t.Error("expected non-zero ID")
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
}

func TestJSONRPCResponse(t *testing.T) {
	successData := `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`
	var resp mcp.Response
	if err := json.Unmarshal([]byte(successData), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Error != nil {
		t.Error("expected no error")
	}

	errData := `{"jsonrpc":"2.0","id":2,"error":{"code":-32600,"message":"Invalid"}}`
	var errResp mcp.Response
	if err := json.Unmarshal([]byte(errData), &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error == nil {
		t.Error("expected error")
	}
	if errResp.Error.Code != -32600 {
		t.Errorf("expected -32600, got %d", errResp.Error.Code)
	}
}

func TestToolInfo(t *testing.T) {
	info := mcp.ToolInfo{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: map[string]any{"type": "object"},
	}
	if info.Name != "read_file" {
		t.Errorf("expected 'read_file', got %q", info.Name)
	}
}

func TestServerConfig(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"server.js"},
	}
	if config.Transport != "stdio" {
		t.Errorf("expected 'stdio', got %q", config.Transport)
	}
}

func TestClientCreation(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	client := mcp.NewClient(config)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestToolAdapter(t *testing.T) {
	info := mcp.ToolInfo{
		Name:        "read_file",
		Description: "Read file contents",
		InputSchema: map[string]any{"type": "object"},
	}

	mockCaller := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "file contents"}},
		},
	}

	adapter := mcp.NewToolAdapter(info, mockCaller)

	// Verify Tool interface compliance
	var _ tool.Tool = adapter

	if adapter.Name() != "read_file" {
		t.Errorf("expected 'read_file', got %q", adapter.Name())
	}

	result, err := adapter.Execute(context.Background(), map[string]any{"path": "/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.Output != "file contents" {
		t.Errorf("expected 'file contents', got %q", result.Output)
	}
}

type mockMCPCaller struct {
	result *mcp.ToolCallResult
	err    error
}

func (m *mockMCPCaller) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolCallResult, error) {
	return m.result, m.err
}

func TestToolAdapterError(t *testing.T) {
	info := mcp.ToolInfo{Name: "failing_tool"}
	mockCaller := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "error message"}},
			IsError: true,
		},
	}

	adapter := mcp.NewToolAdapter(info, mockCaller)
	result, err := adapter.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure")
	}
}

// Server startup failure tests

func TestClientStartInvalidCommand(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "invalid",
		Transport: "stdio",
		Command:   "/nonexistent/command",
		Args:      []string{},
	}
	client := mcp.NewClient(config)
	ctx := context.Background()

	err := client.Start(ctx)
	if err == nil {
		t.Fatal("expected error for invalid command")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestClientStartMissingExecutable(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "missing",
		Transport: "stdio",
		Command:   "this-command-definitely-does-not-exist-12345",
		Args:      []string{},
	}
	client := mcp.NewClient(config)
	ctx := context.Background()

	err := client.Start(ctx)
	if err == nil {
		t.Fatal("expected error for missing executable")
	}
}

func TestClientStartAlreadyRunning(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "cat",
		Args:      []string{},
	}
	client := mcp.NewClient(config)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start the client (will fail to initialize but that's ok for this test)
	_ = client.Start(ctx)
	defer client.Close()

	// Try to start again with new context
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	err := client.Start(ctx2)
	if err == nil {
		t.Fatal("expected error when starting already running client")
	}
	if err.Error() != "client already running" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// Server crash and connection tests

func TestClientServerCrashDuringOperation(t *testing.T) {
	// Use a command that exits immediately
	config := mcp.ServerConfig{
		Name:      "crash",
		Transport: "stdio",
		Command:   "sh",
		Args:      []string{"-c", "exit 1"},
	}
	client := mcp.NewClient(config)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start will fail because server exits immediately
	err := client.Start(ctx)
	if err == nil {
		client.Close()
		t.Fatal("expected error when server crashes during startup")
	}
}

func TestClientConnectionCleanupOnContextCancellation(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "cat",
		Args:      []string{},
	}
	client := mcp.NewClient(config)
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	err := client.Start(ctx)
	// Should fail due to context cancellation
	if err == nil {
		client.Close()
		t.Fatal("expected error when context is cancelled")
	}
}

// JSON-RPC protocol error tests

func TestJSONRPCProtocolErrors(t *testing.T) {
	tests := []struct {
		name        string
		errorCode   int
		errorMsg    string
		expectedErr bool
	}{
		{
			name:        "Parse Error",
			errorCode:   -32700,
			errorMsg:    "Parse error",
			expectedErr: true,
		},
		{
			name:        "Invalid Request",
			errorCode:   -32600,
			errorMsg:    "Invalid Request",
			expectedErr: true,
		},
		{
			name:        "Method Not Found",
			errorCode:   -32601,
			errorMsg:    "Method not found",
			expectedErr: true,
		},
		{
			name:        "Invalid Params",
			errorCode:   -32602,
			errorMsg:    "Invalid params",
			expectedErr: true,
		},
		{
			name:        "Internal Error",
			errorCode:   -32603,
			errorMsg:    "Internal error",
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpcErr := &mcp.RPCError{
				Code:    tt.errorCode,
				Message: tt.errorMsg,
			}

			if rpcErr.Error() != tt.errorMsg {
				t.Errorf("expected error message %q, got %q", tt.errorMsg, rpcErr.Error())
			}
			if rpcErr.Code != tt.errorCode {
				t.Errorf("expected error code %d, got %d", tt.errorCode, rpcErr.Code)
			}
		})
	}
}

func TestJSONRPCErrorResponse(t *testing.T) {
	testCases := []struct {
		name     string
		jsonData string
		code     int
		message  string
	}{
		{
			name:     "ParseError",
			jsonData: `{"jsonrpc":"2.0","id":1,"error":{"code":-32700,"message":"Parse error"}}`,
			code:     -32700,
			message:  "Parse error",
		},
		{
			name:     "InvalidRequest",
			jsonData: `{"jsonrpc":"2.0","id":2,"error":{"code":-32600,"message":"Invalid Request"}}`,
			code:     -32600,
			message:  "Invalid Request",
		},
		{
			name:     "MethodNotFound",
			jsonData: `{"jsonrpc":"2.0","id":3,"error":{"code":-32601,"message":"Method not found"}}`,
			code:     -32601,
			message:  "Method not found",
		},
		{
			name:     "InvalidParams",
			jsonData: `{"jsonrpc":"2.0","id":4,"error":{"code":-32602,"message":"Invalid params"}}`,
			code:     -32602,
			message:  "Invalid params",
		},
		{
			name:     "InternalError",
			jsonData: `{"jsonrpc":"2.0","id":5,"error":{"code":-32603,"message":"Internal error"}}`,
			code:     -32603,
			message:  "Internal error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var resp mcp.Response
			if err := json.Unmarshal([]byte(tc.jsonData), &resp); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if resp.Error == nil {
				t.Fatal("expected error in response")
			}
			if resp.Error.Code != tc.code {
				t.Errorf("expected code %d, got %d", tc.code, resp.Error.Code)
			}
			if resp.Error.Message != tc.message {
				t.Errorf("expected message %q, got %q", tc.message, resp.Error.Message)
			}
		})
	}
}

// Concurrent request handling tests

func TestClientConcurrentRequests(t *testing.T) {
	// Test that multiple concurrent mock calls work
	mockCaller := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
		},
	}

	info := mcp.ToolInfo{Name: "test_tool"}
	adapter := mcp.NewToolAdapter(info, mockCaller)

	const numGoroutines = 10
	done := make(chan bool, numGoroutines)
	ctx := context.Background()

	for i := 0; i < numGoroutines; i++ {
		go func() {
			result, err := adapter.Execute(ctx, map[string]any{"test": "data"})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !result.Success {
				t.Error("expected success")
			}
			done <- true
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

func TestRequestIDIncrement(t *testing.T) {
	// Test that request IDs increment properly for concurrent requests
	req1 := mcp.NewRequest("test", nil)
	req2 := mcp.NewRequest("test", nil)
	req3 := mcp.NewRequest("test", nil)

	if req1.ID == 0 || req2.ID == 0 || req3.ID == 0 {
		t.Error("expected non-zero request IDs")
	}

	if req2.ID <= req1.ID {
		t.Error("expected req2.ID > req1.ID")
	}

	if req3.ID <= req2.ID {
		t.Error("expected req3.ID > req2.ID")
	}
}

// Tool call timeout scenarios

func TestToolCallTimeout(t *testing.T) {
	mockCaller := &mockMCPCallerWithDelay{
		delay: 100 * time.Millisecond,
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "slow result"}},
		},
	}

	info := mcp.ToolInfo{Name: "slow_tool"}
	adapter := mcp.NewToolAdapter(info, mockCaller)

	// Create a context with a very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := adapter.Execute(ctx, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestToolCallContextCancellation(t *testing.T) {
	mockCaller := &mockMCPCallerWithDelay{
		delay: 100 * time.Millisecond,
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
		},
	}

	info := mcp.ToolInfo{Name: "cancellable_tool"}
	adapter := mcp.NewToolAdapter(info, mockCaller)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	_, err := adapter.Execute(ctx, nil)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// Client close and cleanup tests

func TestClientClose(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "cat",
		Args:      []string{},
	}
	client := mcp.NewClient(config)

	// Close before start should not error
	err := client.Close()
	if err != nil {
		t.Errorf("unexpected error closing non-started client: %v", err)
	}
}

func TestClientDoubleClose(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "cat",
		Args:      []string{},
	}
	client := mcp.NewClient(config)

	// Close twice
	err1 := client.Close()
	err2 := client.Close()

	if err1 != nil {
		t.Errorf("unexpected error on first close: %v", err1)
	}
	if err2 != nil {
		t.Errorf("unexpected error on second close: %v", err2)
	}
}

// Mock caller with delay for timeout tests

type mockMCPCallerWithDelay struct {
	delay  time.Duration
	result *mcp.ToolCallResult
	err    error
}

func (m *mockMCPCallerWithDelay) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolCallResult, error) {
	select {
	case <-time.After(m.delay):
		return m.result, m.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Additional edge case tests

func TestClientServerConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		config mcp.ServerConfig
	}{
		{
			name: "EmptyCommand",
			config: mcp.ServerConfig{
				Name:      "test",
				Transport: "stdio",
				Command:   "",
			},
		},
		{
			name: "WithEnvVars",
			config: mcp.ServerConfig{
				Name:      "test",
				Transport: "stdio",
				Command:   "echo",
				Env:       map[string]string{"TEST": "value"},
			},
		},
		{
			name: "WithMultipleArgs",
			config: mcp.ServerConfig{
				Name:      "test",
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"arg1", "arg2", "arg3"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := mcp.NewClient(tt.config)
			if client == nil {
				t.Error("expected non-nil client")
			}
		})
	}
}

func TestToolCallResultMultipleContentBlocks(t *testing.T) {
	mockCaller := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{
				{Type: "text", Text: "block 1"},
				{Type: "text", Text: "block 2"},
				{Type: "image", MimeType: "image/png", Data: "base64data"},
			},
		},
	}

	info := mcp.ToolInfo{Name: "multi_block_tool"}
	adapter := mcp.NewToolAdapter(info, mockCaller)

	result, err := adapter.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	// Should concatenate text blocks and represent non-text blocks
	expected := "block 1\nblock 2\n[image: image/png]"
	if result.Output != expected {
		t.Errorf("unexpected output: got %q, want %q", result.Output, expected)
	}
}

func TestToolInfoWithComplexSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name",
			},
			"age": map[string]any{
				"type":    "number",
				"minimum": 0,
			},
		},
		"required": []string{"name"},
	}

	info := mcp.ToolInfo{
		Name:        "complex_tool",
		Description: "A tool with complex schema",
		InputSchema: schema,
	}

	if info.InputSchema["type"] != "object" {
		t.Error("expected object type in schema")
	}

	props, ok := info.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be map")
	}
	if len(props) != 2 {
		t.Errorf("expected 2 properties, got %d", len(props))
	}
}

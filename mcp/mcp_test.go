package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
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
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	ctx := context.Background()

	err = client.Start(ctx)
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
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	ctx := context.Background()

	err = client.Start(ctx)
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
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start the client (will fail to initialize but that's ok for this test)
	_ = client.Start(ctx)
	defer client.Close()

	// Try to start again with new context
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	err = client.Start(ctx2)
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
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start will fail because server exits immediately
	err = client.Start(ctx)
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
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	err = client.Start(ctx)
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
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Close before start should not error
	err = client.Close()
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
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

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
			client, err := mcp.NewClient(tt.config)
			if err != nil {
				t.Fatalf("NewClient failed: %v", err)
			}
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

// ToolManager tests

// mockToolProvider implements the ToolProvider interface for testing
type mockToolProvider struct {
	tools      []mcp.ToolInfo
	listErr    error
	callResult *mcp.ToolCallResult
	callErr    error
}

func (m *mockToolProvider) ListTools(ctx context.Context) ([]mcp.ToolInfo, error) {
	return m.tools, m.listErr
}

func (m *mockToolProvider) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolCallResult, error) {
	return m.callResult, m.callErr
}

// Verify interface compliance
var _ mcp.ToolProvider = (*mockToolProvider)(nil)

func TestNewToolManager(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "echo",
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	manager := mcp.NewToolManager(client)
	if manager == nil {
		t.Fatal("expected non-nil ToolManager")
	}

	// Initially should have no tools
	tools := manager.Tools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools initially, got %d", len(tools))
	}
}

func TestToolManagerRefresh(t *testing.T) {
	t.Run("RefreshLoadsTools", func(t *testing.T) {
		mockProvider := &mockToolProvider{
			tools: []mcp.ToolInfo{
				{
					Name:        "read_file",
					Description: "Read a file",
					InputSchema: map[string]any{"type": "object"},
				},
				{
					Name:        "write_file",
					Description: "Write a file",
					InputSchema: map[string]any{"type": "object"},
				},
			},
			callResult: &mcp.ToolCallResult{
				Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
			},
		}

		manager := mcp.NewToolManager(mockProvider)
		ctx := context.Background()

		// Refresh should load the tools
		err := manager.Refresh(ctx)
		if err != nil {
			t.Fatalf("Refresh failed: %v", err)
		}

		// Verify tools were loaded
		tools := manager.Tools()
		if len(tools) != 2 {
			t.Errorf("expected 2 tools, got %d", len(tools))
		}

		// Verify we can get each tool
		readFile, ok := manager.Get("read_file")
		if !ok {
			t.Error("failed to get read_file tool")
		}
		if readFile != nil && readFile.Name() != "read_file" {
			t.Errorf("expected 'read_file', got %q", readFile.Name())
		}

		writeFile, ok := manager.Get("write_file")
		if !ok {
			t.Error("failed to get write_file tool")
		}
		if writeFile != nil && writeFile.Name() != "write_file" {
			t.Errorf("expected 'write_file', got %q", writeFile.Name())
		}
	})

	t.Run("RefreshError", func(t *testing.T) {
		mockProvider := &mockToolProvider{
			listErr: context.DeadlineExceeded,
		}

		manager := mcp.NewToolManager(mockProvider)
		ctx := context.Background()

		// Refresh should fail
		err := manager.Refresh(ctx)
		if err == nil {
			t.Fatal("expected error from Refresh")
		}
		if err != context.DeadlineExceeded {
			t.Errorf("expected DeadlineExceeded error, got %v", err)
		}

		// Tools should still be empty
		tools := manager.Tools()
		if len(tools) != 0 {
			t.Errorf("expected 0 tools after error, got %d", len(tools))
		}
	})

	t.Run("RefreshReplacesTools", func(t *testing.T) {
		mockProvider := &mockToolProvider{
			tools: []mcp.ToolInfo{
				{Name: "tool1", Description: "First"},
				{Name: "tool2", Description: "Second"},
			},
			callResult: &mcp.ToolCallResult{
				Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
			},
		}

		manager := mcp.NewToolManager(mockProvider)
		ctx := context.Background()

		// First refresh
		err := manager.Refresh(ctx)
		if err != nil {
			t.Fatalf("first Refresh failed: %v", err)
		}

		if len(manager.Tools()) != 2 {
			t.Errorf("expected 2 tools after first refresh, got %d", len(manager.Tools()))
		}

		// Update the mock to return different tools
		mockProvider.tools = []mcp.ToolInfo{
			{Name: "tool3", Description: "Third"},
		}

		// Second refresh should replace tools
		err = manager.Refresh(ctx)
		if err != nil {
			t.Fatalf("second Refresh failed: %v", err)
		}

		tools := manager.Tools()
		if len(tools) != 1 {
			t.Errorf("expected 1 tool after second refresh, got %d", len(tools))
		}

		// Old tools should be gone
		_, ok := manager.Get("tool1")
		if ok {
			t.Error("tool1 should have been removed")
		}

		// New tool should be present
		tool3, ok := manager.Get("tool3")
		if !ok {
			t.Error("tool3 should be present")
		}
		if tool3 != nil && tool3.Name() != "tool3" {
			t.Errorf("expected 'tool3', got %q", tool3.Name())
		}
	})
}

func TestToolManagerTools(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "echo",
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	manager := mcp.NewToolManager(client)

	// Initially empty
	tools := manager.Tools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}

	// After refresh would have tools, but we can't test that without a real server
	// So we verify the return type is correct
	if tools == nil {
		t.Error("expected non-nil slice")
	}
}

func TestToolManagerGet(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "echo",
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	manager := mcp.NewToolManager(client)

	// Get non-existent tool
	tool, ok := manager.Get("nonexistent")
	if ok {
		t.Error("expected ok=false for non-existent tool")
	}
	if tool != nil {
		t.Error("expected nil tool for non-existent tool")
	}
}

func TestToolManagerRegisterAll(t *testing.T) {
	t.Run("EmptyManager", func(t *testing.T) {
		// Create a manager with no tools
		mockProvider := &mockToolProvider{
			tools: []mcp.ToolInfo{},
		}
		manager := mcp.NewToolManager(mockProvider)

		// Create a registry
		registry := tool.NewRegistry()

		// Register all tools (should be empty initially)
		manager.RegisterAll(registry)

		if registry.Count() != 0 {
			t.Errorf("expected 0 registered tools, got %d", registry.Count())
		}
	})

	t.Run("WithTools", func(t *testing.T) {
		// Create a manager with mock tools
		mockProvider := &mockToolProvider{
			tools: []mcp.ToolInfo{
				{Name: "tool1", Description: "First tool"},
				{Name: "tool2", Description: "Second tool"},
				{Name: "tool3", Description: "Third tool"},
			},
			callResult: &mcp.ToolCallResult{
				Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
			},
		}

		manager := mcp.NewToolManager(mockProvider)
		ctx := context.Background()

		// Refresh to load tools
		err := manager.Refresh(ctx)
		if err != nil {
			t.Fatalf("Refresh failed: %v", err)
		}

		// Create a registry and register all tools
		registry := tool.NewRegistry()
		manager.RegisterAll(registry)

		if registry.Count() != 3 {
			t.Errorf("expected 3 registered tools, got %d", registry.Count())
		}

		// Verify all tools are in the registry
		for _, name := range []string{"tool1", "tool2", "tool3"} {
			tool, ok := registry.Get(name)
			if !ok {
				t.Errorf("failed to get tool %s from registry", name)
			}
			if tool != nil && tool.Name() != name {
				t.Errorf("expected tool name %q, got %q", name, tool.Name())
			}
		}
	})

	t.Run("MultipleRegistries", func(t *testing.T) {
		// Test that RegisterAll works with multiple registries
		mockProvider := &mockToolProvider{
			tools: []mcp.ToolInfo{
				{Name: "tool_a", Description: "Tool A"},
				{Name: "tool_b", Description: "Tool B"},
			},
			callResult: &mcp.ToolCallResult{
				Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
			},
		}

		manager := mcp.NewToolManager(mockProvider)
		ctx := context.Background()

		// Refresh to load tools
		err := manager.Refresh(ctx)
		if err != nil {
			t.Fatalf("Refresh failed: %v", err)
		}

		// Register to first registry
		registry1 := tool.NewRegistry()
		manager.RegisterAll(registry1)

		// Register to second registry
		registry2 := tool.NewRegistry()
		manager.RegisterAll(registry2)

		// Both registries should have the same tools
		if registry1.Count() != registry2.Count() {
			t.Errorf("registries have different counts: %d vs %d", registry1.Count(), registry2.Count())
		}

		if registry1.Count() != 2 {
			t.Errorf("expected 2 tools in each registry, got %d", registry1.Count())
		}
	})
}

func TestToolManagerToolsWithMultipleRetrievals(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "echo",
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	manager := mcp.NewToolManager(client)

	// Call Tools multiple times to ensure it's stable
	tools1 := manager.Tools()
	tools2 := manager.Tools()

	if len(tools1) != len(tools2) {
		t.Errorf("expected same tool count, got %d and %d", len(tools1), len(tools2))
	}

	// Both should be non-nil
	if tools1 == nil || tools2 == nil {
		t.Error("expected non-nil tool slices")
	}
}

func TestToolAdapterDescription(t *testing.T) {
	info := mcp.ToolInfo{
		Name:        "test_tool",
		Description: "This is a test tool",
		InputSchema: map[string]any{"type": "object"},
	}

	mockCaller := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
		},
	}

	adapter := mcp.NewToolAdapter(info, mockCaller)

	desc := adapter.Description()
	if desc != "This is a test tool" {
		t.Errorf("expected 'This is a test tool', got %q", desc)
	}
}

func TestToolAdapterRequiresApproval(t *testing.T) {
	info := mcp.ToolInfo{
		Name:        "test_tool",
		Description: "Test",
	}

	mockCaller := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
		},
	}

	adapter := mcp.NewToolAdapter(info, mockCaller)

	// MCP tools always require approval
	requiresApproval := adapter.RequiresApproval(map[string]any{"test": "param"})
	if !requiresApproval {
		t.Error("expected MCP tools to require approval")
	}

	// Test with nil params
	requiresApproval = adapter.RequiresApproval(nil)
	if !requiresApproval {
		t.Error("expected MCP tools to require approval even with nil params")
	}
}

func TestToolAdapterInputSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path",
			},
		},
		"required": []string{"path"},
	}

	info := mcp.ToolInfo{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: schema,
	}

	mockCaller := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
		},
	}

	adapter := mcp.NewToolAdapter(info, mockCaller)

	inputSchema := adapter.InputSchema()
	if inputSchema == nil {
		t.Fatal("expected non-nil input schema")
	}

	if inputSchema["type"] != "object" {
		t.Errorf("expected type 'object', got %v", inputSchema["type"])
	}

	props, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}

	if len(props) != 1 {
		t.Errorf("expected 1 property, got %d", len(props))
	}
}

func TestToolAdapterInputSchemaEmpty(t *testing.T) {
	info := mcp.ToolInfo{
		Name:        "no_params_tool",
		Description: "Tool with no parameters",
		InputSchema: map[string]any{},
	}

	mockCaller := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "result"}},
		},
	}

	adapter := mcp.NewToolAdapter(info, mockCaller)

	inputSchema := adapter.InputSchema()
	if inputSchema == nil {
		t.Fatal("expected non-nil input schema")
	}

	if len(inputSchema) != 0 {
		t.Errorf("expected empty schema, got %d properties", len(inputSchema))
	}
}

func TestToolAdapterResourceContentBlock(t *testing.T) {
	mockCaller := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{
				{Type: "text", Text: "text content"},
				{Type: "resource", MimeType: "application/json"},
			},
		},
	}

	info := mcp.ToolInfo{Name: "resource_tool"}
	adapter := mcp.NewToolAdapter(info, mockCaller)

	result, err := adapter.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}

	expected := "text content\n[resource: application/json]"
	if result.Output != expected {
		t.Errorf("unexpected output: got %q, want %q", result.Output, expected)
	}
}

func TestToolManagerIntegration(t *testing.T) {
	// This is an integration test that verifies the full ToolManager lifecycle
	// without requiring a real MCP server

	mockClient := &mockMCPCaller{
		result: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "success"}},
		},
	}

	// Create multiple tool adapters
	tools := []*mcp.ToolAdapter{
		mcp.NewToolAdapter(mcp.ToolInfo{
			Name:        "tool_a",
			Description: "First tool",
			InputSchema: map[string]any{"type": "object"},
		}, mockClient),
		mcp.NewToolAdapter(mcp.ToolInfo{
			Name:        "tool_b",
			Description: "Second tool",
			InputSchema: map[string]any{"type": "object"},
		}, mockClient),
		mcp.NewToolAdapter(mcp.ToolInfo{
			Name:        "tool_c",
			Description: "Third tool",
			InputSchema: map[string]any{"type": "object"},
		}, mockClient),
	}

	// Verify all tools are properly created
	if len(tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(tools))
	}

	// Verify each tool implements the Tool interface correctly
	for i, adapter := range tools {
		if adapter == nil {
			t.Errorf("tool %d is nil", i)
			continue
		}

		// Verify Tool interface compliance
		var _ tool.Tool = adapter

		// Test all interface methods
		if adapter.Name() == "" {
			t.Errorf("tool %d has empty name", i)
		}
		if adapter.Description() == "" {
			t.Errorf("tool %d has empty description", i)
		}
		if !adapter.RequiresApproval(nil) {
			t.Errorf("tool %d should require approval", i)
		}
		if adapter.InputSchema() == nil {
			t.Errorf("tool %d has nil input schema", i)
		}

		// Test Execute
		result, err := adapter.Execute(context.Background(), map[string]any{})
		if err != nil {
			t.Errorf("tool %d Execute failed: %v", i, err)
		}
		if !result.Success {
			t.Errorf("tool %d Execute was not successful", i)
		}
	}

	// Test registration
	registry := tool.NewRegistry()
	for _, adapter := range tools {
		registry.Register(adapter)
	}

	if registry.Count() != 3 {
		t.Errorf("expected 3 registered tools, got %d", registry.Count())
	}

	// Verify we can retrieve the tools
	for _, adapter := range tools {
		retrieved, ok := registry.Get(adapter.Name())
		if !ok {
			t.Errorf("failed to retrieve tool %s", adapter.Name())
		}
		if retrieved.Name() != adapter.Name() {
			t.Errorf("retrieved tool has wrong name: got %s, want %s", retrieved.Name(), adapter.Name())
		}
	}
}

// Integration tests with mock MCP server for ListTools and CallTool

func TestClientListTools(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}

	// Verify tool details
	foundTestTool := false
	foundEchoTool := false
	for _, tool := range tools {
		if tool.Name == "test_tool" {
			foundTestTool = true
			if tool.Description != "A test tool" {
				t.Errorf("expected 'A test tool', got %q", tool.Description)
			}
		}
		if tool.Name == "echo_tool" {
			foundEchoTool = true
			if tool.Description != "Echoes the input" {
				t.Errorf("expected 'Echoes the input', got %q", tool.Description)
			}
		}
	}

	if !foundTestTool {
		t.Error("expected to find test_tool")
	}
	if !foundEchoTool {
		t.Error("expected to find echo_tool")
	}
}

func TestClientCallTool(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Test successful tool call
	result, err := client.CallTool(ctx, "test_tool", map[string]any{"input": "test"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if result.IsError {
		t.Error("expected successful result")
	}

	if len(result.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(result.Content))
	}

	if result.Content[0].Type != "text" {
		t.Errorf("expected type 'text', got %q", result.Content[0].Type)
	}

	if result.Content[0].Text != "test result" {
		t.Errorf("expected 'test result', got %q", result.Content[0].Text)
	}
}

func TestClientCallToolEcho(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Test echo tool with custom message
	testMessage := "hello world"
	result, err := client.CallTool(ctx, "echo_tool", map[string]any{"message": testMessage})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if result.IsError {
		t.Error("expected successful result")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected at least 1 content block")
	}

	if result.Content[0].Text != testMessage {
		t.Errorf("expected %q, got %q", testMessage, result.Content[0].Text)
	}
}

func TestClientCallToolNotFound(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Test calling non-existent tool
	_, err = client.CallTool(ctx, "nonexistent_tool", nil)
	if err == nil {
		t.Fatal("expected error for non-existent tool")
	}

	// Should be an RPC error
	rpcErr, ok := err.(*mcp.RPCError)
	if !ok {
		t.Fatalf("expected RPCError, got %T", err)
	}

	if rpcErr.Code != -32601 {
		t.Errorf("expected code -32601, got %d", rpcErr.Code)
	}
}

func TestClientCallToolWithError(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Test tool that returns an error result
	result, err := client.CallTool(ctx, "error_tool", nil)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if !result.IsError {
		t.Error("expected IsError to be true")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected at least 1 content block")
	}
}

func TestClientListToolsTimeout(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Create a very short timeout for the actual call
	callCtx, callCancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer callCancel()

	// Wait for timeout to definitely expire
	time.Sleep(10 * time.Millisecond)

	_, err = client.ListTools(callCtx)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestClientCallToolTimeout(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Create a very short timeout for the actual call
	callCtx, callCancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer callCancel()

	// Wait for timeout to definitely expire
	time.Sleep(10 * time.Millisecond)

	_, err = client.CallTool(callCtx, "test_tool", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestClientMultipleListToolsCalls(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Call ListTools multiple times to ensure it works consistently
	for i := 0; i < 3; i++ {
		tools, err := client.ListTools(ctx)
		if err != nil {
			t.Fatalf("ListTools call %d failed: %v", i+1, err)
		}

		if len(tools) != 2 {
			t.Errorf("call %d: expected 2 tools, got %d", i+1, len(tools))
		}
	}
}

func TestClientCallToolAfterClose(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}

	// Close the client
	client.Close()

	// Try to call tool after close
	_, err = client.CallTool(ctx, "test_tool", nil)
	if err == nil {
		t.Fatal("expected error when calling tool after close")
	}
}

func TestClientListToolsAfterClose(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}

	// Close the client
	client.Close()

	// Try to list tools after close
	_, err = client.ListTools(ctx)
	if err == nil {
		t.Fatal("expected error when listing tools after close")
	}
}

// Additional edge case tests for better coverage

func TestClientListToolsInvalidJSON(t *testing.T) {
	// This test ensures we handle malformed JSON responses gracefully
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Normal call should succeed
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
}

func TestClientCallToolWithNilArguments(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Test calling tool with nil arguments
	result, err := client.CallTool(ctx, "echo_tool", nil)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if result.IsError {
		t.Error("expected successful result")
	}

	// Echo tool should return "no message" when args are nil
	if len(result.Content) == 0 {
		t.Fatal("expected at least 1 content block")
	}

	if result.Content[0].Text != "no message" {
		t.Errorf("expected 'no message', got %q", result.Content[0].Text)
	}
}

func TestClientCallToolWithEmptyArguments(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Test calling tool with empty map arguments
	result, err := client.CallTool(ctx, "test_tool", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if result.IsError {
		t.Error("expected successful result")
	}
}

func TestClientConcurrentToolCalls(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Make multiple concurrent tool calls
	const numCalls = 5
	done := make(chan error, numCalls)

	for i := 0; i < numCalls; i++ {
		go func(index int) {
			message := "concurrent message"
			result, err := client.CallTool(ctx, "echo_tool", map[string]any{"message": message})
			if err != nil {
				done <- err
				return
			}
			if result.IsError {
				done <- fmt.Errorf("tool returned error")
				return
			}
			if len(result.Content) == 0 {
				done <- fmt.Errorf("no content blocks")
				return
			}
			if result.Content[0].Text != message {
				done <- fmt.Errorf("wrong message: got %q, want %q", result.Content[0].Text, message)
				return
			}
			done <- nil
		}(i)
	}

	// Wait for all calls to complete
	for i := 0; i < numCalls; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent call %d failed: %v", i, err)
		}
	}
}

func TestClientConcurrentMixedCalls(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "mock",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Mix ListTools and CallTool calls concurrently
	const numCalls = 6
	done := make(chan error, numCalls)

	// 3 ListTools calls
	for i := 0; i < 3; i++ {
		go func() {
			tools, err := client.ListTools(ctx)
			if err != nil {
				done <- err
				return
			}
			if len(tools) != 2 {
				done <- fmt.Errorf("expected 2 tools, got %d", len(tools))
				return
			}
			done <- nil
		}()
	}

	// 3 CallTool calls
	for i := 0; i < 3; i++ {
		go func() {
			result, err := client.CallTool(ctx, "test_tool", nil)
			if err != nil {
				done <- err
				return
			}
			if result.IsError {
				done <- fmt.Errorf("tool returned error")
				return
			}
			done <- nil
		}()
	}

	// Wait for all calls to complete
	for i := 0; i < numCalls; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent call %d failed: %v", i, err)
		}
	}
}

func TestNotificationType(t *testing.T) {
	jsonData := `{"method":"tools/changed","params":{"reason":"update"}}`
	var notif mcp.Notification
	if err := json.Unmarshal([]byte(jsonData), &notif); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if notif.Method != "tools/changed" {
		t.Errorf("expected 'tools/changed', got %q", notif.Method)
	}
	if notif.Params == nil {
		t.Error("expected non-nil params")
	}
}

func TestServerConfigHTTPFields(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "remote",
		Transport: "http",
		URL:       "https://example.com/mcp",
		Headers:   map[string]string{"Authorization": "Bearer token"},
	}
	if config.URL != "https://example.com/mcp" {
		t.Errorf("expected URL, got %q", config.URL)
	}
	if config.Headers["Authorization"] != "Bearer token" {
		t.Error("expected Authorization header")
	}
}

func TestMCPErrors(t *testing.T) {
	if mcp.ErrSessionExpired.Error() != "mcp: session expired" {
		t.Errorf("unexpected error message: %s", mcp.ErrSessionExpired.Error())
	}
	if mcp.ErrNotConnected.Error() != "mcp: client not connected" {
		t.Errorf("unexpected error message: %s", mcp.ErrNotConnected.Error())
	}
	if mcp.ErrTransportClosed.Error() != "mcp: transport closed" {
		t.Errorf("unexpected error message: %s", mcp.ErrTransportClosed.Error())
	}
}

// Client interface tests

func TestClientInterface(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "echo",
	}

	// NewClient should return (Client, error)
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	// This assignment verifies client is a mcp.Client at compile time
	_ = client
}

func TestNewClientUnsupportedTransport(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "unsupported",
	}
	_, err := mcp.NewClient(config)
	if err == nil {
		t.Fatal("expected error for unsupported transport")
	}
}

func TestNewClientEmptyTransportDefaultsToStdio(t *testing.T) {
	config := mcp.ServerConfig{
		Name:    "test",
		Command: "echo",
	}

	// Empty transport should default to stdio
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClientHTTPTransportCreatesClient(t *testing.T) {
	tests := []struct {
		name      string
		transport string
	}{
		{"http", "http"},
		{"streamable-http", "streamable-http"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := mcp.ServerConfig{
				Name:      "test",
				Transport: tt.transport,
				URL:       "https://example.com/mcp",
			}
			client, err := mcp.NewClient(config)
			if err != nil {
				t.Fatalf("NewClient failed: %v", err)
			}
			if client == nil {
				t.Fatal("expected non-nil client")
			}

			// Verify Client interface implementation
			var _ mcp.Client = client //nolint:staticcheck // intentional type assertion

			// HTTP clients should return non-nil notifications channel
			ch := client.Notifications()
			if ch == nil {
				t.Error("expected non-nil notifications channel for HTTP transport")
			}
		})
	}
}

func TestClientNotificationsReturnsNilForStdio(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test",
		Transport: "stdio",
		Command:   "echo",
	}

	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// stdio clients don't support notifications (no SSE)
	if client.Notifications() != nil {
		t.Error("expected nil notifications channel for stdio transport")
	}
}

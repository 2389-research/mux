package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/mux/mcp"
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

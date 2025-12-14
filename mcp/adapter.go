// ABOUTME: Implements the Tool adapter - wraps MCP tools to implement
// ABOUTME: the mux Tool interface for seamless integration.
package mcp

import (
	"context"
	"strings"

	"github.com/2389-research/mux/tool"
)

// ToolCaller is the interface for calling MCP tools.
type ToolCaller interface {
	CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error)
}

// ToolAdapter wraps an MCP tool to implement the Tool interface.
type ToolAdapter struct {
	info   ToolInfo
	caller ToolCaller
}

// NewToolAdapter creates a new adapter for an MCP tool.
func NewToolAdapter(info ToolInfo, caller ToolCaller) *ToolAdapter {
	return &ToolAdapter{info: info, caller: caller}
}

// Name returns the tool name.
func (a *ToolAdapter) Name() string { return a.info.Name }

// Description returns the tool description.
func (a *ToolAdapter) Description() string { return a.info.Description }

// RequiresApproval returns true - MCP tools require approval by default.
func (a *ToolAdapter) RequiresApproval(params map[string]any) bool { return true }

// Execute calls the MCP tool and converts the result.
//
// Return pattern note: This method returns both a *tool.Result and an error.
// When CallTool fails (network error, timeout, etc), we return BOTH:
//   - An error result object (for consistent tool.Result handling)
//   - The actual error (for proper error propagation)
// This dual return allows callers to either handle the error directly OR
// use the Result object uniformly with other tool results.
func (a *ToolAdapter) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	mcpResult, err := a.caller.CallTool(ctx, a.info.Name, params)
	if err != nil {
		// Return BOTH the error result AND the error (see method doc for explanation)
		return tool.NewErrorResult(a.info.Name, err.Error()), err
	}

	output := a.extractOutput(mcpResult)
	result := tool.NewResult(a.info.Name, !mcpResult.IsError, output, "")
	if mcpResult.IsError {
		result.Error = output
	}
	return result, nil
}

func (a *ToolAdapter) extractOutput(mcpResult *ToolCallResult) string {
	var parts []string
	for _, block := range mcpResult.Content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "image", "resource":
			parts = append(parts, "["+block.Type+": "+block.MimeType+"]")
		}
	}
	return strings.Join(parts, "\n")
}

// InputSchema returns the JSON schema for tool parameters.
func (a *ToolAdapter) InputSchema() map[string]any { return a.info.InputSchema }

// ToolManager manages multiple MCP tool adapters.
type ToolManager struct {
	client *Client
	tools  map[string]*ToolAdapter
}

// NewToolManager creates a manager for MCP tools.
func NewToolManager(client *Client) *ToolManager {
	return &ToolManager{client: client, tools: make(map[string]*ToolAdapter)}
}

// Refresh reloads tools from the MCP server.
func (m *ToolManager) Refresh(ctx context.Context) error {
	infos, err := m.client.ListTools(ctx)
	if err != nil {
		return err
	}
	m.tools = make(map[string]*ToolAdapter)
	for _, info := range infos {
		m.tools[info.Name] = NewToolAdapter(info, m.client)
	}
	return nil
}

// Tools returns all available tool adapters.
func (m *ToolManager) Tools() []*ToolAdapter {
	tools := make([]*ToolAdapter, 0, len(m.tools))
	for _, t := range m.tools {
		tools = append(tools, t)
	}
	return tools
}

// Get retrieves a specific tool adapter.
func (m *ToolManager) Get(name string) (*ToolAdapter, bool) {
	t, ok := m.tools[name]
	return t, ok
}

// RegisterAll adds all MCP tools to a tool registry.
func (m *ToolManager) RegisterAll(registry *tool.Registry) {
	for _, t := range m.tools {
		registry.Register(t)
	}
}

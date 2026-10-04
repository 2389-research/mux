//go:build integration

// ABOUTME: Verifies public MCP and tool execution contracts against a real stdio server.
// ABOUTME: Uses isolated process logs to prove successful calls and denied-call isolation.
package mux_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/mux/mcp"
	"github.com/2389-research/mux/tool"
)

func TestMCPProcessContract(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) {
		ctx, client, logPath := startProcessClient(t)
		infos, err := client.ListTools(ctx)
		if err != nil || len(infos) != 1 || infos[0].Name != "echo" {
			t.Fatalf("discover echo: infos=%+v err=%v", infos, err)
		}
		// Unicode, escaped characters and a frame exceeding bufio's default
		// buffer must survive the real JSON-RPC transport byte for byte.
		message := randomNonce(t) + " ☃ \"quoted\" \\ " + strings.Repeat("x", 128*1024)
		result, err := client.CallTool(ctx, "echo", map[string]any{"message": message})
		if err != nil || result.IsError || len(result.Content) != 1 || result.Content[0].Text != message {
			t.Fatalf("echo did not preserve the message: err=%v", err)
		}
		assertProcessLog(t, logPath, message+"\n")
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := client.ListTools(ctx); !errors.Is(err, mcp.ErrTransportClosed) {
			t.Fatalf("ListTools after Close = %v", err)
		}
		if err := client.Start(ctx); !errors.Is(err, mcp.ErrTransportClosed) {
			t.Fatalf("Start after Close = %v", err)
		}
	})

	t.Run("error_recovery", func(t *testing.T) {
		ctx, client, logPath := startProcessClient(t)
		for _, call := range []struct {
			name string
			args map[string]any
		}{
			{"missing", map[string]any{}},
			{"echo", map[string]any{"message": ""}},
			{"echo", map[string]any{"message": 42}},
		} {
			if _, err := client.CallTool(ctx, call.name, call.args); err == nil {
				t.Fatalf("invalid call %s(%v) succeeded", call.name, call.args)
			}
		}
		message := randomNonce(t)
		result, err := client.CallTool(ctx, "echo", map[string]any{"message": message})
		if err != nil || result.IsError || len(result.Content) != 1 || result.Content[0].Text != message {
			t.Fatalf("valid call after protocol errors failed: err=%v", err)
		}
		assertProcessLog(t, logPath, message+"\n")
	})

	t.Run("approval", func(t *testing.T) {
		ctx, client, logPath := startProcessClient(t)
		infos, err := client.ListTools(ctx)
		if err != nil || len(infos) != 1 {
			t.Fatalf("discover tool: infos=%+v err=%v", infos, err)
		}
		registry := tool.NewRegistry()
		registry.Register(mcp.NewToolAdapter(infos[0], client))
		executor := tool.NewExecutor(registry)
		message := randomNonce(t)
		args := map[string]any{"message": message}
		if _, err := executor.Execute(ctx, "echo", args); !errors.Is(err, tool.ErrApprovalRequired) {
			t.Fatalf("missing approval = %v", err)
		}
		if _, err := executor.Execute(ctx, "echo", args, tool.WithApprovalDecision(false)); !errors.Is(err, tool.ErrApprovalDenied) {
			t.Fatalf("denied approval = %v", err)
		}
		if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unapproved calls reached the server: log stat = %v", err)
		}
		result, err := executor.Execute(ctx, "echo", args, tool.WithApprovalDecision(true))
		if err != nil || !result.Success || result.ModelText() != message {
			t.Fatalf("approved call failed: result=%+v err=%v", result, err)
		}
		assertProcessLog(t, logPath, message+"\n")
		// A decision applies to one call; it must not approve the next call.
		if _, err := executor.Execute(ctx, "echo", args); !errors.Is(err, tool.ErrApprovalRequired) {
			t.Fatalf("approval leaked to later call: %v", err)
		}
		assertProcessLog(t, logPath, message+"\n")
	})
}

func startProcessClient(t *testing.T) (context.Context, mcp.Client, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	logPath := filepath.Join(t.TempDir(), "echo.log")
	client, err := mcp.NewClient(mcp.ServerConfig{
		Name: "contract-echo", Transport: "stdio", Command: buildEchoServer(ctx, t),
		Env: map[string]string{"MUX_ECHO_SERVER_LOG": logPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close MCP process: %v", err)
		}
	})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, client, logPath
}

func assertProcessLog(t *testing.T, logPath, want string) {
	t.Helper()
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("server log differs: got %d bytes, want %d", len(got), len(want))
	}
}

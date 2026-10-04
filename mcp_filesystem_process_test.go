//go:build integration

// ABOUTME: Verifies interoperability with the independently maintained MCP filesystem server.
// ABOUTME: Creates, reads and lists only owned temporary files through the public mux client.
package mux_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/mux/mcp"
)

func TestMCPFilesystemProcessContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(repoRoot(t), "node_modules", "@modelcontextprotocol", "server-filesystem", "dist", "index.js")
	if _, err := os.Stat(server); err != nil {
		t.Fatal("reference MCP server missing; run npm ci --ignore-scripts: ", err)
	}
	client, err := mcp.NewClient(mcp.ServerConfig{Name: "filesystem", Transport: "stdio", Command: "node", Args: []string{server, dir}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	infos, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"write_file", "read_text_file", "list_directory"} {
		found := false
		for _, info := range infos {
			if info.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("reference server did not discover %s", name)
		}
	}
	path := filepath.Join(dir, "nonce.txt")
	nonce := randomNonce(t) + " ☃\n"
	call := func(name string, args map[string]any) string {
		t.Helper()
		result, err := client.CallTool(ctx, name, args)
		if err != nil || result.IsError {
			t.Fatalf("%s: result=%+v err=%v", name, result, err)
		}
		var text string
		for _, block := range result.Content {
			text += block.Text
		}
		return text
	}
	call("write_file", map[string]any{"path": path, "content": nonce})
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != nonce {
		t.Fatalf("actual file mismatch: err=%v", err)
	}
	if got := call("read_text_file", map[string]any{"path": path}); got != nonce {
		t.Fatalf("reference read mismatch: %q", got)
	}
	if got := call("list_directory", map[string]any{"path": dir}); !strings.Contains(got, "[FILE] nonce.txt") {
		t.Fatalf("reference directory result: %q", got)
	}
}

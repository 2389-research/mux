// ABOUTME: Full-featured mux example showing tools, child agents, coordinator.
// ABOUTME: Run with: ANTHROPIC_API_KEY=xxx go run main.go
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/coordinator"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

// --- Tools ---

// ReadTool reads a file from disk.
type ReadTool struct{}

func (t *ReadTool) Name() string        { return "read_file" }
func (t *ReadTool) Description() string { return "Read the contents of a file from disk" }
func (t *ReadTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read",
			},
		},
		"required": []string{"path"},
	}
}
func (t *ReadTool) RequiresApproval(map[string]any) bool { return false }
func (t *ReadTool) Execute(_ context.Context, p map[string]any) (*tool.Result, error) {
	path, ok := p["path"].(string)
	if !ok || path == "" {
		return tool.NewResult("read_file", false, "", "missing required parameter: path"), nil
	}

	// Clean the path to prevent directory traversal
	path = filepath.Clean(path)

	content, err := os.ReadFile(path)
	if err != nil {
		return tool.NewResult("read_file", false, "", fmt.Sprintf("failed to read file: %v", err)), nil
	}

	return tool.NewResult("read_file", true, string(content), ""), nil
}

// WriteTool writes content to a file on disk.
type WriteTool struct {
	coord *coordinator.Coordinator
}

func (t *WriteTool) Name() string        { return "write_file" }
func (t *WriteTool) Description() string { return "Write content to a file on disk" }
func (t *WriteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}
func (t *WriteTool) RequiresApproval(map[string]any) bool { return true } // Requires approval!
func (t *WriteTool) Execute(ctx context.Context, p map[string]any) (*tool.Result, error) {
	path, ok := p["path"].(string)
	if !ok || path == "" {
		return tool.NewResult("write_file", false, "", "missing required parameter: path"), nil
	}
	content, ok := p["content"].(string)
	if !ok {
		return tool.NewResult("write_file", false, "", "missing required parameter: content"), nil
	}

	// Clean the path to prevent directory traversal
	path = filepath.Clean(path)

	// Acquire lock on the file
	if t.coord != nil {
		if err := t.coord.Acquire(ctx, "main", path); err != nil {
			return tool.NewResult("write_file", false, "", fmt.Sprintf("lock failed: %v", err)), nil
		}
		defer func() { t.coord.Release("main", path) }() //nolint:errcheck // best-effort release
	}

	// Create parent directories if needed
	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return tool.NewResult("write_file", false, "", fmt.Sprintf("failed to create directory: %v", err)), nil
		}
	}

	// Write the file (0644 is intentional - user-readable files)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil { //nolint:gosec // G306: 0644 is intentional for user-created files
		return tool.NewResult("write_file", false, "", fmt.Sprintf("failed to write file: %v", err)), nil
	}

	return tool.NewResult("write_file", true, fmt.Sprintf("wrote %d bytes to %s", len(content), path), ""), nil
}

// SearchTool searches for text patterns in files.
type SearchTool struct {
	cache *coordinator.Cache
}

func (t *SearchTool) Name() string { return "search" }
func (t *SearchTool) Description() string {
	return "Search for text patterns in files within a directory"
}
func (t *SearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Text pattern to search for",
			},
			"directory": map[string]any{
				"type":        "string",
				"description": "Directory to search in (defaults to current directory)",
			},
		},
		"required": []string{"pattern"},
	}
}
func (t *SearchTool) RequiresApproval(map[string]any) bool { return false }
func (t *SearchTool) Execute(_ context.Context, p map[string]any) (*tool.Result, error) {
	pattern, ok := p["pattern"].(string)
	if !ok || pattern == "" {
		return tool.NewResult("search", false, "", "missing required parameter: pattern"), nil
	}

	dir, _ := p["directory"].(string)
	if dir == "" {
		dir = "."
	}
	dir = filepath.Clean(dir)

	// Check cache first
	cacheKey := fmt.Sprintf("%s:%s", dir, pattern)
	if t.cache != nil {
		if cached, ok := t.cache.Get(cacheKey); ok {
			return tool.NewResult("search", true, fmt.Sprintf("[CACHED] %s", cached), ""), nil
		}
	}

	var matches []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}
		if info.IsDir() {
			return nil
		}
		// Only search text files (skip binaries)
		if info.Size() > 1024*1024 { // Skip files > 1MB
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		if strings.Contains(string(content), pattern) {
			matches = append(matches, path)
		}
		return nil
	})

	if err != nil {
		return tool.NewResult("search", false, "", fmt.Sprintf("search failed: %v", err)), nil
	}

	var result string
	if len(matches) == 0 {
		result = fmt.Sprintf("No files containing '%s' found in %s", pattern, dir)
	} else {
		result = fmt.Sprintf("Found '%s' in %d file(s):\n%s", pattern, len(matches), strings.Join(matches, "\n"))
	}

	// Cache the result
	if t.cache != nil {
		t.cache.Set(cacheKey, result, 5*time.Minute)
	}

	return tool.NewResult("search", true, result, ""), nil
}

// --- Main ---

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Set ANTHROPIC_API_KEY")
		os.Exit(1)
	}

	// Create coordinator components
	coord := coordinator.New()
	cache := coordinator.NewCache()
	rateLimiter := coordinator.NewRateLimiter(10, 5) // 10 burst, 5/sec

	// Create registry with all tools
	registry := tool.NewRegistry()
	registry.Register(&ReadTool{})
	registry.Register(&WriteTool{coord: coord})
	registry.Register(&SearchTool{cache: cache})

	// Create LLM client
	llmClient := llm.NewAnthropicClient(apiKey, "claude-sonnet-4-20250514")

	// Create root agent with all tools
	rootAgent := agent.New(agent.Config{
		Name:         "root",
		Registry:     registry,
		LLMClient:    llmClient,
		SystemPrompt: "You are a helpful assistant with file and search tools.",
		ApprovalFunc: func(_ context.Context, t tool.Tool, _ map[string]any) (bool, error) {
			fmt.Printf("\n⚠️  Tool '%s' requires approval. Allow? [y/N] ", t.Name())
			var response string
			fmt.Scanln(&response) //nolint:errcheck // user input, ignore scan errors
			return strings.ToLower(response) == "y", nil
		},
	})

	// Create a restricted child agent (read-only)
	readOnlyAgent, err := rootAgent.SpawnChild(agent.Config{
		Name:         "reader",
		AllowedTools: []string{"read_file", "search"}, // No write_file!
		SystemPrompt: "You are a read-only assistant. You can read and search but not write.",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to spawn child: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Full mux example with coordinator features.")
	fmt.Println("Commands: /root, /reader, /status, quit")
	fmt.Println("- /root   : Use root agent (all tools)")
	fmt.Println("- /reader : Use read-only child agent")
	fmt.Println("- /status : Show coordinator status")

	currentAgent := rootAgent
	agentName := "root"
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Printf("\n[%s]> ", agentName)
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Handle commands
		switch input {
		case "quit":
			return
		case "/root":
			currentAgent = rootAgent
			agentName = "root"
			fmt.Println("Switched to root agent (all tools)")
			continue
		case "/reader":
			currentAgent = readOnlyAgent
			agentName = "reader"
			fmt.Println("Switched to read-only agent")
			continue
		case "/status":
			fmt.Printf("Cache entries: active\n")
			fmt.Printf("Rate limiter: 10 burst, 5/sec\n")
			fmt.Printf("Coordinator: ready\n")
			continue
		}

		// Apply rate limiting
		ctx := context.Background()
		if err := rateLimiter.Take(ctx, 1); err != nil {
			fmt.Fprintf(os.Stderr, "Rate limited: %v\n", err)
			continue
		}

		// Subscribe to events
		events := currentAgent.Subscribe()
		done := make(chan struct{})

		go func() {
			defer close(done)
			for ev := range events {
				switch ev.Type {
				case orchestrator.EventText:
					fmt.Print(ev.Text)
				case orchestrator.EventToolCall:
					fmt.Printf("\n🔧 [%s] %v\n", ev.ToolName, ev.ToolParams)
				case orchestrator.EventToolResult:
					if ev.Result.Success {
						fmt.Printf("✓ %s\n", ev.Result.Output)
					} else {
						fmt.Printf("✗ %s\n", ev.Result.Error)
					}
				case orchestrator.EventError:
					fmt.Fprintf(os.Stderr, "\n❌ Error: %v\n", ev.Error)
				}
			}
		}()

		if err := currentAgent.Run(ctx, input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}

		<-done
		fmt.Println()
	}
}

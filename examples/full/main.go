// ABOUTME: Full-featured mux example showing tools, child agents, coordinator.
// ABOUTME: Run with: ANTHROPIC_API_KEY=xxx go run main.go
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/coordinator"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

// --- Tools ---

// ReadTool simulates reading a file.
type ReadTool struct{}

func (t *ReadTool) Name() string                         { return "read_file" }
func (t *ReadTool) Description() string                  { return "Read a file: {\"path\": \"file.txt\"}" }
func (t *ReadTool) RequiresApproval(map[string]any) bool { return false }
func (t *ReadTool) Execute(_ context.Context, p map[string]any) (*tool.Result, error) {
	path, _ := p["path"].(string)
	return tool.NewResult("read_file", true, fmt.Sprintf("Contents of %s:\nHello, world!", path), ""), nil
}

// WriteTool simulates writing a file.
type WriteTool struct {
	coord *coordinator.Coordinator
}

func (t *WriteTool) Name() string { return "write_file" }
func (t *WriteTool) Description() string {
	return "Write a file: {\"path\": \"file.txt\", \"content\": \"...\"}"
}
func (t *WriteTool) RequiresApproval(map[string]any) bool { return true } // Requires approval!
func (t *WriteTool) Execute(ctx context.Context, p map[string]any) (*tool.Result, error) {
	path, _ := p["path"].(string)
	content, _ := p["content"].(string)

	// Acquire lock on the file
	if t.coord != nil {
		if err := t.coord.Acquire(ctx, "main", path); err != nil {
			return tool.NewResult("write_file", false, "", fmt.Sprintf("Lock failed: %v", err)), nil
		}
		defer func() { t.coord.Release("main", path) }() //nolint:errcheck // best-effort release
	}

	return tool.NewResult("write_file", true, fmt.Sprintf("Wrote %d bytes to %s", len(content), path), ""), nil
}

// SearchTool simulates searching.
type SearchTool struct {
	cache *coordinator.Cache
}

func (t *SearchTool) Name() string                         { return "search" }
func (t *SearchTool) Description() string                  { return "Search for info: {\"query\": \"...\"}" }
func (t *SearchTool) RequiresApproval(map[string]any) bool { return false }
func (t *SearchTool) Execute(_ context.Context, p map[string]any) (*tool.Result, error) {
	query, _ := p["query"].(string)

	// Check cache first
	if t.cache != nil {
		if cached, ok := t.cache.Get(query); ok {
			return tool.NewResult("search", true, fmt.Sprintf("[CACHED] %s", cached), ""), nil
		}
	}

	result := fmt.Sprintf("Search results for '%s': Found 3 items.", query)

	// Cache the result
	if t.cache != nil {
		t.cache.Set(query, result, 5*time.Minute)
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

// ABOUTME: Minimal mux example - a simple agent with one tool.
// ABOUTME: Run with: ANTHROPIC_API_KEY=xxx go run main.go (or OPENAI_API_KEY=xxx)
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

// CalculatorTool does simple math.
type CalculatorTool struct{}

func (t *CalculatorTool) Name() string                         { return "calculator" }
func (t *CalculatorTool) Description() string                  { return "Add two numbers" }
func (t *CalculatorTool) RequiresApproval(map[string]any) bool { return false }
func (t *CalculatorTool) Execute(_ context.Context, p map[string]any) (*tool.Result, error) {
	a, _ := p["a"].(float64)
	b, _ := p["b"].(float64)
	return tool.NewResult("calculator", true, fmt.Sprintf("%.0f + %.0f = %.0f", a, b, a+b), ""), nil
}

// InputSchema provides the JSON schema for this tool's input.
func (t *CalculatorTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "number", "description": "First number to add"},
			"b": map[string]any{"type": "number", "description": "Second number to add"},
		},
		"required": []string{"a", "b"},
	}
}

func main() {
	// Support both Anthropic and OpenAI
	var llmClient llm.Client
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		llmClient = llm.NewOpenAIClient(apiKey, "gpt-5.2")
		fmt.Println("Using OpenAI (gpt-5.2)")
	} else if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		llmClient = llm.NewAnthropicClient(apiKey, "claude-sonnet-4-20250514")
		fmt.Println("Using Anthropic (claude-sonnet-4)")
	} else {
		fmt.Fprintln(os.Stderr, "Set OPENAI_API_KEY or ANTHROPIC_API_KEY")
		os.Exit(1)
	}

	// Create registry and register tool
	registry := tool.NewRegistry()
	registry.Register(&CalculatorTool{})

	// Create agent
	a := agent.New(agent.Config{
		Name:         "minimal",
		Registry:     registry,
		LLMClient:    llmClient,
		SystemPrompt: "You are a helpful assistant with a calculator tool.",
	})

	// Simple REPL
	fmt.Println("Minimal mux agent. Type 'quit' to exit.")
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "quit" {
			break
		}

		// Subscribe to events and print output
		events := a.Subscribe()
		go func() {
			for ev := range events {
				switch ev.Type {
				case orchestrator.EventText:
					fmt.Print(ev.Text)
				case orchestrator.EventToolCall:
					fmt.Printf("\n[Tool: %s]\n", ev.ToolName)
				case orchestrator.EventToolResult:
					fmt.Printf("[Result: %s]\n", ev.Result.Output)
				case orchestrator.EventError:
					fmt.Fprintf(os.Stderr, "\nError: %v\n", ev.Error)
				}
			}
		}()

		if err := a.Run(context.Background(), input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		fmt.Println()
	}
}

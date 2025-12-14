// ABOUTME: Demonstrates basic mux usage - creating a simple agent with
// ABOUTME: built-in tools and running a conversation.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

// EchoTool is a simple built-in tool.
type EchoTool struct{}

func (t *EchoTool) Name() string                                { return "echo" }
func (t *EchoTool) Description() string                         { return "Echoes input" }
func (t *EchoTool) RequiresApproval(params map[string]any) bool { return false }
func (t *EchoTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	msg, _ := params["message"].(string)
	return tool.NewResult("echo", true, "Echo: "+msg, ""), nil
}

// DemoClient is a placeholder LLM client.
type DemoClient struct{}

func (c *DemoClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Hello from mux!"}},
		StopReason: llm.StopReasonEndTurn,
	}, nil
}

func (c *DemoClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		ch <- llm.StreamEvent{Type: llm.EventContentDelta, Text: "Hello!"}
		ch <- llm.StreamEvent{Type: llm.EventMessageStop}
		close(ch)
	}()
	return ch, nil
}

func main() {
	registry := tool.NewRegistry()
	registry.Register(&EchoTool{})
	executor := tool.NewExecutor(registry)
	client := &DemoClient{}
	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	go func() {
		for event := range events {
			switch event.Type {
			case orchestrator.EventText:
				fmt.Print(event.Text)
			case orchestrator.EventComplete:
				fmt.Println("\n[Complete]")
			case orchestrator.EventError:
				fmt.Fprintf(os.Stderr, "Error: %v\n", event.Error)
			}
		}
	}()

	if err := orch.Run(context.Background(), "Hello"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

package llm_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/llm"
)

type mockClient struct{}

func (m *mockClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Hello"}},
		StopReason: llm.StopReasonEndTurn,
	}, nil
}

func (m *mockClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		ch <- llm.StreamEvent{Type: llm.EventContentDelta, Text: "Hello"}
		ch <- llm.StreamEvent{Type: llm.EventMessageStop}
		close(ch)
	}()
	return ch, nil
}

func TestClientInterface(t *testing.T) {
	var client llm.Client = &mockClient{}

	resp, err := client.CreateMessage(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.NewUserMessage("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(resp.Content))
	}
}

func TestMessageConstruction(t *testing.T) {
	msg := llm.NewUserMessage("Hello world")
	if msg.Role != llm.RoleUser {
		t.Errorf("expected role User, got %s", msg.Role)
	}

	assistantMsg := llm.NewAssistantMessage("Hi there")
	if assistantMsg.Role != llm.RoleAssistant {
		t.Errorf("expected role Assistant, got %s", assistantMsg.Role)
	}
}

func TestResponseHelpers(t *testing.T) {
	resp := &llm.Response{
		Content: []llm.ContentBlock{
			{Type: llm.ContentTypeText, Text: "Hello "},
			{Type: llm.ContentTypeText, Text: "World"},
			{Type: llm.ContentTypeToolUse, ID: "tool_1", Name: "test"},
		},
	}

	if !resp.HasToolUse() {
		t.Error("expected HasToolUse to return true")
	}

	uses := resp.ToolUses()
	if len(uses) != 1 {
		t.Errorf("expected 1 tool use, got %d", len(uses))
	}

	text := resp.TextContent()
	if text != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", text)
	}
}

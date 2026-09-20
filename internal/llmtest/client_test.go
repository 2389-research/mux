// ABOUTME: Tests the shared scripted llm.Client test double: driving a real
// ABOUTME: agent run, request recording, snapshot independence, and defaults.
package llmtest_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/internal/llmtest"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

// TestClientDrivesAgentRun exercises the fixture through a real consumer:
// an agent run should reach the scripted response and leave it in history.
func TestClientDrivesAgentRun(t *testing.T) {
	var sawRequest bool
	client := &llmtest.Client{
		Media: llm.FullCapabilities(),
		Respond: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			sawRequest = true
			if len(req.Messages) == 0 {
				t.Error("callback received a request with no messages")
			}
			return &llm.Response{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
				StopReason: llm.StopReasonEndTurn,
			}, nil
		},
	}

	a := agent.New(agent.Config{
		Name:      "shared-fake-agent",
		Registry:  tool.NewRegistry(),
		LLMClient: client,
	})

	if err := a.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawRequest {
		t.Fatal("scripted callback never saw a request")
	}

	history := a.Messages()
	last := history[len(history)-1]
	if got := blockText(last.Blocks); got != "ok" {
		t.Fatalf("final history text = %q, want %q", got, "ok")
	}

	requests := client.Requests()
	if len(requests) == 0 {
		t.Fatal("Requests() recorded nothing")
	}
	if len(requests[0].Messages) == 0 {
		t.Fatal("recorded request has no messages")
	}
}

// TestClientRequestsAreIndependentSnapshots proves the recorded requests do not
// alias later caller state: mutating a returned snapshot must not change the
// fixture's stored copy, and returning one must not mutate the caller's slice.
func TestClientRequestsAreIndependentSnapshots(t *testing.T) {
	client := &llmtest.Client{
		Respond: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			return &llm.Response{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
				StopReason: llm.StopReasonEndTurn,
			}, nil
		},
	}

	req := &llm.Request{Messages: []llm.Message{llm.NewUserMessage("first")}}
	if _, err := client.CreateMessage(context.Background(), req); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Mutating the caller's request after the call must not reach the record.
	req.Messages[0] = llm.NewUserMessage("mutated")

	first := client.Requests()
	if got := first[0].Messages[0].Content; got != "first" {
		t.Fatalf("recorded message content = %q, want %q (record aliases the caller's request)", got, "first")
	}

	// Mutating a returned snapshot must not reach the fixture's record.
	first[0].Messages[0] = llm.NewUserMessage("snapshot-mutation")
	second := client.Requests()
	if got := second[0].Messages[0].Content; got != "first" {
		t.Fatalf("recorded message content after snapshot mutation = %q, want %q", got, "first")
	}
}

// TestClientUnexpectedPathErrors confirms an unset callback surfaces a clear
// error instead of a nil-pointer panic or a silent zero response.
func TestClientUnexpectedPathErrors(t *testing.T) {
	client := &llmtest.Client{}

	if _, err := client.CreateMessage(context.Background(), &llm.Request{}); err == nil {
		t.Error("CreateMessage with nil Respond = nil error, want error")
	} else if !strings.Contains(err.Error(), "nonstream") {
		t.Errorf("CreateMessage error = %q, want it to name the nonstream path", err)
	}

	if _, err := client.CreateMessageStream(context.Background(), &llm.Request{}); err == nil {
		t.Error("CreateMessageStream with nil Stream = nil error, want error")
	} else if !strings.Contains(err.Error(), "stream") {
		t.Errorf("CreateMessageStream error = %q, want it to name the stream path", err)
	}
}

// TestClientStreamCallback wires the streaming path and checks the events reach
// the caller unchanged.
func TestClientStreamCallback(t *testing.T) {
	client := &llmtest.Client{
		Stream: func(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
			ch := make(chan llm.StreamEvent, 2)
			ch <- llm.StreamEvent{Type: llm.EventContentDelta, Text: "hi"}
			ch <- llm.StreamEvent{Type: llm.EventMessageStop}
			close(ch)
			return ch, nil
		},
	}

	stream, err := client.CreateMessageStream(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}
	first, ok := <-stream
	if !ok {
		t.Fatal("stream closed before delivering an event")
	}
	if first.Type != llm.EventContentDelta || first.Text != "hi" {
		t.Fatalf("first event = %+v, want content_block_delta %q", first, "hi")
	}
	if len(client.Requests()) != 1 {
		t.Fatalf("Requests() len = %d, want 1", len(client.Requests()))
	}
}

// TestClientCapabilities confirms Capabilities reports the configured Media value.
func TestClientCapabilities(t *testing.T) {
	client := &llmtest.Client{Media: llm.Capabilities{Image: true}}
	if got := client.Capabilities(); !got.Image || got.PDF || got.Audio || got.Video {
		t.Fatalf("Capabilities() = %+v, want only Image", got)
	}

	var _ llm.Client = (*llmtest.Client)(nil)
}

// TestClientPropagatesCallbackError confirms the fixture returns the callback's
// error untouched, so scripted failures behave like a real provider failure.
func TestClientPropagatesCallbackError(t *testing.T) {
	want := errors.New("provider boom")
	client := &llmtest.Client{
		Respond: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			return nil, want
		},
	}
	if _, err := client.CreateMessage(context.Background(), &llm.Request{}); !errors.Is(err, want) {
		t.Fatalf("CreateMessage error = %v, want %v", err, want)
	}
}

func blockText(blocks []llm.ContentBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		b.WriteString(block.Text)
	}
	return b.String()
}

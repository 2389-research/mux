//go:build live

// ABOUTME: Exercises bounded streaming, reasoning replay, token limits and compaction with actual providers.
// ABOUTME: Sends only synthetic data and forwards every request to the configured live API.
package mux_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/mux/hooks"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/mcp"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

func liveStream(t *testing.T, ctx context.Context, client llm.Client, req *llm.Request) *llm.Response {
	t.Helper()
	events, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var result *llm.Response
	stops, deltas := 0, 0
	for event := range events {
		if event.Error != nil || event.Type == llm.EventError {
			t.Fatalf("live stream error: %v", event.Error)
		}
		if event.Type == llm.EventContentDelta {
			deltas++
		}
		if event.Type == llm.EventMessageStop {
			stops++
			result = event.Response
			if result != nil && len(event.FinalBlocks) != len(result.Content) {
				t.Fatal("terminal block mapping incomplete")
			}
		}
	}
	if result == nil || stops != 1 || deltas == 0 {
		t.Fatalf("stream result=%v stops=%d deltas=%d", result != nil, stops, deltas)
	}
	t.Logf("live stream model=%s stop=%s input=%d output=%d thinking=%d", req.Model, result.StopReason, result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.ThinkingTokens)
	return result
}

func TestLiveProviderStreaming(t *testing.T) {
	provider, model := requireLiveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client := newLiveClient(ctx, t, provider, model)
	log := filepath.Join(t.TempDir(), "echo.log")
	mcpClient, err := mcp.NewClient(mcp.ServerConfig{Name: "echo", Transport: "stdio", Command: buildEchoServer(ctx, t), Env: map[string]string{"MUX_ECHO_SERVER_LOG": log}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mcpClient.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatal(err)
	}
	infos, err := mcpClient.ListTools(ctx)
	if err != nil || len(infos) != 1 {
		t.Fatalf("discovery: %v", err)
	}
	nonce := randomNonce(t)
	req := &llm.Request{Model: model, MaxTokens: 2048, Tools: []llm.ToolDefinition{{Name: infos[0].Name, Description: infos[0].Description, InputSchema: infos[0].InputSchema}}, Messages: []llm.Message{llm.NewUserMessage(fmt.Sprintf("Call echo exactly once with message %q then return only its returned text.", nonce))}}
	if provider == "gemini" {
		req.Thinking = &llm.ThinkingConfig{Enabled: true, Budget: 1024}
	}
	first := liveStream(t, ctx, client, req)
	uses := first.ToolUses()
	if first.StopReason != llm.StopReasonToolUse || len(uses) != 1 || uses[0].Name != "echo" || uses[0].Input["message"] != nonce {
		t.Fatal("stream did not produce the requested exact structured tool input")
	}
	if provider == "gemini" {
		signature := false
		for _, b := range first.Content {
			if b.Replay != nil && strings.Contains(string(b.Replay.Data), "thoughtSignature") {
				signature = true
			}
		}
		if first.Usage.ThinkingTokens == 0 || !signature {
			t.Fatalf("reasoning evidence missing: thinking=%d signature=%v", first.Usage.ThinkingTokens, signature)
		}
	}
	result, err := mcpClient.CallTool(ctx, "echo", uses[0].Input)
	if err != nil || result.IsError || len(result.Content) != 1 || result.Content[0].Text != nonce {
		t.Fatal("real process tool result mismatch: ", err)
	}
	assertLiveEchoLog(t, log, nonce)
	req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant, Blocks: first.Content}, llm.NewUserMessageWithBlocks(llm.ContentBlock{Type: llm.ContentTypeToolResult, Name: "echo", ToolUseID: uses[0].ID, Text: nonce}))
	final := liveStream(t, ctx, client, req)
	if final.StopReason != llm.StopReasonEndTurn || !strings.Contains(final.TextContent(), nonce) || final.HasToolUse() {
		t.Fatal("replayed stream did not finish with real tool result")
	}
}

func assertLiveEchoLog(t *testing.T, path, nonce string) {
	t.Helper()
	// Shared process assertion is available under integration as well as live builds.
	data, err := os.ReadFile(path)
	if err != nil || string(data) != nonce+"\n" {
		t.Fatal("echo process log mismatch: ", err)
	}
}

func TestLiveProviderTokenLimit(t *testing.T) {
	provider, model := requireLiveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client := newLiveClient(ctx, t, provider, model)
	req := &llm.Request{Model: model, MaxTokens: 4, Messages: []llm.Message{llm.NewUserMessage("Write the word alpha 100 times, separated by spaces. Do not abbreviate.")}}
	direct, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if direct.StopReason != llm.StopReasonMaxTokens {
		t.Fatalf("direct stop=%s", direct.StopReason)
	}
	streamed := liveStream(t, ctx, client, req)
	if streamed.StopReason != llm.StopReasonMaxTokens {
		t.Fatalf("stream stop=%s", streamed.StopReason)
	}
	t.Logf("direct token-limit input=%d output=%d", direct.Usage.InputTokens, direct.Usage.OutputTokens)
}

// cappedLiveClient is a request budget guard; all accepted calls reach the actual provider.
type cappedLiveClient struct {
	llm.Client
	calls int
}

func (c *cappedLiveClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	c.calls++
	if c.calls > 3 {
		return nil, fmt.Errorf("live request budget exceeded")
	}
	bounded := *req
	bounded.MaxTokens = 512
	return c.Client.CreateMessage(ctx, &bounded)
}
func TestLiveProviderCompaction(t *testing.T) {
	provider, model := requireLiveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client := &cappedLiveClient{Client: newLiveClient(ctx, t, provider, model)}
	manager := hooks.NewManager()
	var events []hooks.CompactionEvent
	manager.OnCompaction(func(_ context.Context, e *hooks.CompactionEvent) error { events = append(events, *e); return nil })
	orch := orchestrator.NewWithConfig(client, tool.NewExecutor(tool.NewRegistry()), orchestrator.Config{Model: model, CompactionModel: model, ContextBudget: 2048, MaxIterations: 2, HookManager: manager, SystemPrompt: "Extract the public test marker from the supplied conversation. Return it verbatim."})
	nonce := "public-mux-test-" + randomNonce(t)
	orch.SetMessages([]llm.Message{llm.NewUserMessage("The public test marker for this run is: " + nonce), llm.NewAssistantMessage(strings.Repeat("Archive filler with no task information. ", 400))})
	if err := orch.Continue(ctx, "What is the public test marker for this run?"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].CompactedTokens >= events[0].OriginalTokens || !strings.Contains(events[0].Summary, nonce) {
		t.Fatal("compaction did not retain the code and reduce history")
	}
	history := orch.Messages()
	last := history[len(history)-1]
	response := llm.Response{Content: last.Blocks}
	if !strings.Contains(response.TextContent()+last.Content, nonce) {
		t.Fatalf("post-compaction response lost synthetic marker: %q", response.TextContent()+last.Content)
	}
	if client.calls != 2 {
		t.Fatalf("compaction made %d calls; want summary + answer", client.calls)
	}
	usage := orch.Usage()
	t.Logf("live compaction calls=%d input=%d output=%d original=%d compacted=%d", client.calls, usage.InputTokens, usage.OutputTokens, events[0].OriginalTokens, events[0].CompactedTokens)
}

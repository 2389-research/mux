// ABOUTME: Wire integration tests proving the accumulated Gemini stream
// ABOUTME: response drives orchestrator history and tool execution.
package orchestrator_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

type schemaMockTool struct {
	mockTool
}

func (m *schemaMockTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{"type": "string"},
		},
		"required": []string{"location"},
	}
}

// geminiStreamSequenceServer serves a different SSE payload sequence per
// request, so multi-turn orchestrator runs exercise real wire behavior.
type geminiStreamSequenceServer struct {
	server    *httptest.Server
	mu        sync.Mutex
	requests  int
	sequences [][]string
}

func newGeminiStreamSequenceServer(t *testing.T, sequences ...[]string) *geminiStreamSequenceServer {
	t.Helper()
	g := &geminiStreamSequenceServer{sequences: sequences}
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		seq := g.sequences[0]
		if g.requests > 0 && g.requests < len(g.sequences) {
			seq = g.sequences[g.requests]
		} else if g.requests >= len(g.sequences) {
			seq = g.sequences[len(g.sequences)-1]
		}
		g.requests++
		g.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, p := range seq {
			fmt.Fprintf(w, "data: %s\n\n", p)
		}
	}))
	t.Cleanup(g.server.Close)
	return g
}

// assistantText returns the concatenated text blocks of a message.
func assistantText(m llm.Message) string {
	var text string
	for _, b := range m.Blocks {
		if b.Type == llm.ContentTypeText {
			text += b.Text
		}
	}
	return text
}

// TestOrchestratorGeminiStreamAccumulatedResponseDrivesHistory runs one real
// orchestrator text turn against a two-chunk Gemini SSE stream (mux#ac2b):
// the final streamed response must carry every chunk's text and the usage
// trailer, because the orchestrator persists that response as history.
func TestOrchestratorGeminiStreamAccumulatedResponseDrivesHistory(t *testing.T) {
	s := newGeminiStreamSequenceServer(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello "}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
	})

	client, err := llm.NewGeminiClientWithBaseURL(context.Background(), "key", "gemini-test", s.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	executor := tool.NewExecutor(tool.NewRegistry())

	config := orchestrator.DefaultConfig()
	config.Stream = true
	orch := orchestrator.NewWithConfig(client, executor, config)

	if err := orch.Run(context.Background(), "Say hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	messages := orch.Messages()
	if len(messages) < 2 {
		t.Fatalf("expected user and assistant messages, got %d", len(messages))
	}
	assistant := messages[len(messages)-1]
	if assistant.Role != llm.RoleAssistant {
		t.Fatalf("last message role %q, want assistant", assistant.Role)
	}
	if assistantText(assistant) != "Hello world" {
		t.Errorf("assistant text %q, want %q", assistantText(assistant), "Hello world")
	}

	usage := orch.Usage()
	if usage.InputTokens != 5 || usage.OutputTokens != 2 {
		t.Errorf("usage = input:%d output:%d, want 5/2", usage.InputTokens, usage.OutputTokens)
	}
}

// TestOrchestratorGeminiStreamToolCallSurvivesUsageTrailer streams a function
// call in the first chunk followed by a usage-only trailer: the accumulated
// final response must still carry the tool call so the orchestrator executes
// it exactly once instead of treating the turn as empty.
func TestOrchestratorGeminiStreamToolCallSurvivesUsageTrailer(t *testing.T) {
	var calls int32
	s := newGeminiStreamSequenceServer(t,
		[]string{
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"location":"Boston"}}}]}}]}`,
			`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}`,
		},
		[]string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"sunny"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":1}}`,
		},
	)

	client, err := llm.NewGeminiClientWithBaseURL(context.Background(), "key", "gemini-test", s.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	registry.Register(&schemaMockTool{mockTool{
		name: "get_weather",
		execFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			atomic.AddInt32(&calls, 1)
			if params["location"] != "Boston" {
				t.Errorf("location = %v, want Boston", params["location"])
			}
			return tool.NewResult("get_weather", true, "sunny", ""), nil
		},
	}})
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.Stream = true
	config.MaxIterations = 3
	orch := orchestrator.NewWithConfig(client, executor, config)

	if err := orch.Run(context.Background(), "weather?"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("tool calls = %d, want 1", got)
	}

	messages := orch.Messages()
	if len(messages) == 0 {
		t.Fatal("no messages")
	}
	last := messages[len(messages)-1]
	if last.Role != llm.RoleAssistant || assistantText(last) != "sunny" {
		t.Errorf("last message = %+v, want assistant %q", last, "sunny")
	}
}

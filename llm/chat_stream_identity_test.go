// ABOUTME: Adapter-level regression coverage for Chat Completions stream fragments.
// ABOUTME: Drives Ollama and OpenRouter through their real SDK transport over httptest SSE.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// chatStreamFixtureChunks is the exact interleaved wire sequence from the
// stream-chat plan, shared with TestChatStreamInterleaved so the adapter path
// is pinned to the same normalized output as the accumulator unit test.
var chatStreamFixtureChunks = []string{
	`{"id":"r1","model":"fixture","choices":[{"index":0,"delta":{"role":"assistant","content":"東","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`,
	`{"id":"r1","model":"fixture","choices":[{"index":0,"delta":{"content":"京","tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"read","arguments":"{\"path\":\"b\"}"}},{"index":0,"function":{"arguments":"\"a\"}"}}]}}]}`,
	`{"id":"r1","model":"fixture","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
}

// chatSSEServer serves each payload as its own flushed SSE data frame, so the
// SDK observes them as separate reads rather than one batched body. A payload
// containing newlines would be split by callers that want to exercise a
// transport boundary inside one frame.
func chatSSEServer(t *testing.T, payloads ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		for _, p := range payloads {
			fmt.Fprintf(w, "data: %s\n\n", p)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// newChatFixtureClient builds one of the two Chat Completions adapters against
// a fixture server, so both are exercised through the same assertions.
func newChatFixtureClient(provider, serverURL string) Client {
	client := openai.NewClient(
		option.WithAPIKey("fixture"),
		option.WithBaseURL(serverURL),
		option.WithMaxRetries(0),
	)
	switch provider {
	case "ollama":
		return &OllamaClient{client: client, model: "fixture"}
	default:
		return &OpenRouterClient{client: client, model: "fixture"}
	}
}

// chatStreamTuples collects the normalized typed output of a real stream:
// text and tool-input delta tuples plus the terminal response and refs.
type chatStreamTuples struct {
	texts  []string
	args   []string
	kinds  []StreamDeltaKind
	blocks []string
	final  *Response
	refs   []StreamBlockRef
}

func collectChatStream(t *testing.T, ch <-chan StreamEvent) chatStreamTuples {
	t.Helper()
	var got chatStreamTuples
	for event := range ch {
		switch event.Type {
		case EventError:
			t.Fatalf("unexpected error event: %v", event.Error)
		case EventContentDelta:
			if event.BlockID == "" {
				t.Fatal("content delta carried no block identity")
			}
			got.kinds = append(got.kinds, event.DeltaKind)
			got.blocks = append(got.blocks, event.BlockID)
			if event.DeltaKind == StreamDeltaToolInput {
				got.args = append(got.args, event.BlockID+"="+event.Text)
			} else {
				got.texts = append(got.texts, event.BlockID+"="+event.Text)
			}
		case EventMessageStop:
			got.final = event.Response
			got.refs = event.FinalBlocks
		}
	}
	return got
}

// TestChatAdaptersStreamIdentity runs the interleaved fixture through both real
// adapters over SDK transport, without external credentials, and asserts the
// same exact tuples, final refs and reassembled calls as the unit test.
func TestChatAdaptersStreamIdentity(t *testing.T) {
	for _, provider := range []string{"ollama", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			server := chatSSEServer(t, chatStreamFixtureChunks...)
			defer server.Close()

			client := newChatFixtureClient(provider, server.URL)
			ch, err := client.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
			if err != nil {
				t.Fatalf("CreateMessageStream: %v", err)
			}
			got := collectChatStream(t, ch)

			wantTexts := []string{"chat:0:text=東", "chat:0:text=京"}
			if !reflect.DeepEqual(got.texts, wantTexts) {
				t.Fatalf("text deltas = %q, want %q", got.texts, wantTexts)
			}
			wantArgs := []string{`chat:0:tool:0={"path":`, `chat:0:tool:1={"path":"b"}`, `chat:0:tool:0="a"}`}
			if !reflect.DeepEqual(got.args, wantArgs) {
				t.Fatalf("tool-input deltas = %q, want %q", got.args, wantArgs)
			}
			wantKinds := []StreamDeltaKind{
				StreamDeltaText, StreamDeltaToolInput,
				StreamDeltaText, StreamDeltaToolInput, StreamDeltaToolInput,
			}
			if !reflect.DeepEqual(got.kinds, wantKinds) {
				t.Fatalf("delta kinds = %v, want %v", got.kinds, wantKinds)
			}

			final := got.final
			if final == nil {
				t.Fatal("stream ended without a terminal response")
			}
			if final.TextContent() != "東京" {
				t.Errorf("final text = %q", final.TextContent())
			}
			if final.StopReason != StopReasonToolUse {
				t.Errorf("stop reason = %q", final.StopReason)
			}
			uses := final.ToolUses()
			if len(uses) != 2 || uses[0].ID != "c1" || uses[1].ID != "c2" {
				t.Fatalf("tool uses = %+v", uses)
			}
			if uses[0].Input["path"] != "a" || uses[1].Input["path"] != "b" {
				t.Fatalf("reassembled inputs = %+v", uses)
			}
			wantRefs := []StreamBlockRef{
				{BlockID: "chat:0:text", ContentIndex: 0},
				{BlockID: "chat:0:tool:0", ContentIndex: 1},
				{BlockID: "chat:0:tool:1", ContentIndex: 2},
			}
			if !reflect.DeepEqual(got.refs, wantRefs) {
				t.Fatalf("final refs = %+v, want %+v", got.refs, wantRefs)
			}
			if final.Usage.InputTokens != 3 || final.Usage.OutputTokens != 5 {
				t.Errorf("usage = %+v", final.Usage)
			}
		})
	}
}

// TestChatAdaptersStreamStopBlocksAreIndependent pins the adapter path for the
// same property: a consumer that mutates a tool argument on the block delivered
// by a stop event must not rewrite the terminal Response of the same stream.
// Both clients are exercised over the real SDK transport.
func TestChatAdaptersStreamStopBlocksAreIndependent(t *testing.T) {
	for _, provider := range []string{"ollama", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			server := chatSSEServer(t, chatStreamFixtureChunks...)
			defer server.Close()

			client := newChatFixtureClient(provider, server.URL)
			ch, err := client.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
			if err != nil {
				t.Fatalf("CreateMessageStream: %v", err)
			}

			var final *Response
			var stops int
			for event := range ch {
				switch event.Type {
				case EventError:
					t.Fatalf("unexpected error event: %v", event.Error)
				case EventContentStop:
					if event.Block != nil && event.Block.Type == ContentTypeToolUse {
						stops++
						event.Block.Input["path"] = "REDACTED-BY-CONSUMER"
					}
				case EventMessageStop:
					final = event.Response
				}
			}
			if stops != 2 {
				t.Fatalf("delivered %d tool stop blocks, want 2", stops)
			}
			if final == nil {
				t.Fatal("stream ended without a terminal response")
			}
			uses := final.ToolUses()
			if len(uses) != 2 || uses[0].Input["path"] != "a" || uses[1].Input["path"] != "b" {
				t.Fatalf("final response was rewritten through a delivered stop block: %+v", uses)
			}
		})
	}
}

// TestChatAdaptersStreamUnicodeSplitWrites pins that multi-byte text survives a
// transport boundary inside the SDK's own reader: each fragment arrives as its
// own flushed frame, and the continuation still merges into one block with one
// stable identity.
func TestChatAdaptersStreamUnicodeSplitWrites(t *testing.T) {
	for _, provider := range []string{"ollama", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			server := chatSSEServer(t,
				`{"id":"r1","choices":[{"index":0,"delta":{"content":"東"}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{"content":"京"}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{"content":"タワー"}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			)
			defer server.Close()

			client := newChatFixtureClient(provider, server.URL)
			ch, err := client.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
			if err != nil {
				t.Fatalf("CreateMessageStream: %v", err)
			}
			got := collectChatStream(t, ch)

			if text := got.final.TextContent(); text != "東京タワー" {
				t.Fatalf("final text = %q", text)
			}
			blocks := map[string]bool{}
			for _, id := range got.blocks {
				blocks[id] = true
			}
			if len(blocks) != 1 || !blocks["chat:0:text"] {
				t.Fatalf("Unicode continuation used %v, want one chat:0:text block", blocks)
			}
			if len(got.final.Content) != 1 || len(got.refs) != 1 {
				t.Fatalf("content = %+v refs = %+v, want one merged block", got.final.Content, got.refs)
			}
		})
	}
}

// sseChatToolFragments builds n chat-completions frames that each append one
// argument fragment to native tool index 0. The producer's own message-start
// send, the one block-start event the first fragment emits, and one delta per
// fragment come to n+2 events; callers pick n so that total exactly fills the
// 100-slot event channel.
func sseChatToolFragments(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "data: {\"id\":\"r1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"x\"}}]}}]}\n\n"
	}
	return s
}

// TestChatAdaptersStreamCancelledUnreadChannel is the plan's cancellation
// regression for the new fragment path: a consumer that stops draining must
// still see the producer unwind and release the HTTP stream, with no sleeps
// deciding the outcome. 98 argument fragments plus the producer's message
// start and the first fragment's block start fill the 100-slot buffer exactly,
// so the next fragment's send blocks on the full channel until cancellation
// releases it.
func TestChatAdaptersStreamCancelledUnreadChannel(t *testing.T) {
	for _, provider := range []string{"ollama", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			server := httptest.NewServer(errorPathHandler(sseChatToolFragments(98), ""))
			defer server.Close()

			obs := newObservedClient()
			client := newErrorPathClient(provider, server.URL, obs)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch, err := client.CreateMessageStream(ctx, &Request{Messages: []Message{NewUserMessage("hi")}})
			if err != nil {
				t.Fatalf("CreateMessageStream: %v", err)
			}
			waitForFullBuffer(t, ch)
			cancel()
			assertProducerTerminated(t, obs.observed())
		})
	}
}

// TestChatNonstreamingMalformedToolInput pins that a non-streaming response
// whose tool-call arguments are not one valid JSON object fails instead of
// silently dropping the call: the aggregate converter's drop is only safe once
// this path has refused the response.
func TestChatNonstreamingMalformedToolInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-1",
			"model": "fixture",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":       "c1",
						"type":     "function",
						"function": map[string]any{"name": "read", "arguments": `{"path": "/tmp/x`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer server.Close()

	for _, provider := range []string{"ollama", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			client := newChatFixtureClient(provider, server.URL)
			resp, err := client.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
			if err == nil {
				t.Fatalf("expected protocol failure, got response %+v", resp)
			}
			if resp != nil {
				t.Fatalf("failure returned a response: %+v", resp)
			}
			var protocol *StreamProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("expected *StreamProtocolError, got %T: %v", err, err)
			}
			if protocol.Provider != provider {
				t.Errorf("provider = %q, want %q", protocol.Provider, provider)
			}
		})
	}
}

// TestChatNonstreamingMissingToolArguments pins the other side of the same
// boundary: a response whose tool call carries no argument document at all is
// refused rather than silently converted to an empty call. The Chat API omits
// the argument string when the model declares no arguments, but the SDK decodes
// that identically to a stream cut off before arguments arrived, so mux cannot
// invent the empty object that convertOpenAIResponse would otherwise drop.
func TestChatNonstreamingMissingToolArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-1",
			"model": "fixture",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":       "c1",
						"type":     "function",
						"function": map[string]any{"name": "read", "arguments": ""},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer server.Close()

	for _, provider := range []string{"ollama", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			client := newChatFixtureClient(provider, server.URL)
			resp, err := client.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
			if err == nil {
				t.Fatalf("expected protocol failure, got response %+v", resp)
			}
			var protocol *StreamProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("expected *StreamProtocolError, got %T: %v", err, err)
			}
		})
	}
}

// TestChatAdaptersStreamMalformedJSON pins the transport-level half of the
// malformed matrix: invalid JSON in a data frame is rejected by the SDK decoder
// before any chunk reaches the accumulator, and the producer must still report
// an error event rather than a successful terminal response.
func TestChatAdaptersStreamMalformedJSON(t *testing.T) {
	for _, provider := range []string{"ollama", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			server := chatSSEServer(t,
				`{"id":"r1","choices":[{"index":0,"delta":{"content":"東"}}]}`,
				`{`,
			)
			defer server.Close()

			client := newChatFixtureClient(provider, server.URL)
			ch, err := client.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
			if err != nil {
				t.Fatalf("CreateMessageStream: %v", err)
			}
			var gotError bool
			var final *Response
			for event := range ch {
				switch event.Type {
				case EventError:
					gotError = true
				case EventMessageStop:
					final = event.Response
				}
			}
			if !gotError {
				t.Error("expected an error event for malformed JSON")
			}
			if final != nil {
				t.Fatalf("malformed stream produced a terminal response: %+v", final)
			}
		})
	}
}

// ABOUTME: Pins that a tool-call input integer past 2^53 survives every
// ABOUTME: provider decode path and history round-trip without rounding.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// bigID is an integer one past float64's exact-integer range: decoding it
// through a plain interface{} and re-encoding rounds it down to ...000.
const bigID = "10000000000000001"

// assertExactBigID checks that a decoded tool input carried bigID exactly.
// A float64 decode is reported by its own type and value, so a regression
// names what actually happened rather than just "not equal".
func assertExactBigID(t *testing.T, label string, input map[string]any) {
	t.Helper()
	if input == nil {
		t.Fatalf("%s: input is nil", label)
	}
	got, ok := input["id"]
	if !ok {
		t.Fatalf("%s: input has no id: %#v", label, input)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal id: %v", label, err)
	}
	if string(encoded) != bigID {
		t.Fatalf("%s: id = %s (%T), want %s", label, encoded, got, bigID)
	}
}

func TestConvertResponse_AnthropicToolInputPreservesLargeInteger(t *testing.T) {
	msg := &anthropic.Message{
		ID:    "msg_big",
		Model: "claude-sonnet-4-20250514",
		Content: []anthropic.ContentBlockUnion{
			{
				Type:  "tool_use",
				ID:    "tool_big",
				Name:  "lookup",
				Input: json.RawMessage(`{"id":` + bigID + `}`),
			},
		},
		StopReason: "tool_use",
	}

	resp := convertResponse(msg, string(msg.Model))
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(resp.Content))
	}
	assertExactBigID(t, "convertResponse", resp.Content[0].Input)
}

// TestAnthropicStreamAccumulator_ToolInputPreservesLargeInteger drives the
// real streamed-tool-input path: an input_json_delta accumulation past 2^53
// must reach the finished response block exactly.
func TestAnthropicStreamAccumulator_ToolInputPreservesLargeInteger(t *testing.T) {
	acc := newAnthropicStreamAccumulator("claude-sonnet-4-20250514")
	if err := acc.startBlock(0, "tool_use", "tool_big", "lookup", "", "", "", "", false); err != nil {
		t.Fatalf("startBlock: %v", err)
	}
	if err := acc.appendDelta(0, "input_json_delta", `{"id":`+bigID+`}`); err != nil {
		t.Fatalf("appendDelta: %v", err)
	}
	if err := acc.stopBlock(0); err != nil {
		t.Fatalf("stopBlock: %v", err)
	}
	resp, _ := acc.finish()
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 block, got %d: %#v", len(resp.Content), resp.Content)
	}
	assertExactBigID(t, "anthropic stream accumulator", resp.Content[0].Input)
}

// TestConvertOpenAIResponsesResponse_ToolInputPreservesLargeInteger decodes a
// Responses API payload through the SDK, so the test exercises the same path a
// live response would.
func TestConvertOpenAIResponsesResponse_ToolInputPreservesLargeInteger(t *testing.T) {
	raw := fmt.Sprintf(`{"id":"resp_big","object":"response","created_at":0,"model":"gpt-5.5","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_big","name":"lookup","arguments":"{\"id\":%s}"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, bigID)

	var resp responses.Response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode responses payload: %v", err)
	}
	converted := convertOpenAIResponsesResponse(&resp, "gpt-5.5")
	if len(converted.Content) != 1 {
		t.Fatalf("expected 1 block, got %d: %#v", len(converted.Content), converted.Content)
	}
	assertExactBigID(t, "convertOpenAIResponsesResponse", converted.Content[0].Input)
}

// TestOpenAIClientCreateMessage_ToolInputPreservesLargeInteger is the
// end-to-end non-streaming check: the wire response carries the 17-digit
// integer and the converted block keeps it exactly.
func TestOpenAIClientCreateMessage_ToolInputPreservesLargeInteger(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"resp_big","object":"response","created_at":0,"model":"gpt-5.5","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_big","name":"lookup","arguments":"{\"id\":%s}"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, bigID)
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			openaioption.WithAPIKey("test-key"),
			openaioption.WithBaseURL(server.URL),
			openaioption.WithMaxRetries(0),
		),
		model: "gpt-5.5",
	}

	resp, err := client.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessage("look it up")},
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 block, got %d: %#v", len(resp.Content), resp.Content)
	}
	assertExactBigID(t, "openai responses wire", resp.Content[0].Input)
}

// TestConvertOpenAIResponse_ChatToolInputPreservesLargeInteger covers the
// Chat Completions converter, which Ollama and OpenRouter also ride.
func TestConvertOpenAIResponse_ChatToolInputPreservesLargeInteger(t *testing.T) {
	raw := fmt.Sprintf(`{"id":"chatcmpl_big","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_big","type":"function","function":{"name":"lookup","arguments":"{\"id\":%s}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, bigID)

	var chat openai.ChatCompletion
	if err := json.Unmarshal([]byte(raw), &chat); err != nil {
		t.Fatalf("decode chat completion: %v", err)
	}
	resp := convertOpenAIResponse(&chat)
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 block, got %d: %#v", len(resp.Content), resp.Content)
	}
	assertExactBigID(t, "convertOpenAIResponse chat", resp.Content[0].Input)
}

// TestOpenAIStreamingToolCallBlock_PreservesLargeInteger covers the Chat
// Completions streaming path shared with Ollama and OpenRouter.
func TestOpenAIStreamingToolCallBlock_PreservesLargeInteger(t *testing.T) {
	block := openAIStreamingToolCallBlock("item_1", struct {
		callID    string
		name      string
		arguments string
	}{callID: "call_big", name: "lookup", arguments: `{"id":` + bigID + `}`})
	assertExactBigID(t, "openAIStreamingToolCallBlock", block.Input)
}

// TestAnthropicToolInputRoundTripPreservesLargeInteger sends a history holding
// the large integer back out through convertRequest and asserts the outgoing
// wire body still carries the exact digits. This is the round-trip that
// invalidates an Anthropic thinking signature when it rounds.
func TestAnthropicToolInputRoundTripPreservesLargeInteger(t *testing.T) {
	var wire []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the raw body: decoding it into a map[string]any here would
		// itself round the number through float64 and hide the regression.
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		wire = raw
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_2","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	_, err := client.CreateMessage(context.Background(), &Request{
		Model: "claude-sonnet-4-20250514",
		Messages: []Message{
			NewUserMessage("look it up"),
			{Role: RoleAssistant, Blocks: []ContentBlock{
				{Type: ContentTypeToolUse, ID: "tool_big", Name: "lookup", Input: map[string]any{"id": json.Number(bigID)}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if !containsExactBigID(wire) {
		t.Fatalf("wire body lost the large integer: %s", wire)
	}
}

// containsExactBigID reports whether raw JSON holds bigID as a bare number,
// not as a rounded value and not inside a string.
func containsExactBigID(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return false
	}
	return walkForBigID(tree)
}

func walkForBigID(v any) bool {
	switch typed := v.(type) {
	case map[string]any:
		for _, value := range typed {
			if walkForBigID(value) {
				return true
			}
		}
	case []any:
		for _, value := range typed {
			if walkForBigID(value) {
				return true
			}
		}
	case json.Number:
		return typed.String() == bigID
	}
	return false
}

// ABOUTME: OpenAI Responses replay preservation: reasoning items, message
// phase, and function_call envelopes survive conversion and re-request
// byte-for-byte, in original output order.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// openAIReplayFixtureJSON is a real-shape Responses API body whose ordered
// output list mixes a reasoning item (opaque encrypted content), a commentary
// message, a function_call, and the final-answer message.
const openAIReplayFixtureJSON = `{
  "id": "resp_replay_1",
  "object": "response",
  "created_at": 1757635200,
  "status": "completed",
  "model": "gpt-5.2",
  "output": [
    {
      "type": "reasoning",
      "id": "rs1",
      "status": "completed",
      "summary": [],
      "encrypted_content": "gAAAAABvq0PzExampleOpaqueCiphertext"
    },
    {
      "type": "message",
      "id": "msg1",
      "role": "assistant",
      "status": "completed",
      "phase": "commentary",
      "content": [
        {"type": "output_text", "text": "Checking the test output first.", "annotations": []}
      ]
    },
    {
      "type": "function_call",
      "id": "fc1",
      "call_id": "c1",
      "name": "run_tests",
      "arguments": "{\"package\":\"./llm\"}",
      "status": "completed"
    },
    {
      "type": "message",
      "id": "msg2",
      "role": "assistant",
      "status": "completed",
      "phase": "final_answer",
      "content": [
        {"type": "output_text", "text": "All tests pass.", "annotations": []}
      ]
    }
  ],
  "usage": {
    "input_tokens": 10,
    "output_tokens": 20,
    "output_tokens_details": {"reasoning_tokens": 7},
    "total_tokens": 30
  }
}`

const openAIReplayEncryptedContent = "gAAAAABvq0PzExampleOpaqueCiphertext"

func openAIReplayFixtureResponse(t *testing.T) *responses.Response {
	t.Helper()
	var resp responses.Response
	if err := json.Unmarshal([]byte(openAIReplayFixtureJSON), &resp); err != nil {
		t.Fatalf("unmarshal fixture response: %v", err)
	}
	return &resp
}

func openAIReplayFixtureItems(t *testing.T) []json.RawMessage {
	t.Helper()
	var doc struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal([]byte(openAIReplayFixtureJSON), &doc); err != nil {
		t.Fatalf("extract fixture output items: %v", err)
	}
	return doc.Output
}

func decodeReplayItem(t *testing.T, data json.RawMessage) map[string]any {
	t.Helper()
	var item map[string]any
	if err := json.Unmarshal(data, &item); err != nil {
		t.Fatalf("decode replay data %s: %v", data, err)
	}
	return item
}

func TestConvertOpenAIResponsesResponse_OpenAIReplay(t *testing.T) {
	result := convertOpenAIResponsesResponse(openAIReplayFixtureResponse(t), "gpt-5.2")

	if len(result.Content) != 4 {
		t.Fatalf("expected one mux block per output item (4), got %d: %#v", len(result.Content), result.Content)
	}

	// Item 1 (rs1): reasoning becomes a replay-only block; the encrypted
	// content never appears in normalized form.
	reasoning := result.Content[0]
	if reasoning.Type != ContentTypeReplay {
		t.Errorf("block 0 type: got %q want %q", reasoning.Type, ContentTypeReplay)
	}
	if reasoning.Replay == nil {
		t.Fatal("block 0 must carry a Replay envelope")
	}
	if reasoning.Replay.Provider != "openai" || reasoning.Replay.Model != "gpt-5.2" {
		t.Errorf("block 0 replay identity: got %s/%s", reasoning.Replay.Provider, reasoning.Replay.Model)
	}
	rs1 := decodeReplayItem(t, reasoning.Replay.Data)
	if rs1["type"] != "reasoning" || rs1["id"] != "rs1" {
		t.Errorf("block 0 replay item: got %v", rs1)
	}
	if rs1["encrypted_content"] != openAIReplayEncryptedContent {
		t.Errorf("block 0 encrypted_content: got %v", rs1["encrypted_content"])
	}

	// Item 2 (msg1): output_text joins into one text block; phase stays raw.
	commentary := result.Content[1]
	if commentary.Type != ContentTypeText {
		t.Errorf("block 1 type: got %q want %q", commentary.Type, ContentTypeText)
	}
	if commentary.Text != "Checking the test output first." {
		t.Errorf("block 1 text: got %q", commentary.Text)
	}
	if commentary.Replay == nil {
		t.Fatal("block 1 must carry a Replay envelope")
	}
	msg1 := decodeReplayItem(t, commentary.Replay.Data)
	if msg1["id"] != "msg1" || msg1["phase"] != "commentary" {
		t.Errorf("block 1 replay item id/phase: got %v", msg1)
	}

	// Item 3 (fc1): normalized tool use keyed by call_id; item id stays raw.
	toolUse := result.Content[2]
	if toolUse.Type != ContentTypeToolUse {
		t.Errorf("block 2 type: got %q want %q", toolUse.Type, ContentTypeToolUse)
	}
	if toolUse.ID != "c1" {
		t.Errorf("block 2 tool ID: got %q want call_id c1", toolUse.ID)
	}
	if toolUse.Name != "run_tests" {
		t.Errorf("block 2 tool name: got %q", toolUse.Name)
	}
	if toolUse.Input["package"] != "./llm" {
		t.Errorf("block 2 tool input: got %v", toolUse.Input)
	}
	if toolUse.Replay == nil {
		t.Fatal("block 2 must carry a Replay envelope")
	}
	fc1 := decodeReplayItem(t, toolUse.Replay.Data)
	if fc1["id"] != "fc1" {
		t.Errorf("block 2 replay item must keep item id fc1 raw: got %v", fc1["id"])
	}

	// Item 4 (msg2): final answer phase preserved raw.
	final := result.Content[3]
	if final.Type != ContentTypeText || final.Text != "All tests pass." {
		t.Errorf("block 3: got type %q text %q", final.Type, final.Text)
	}
	msg2 := decodeReplayItem(t, final.Replay.Data)
	if msg2["id"] != "msg2" || msg2["phase"] != "final_answer" {
		t.Errorf("block 3 replay item id/phase: got %v", msg2)
	}

	// TextContent joins only output_text and never encrypted content.
	textBlocks := 0
	for _, block := range result.Content {
		if block.Type != ContentTypeText {
			continue
		}
		textBlocks++
		if strings.Contains(block.Text, openAIReplayEncryptedContent) {
			t.Errorf("text block leaks encrypted content: %q", block.Text)
		}
	}
	if textBlocks != 2 {
		t.Errorf("expected exactly 2 text blocks, got %d", textBlocks)
	}
}

// A message item with multiple output_text parts joins into a single text
// block; a message item with no output_text still yields a (text-empty)
// block so its replay envelope survives.
func TestConvertOpenAIResponsesResponse_OpenAIReplayTextJoining(t *testing.T) {
	fixture := `{
	  "id": "resp_join",
	  "status": "completed",
	  "model": "gpt-5.2",
	  "output": [
	    {
	      "type": "message",
	      "id": "msg_join",
	      "role": "assistant",
	      "status": "completed",
	      "content": [
	        {"type": "output_text", "text": "first part", "annotations": []},
	        {"type": "output_text", "text": "second part", "annotations": []}
	      ]
	    },
	    {
	      "type": "message",
	      "id": "msg_empty",
	      "role": "assistant",
	      "status": "completed",
	      "content": [
	        {"type": "refusal", "refusal": "no"}
	      ]
	    }
	  ]
	}`
	var resp responses.Response
	if err := json.Unmarshal([]byte(fixture), &resp); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	result := convertOpenAIResponsesResponse(&resp, "gpt-5.2")
	if len(result.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d: %#v", len(result.Content), result.Content)
	}
	if got := result.Content[0].Text; got != "first part\nsecond part" {
		t.Errorf("joined output_text: got %q", got)
	}
	if result.Content[0].Replay == nil {
		t.Error("joined text block must carry a Replay envelope")
	}
	if got := result.Content[1]; got.Type != ContentTypeText || got.Text != "" {
		t.Errorf("empty message block: got type %q text %q", got.Type, got.Text)
	}
	if result.Content[1].Replay == nil {
		t.Error("empty text block must still carry a Replay envelope")
	}
}

// Round-trip: replay blocks decode back into raw input items that match the
// original output items — same order, IDs, phase, and encrypted bytes — with
// no duplicate text or tool items alongside.
func TestConvertResponsesInput_OpenAIReplayRoundTrip(t *testing.T) {
	converted := convertOpenAIResponsesResponse(openAIReplayFixtureResponse(t), "gpt-5.2")
	msg := Message{Role: RoleAssistant, Blocks: converted.Content}

	items, err := convertResponsesInput([]Message{msg})
	if err != nil {
		t.Fatalf("convertResponsesInput: %v", err)
	}

	want := openAIReplayFixtureItems(t)
	if len(items) != len(want) {
		t.Fatalf("expected %d input items (one per replay block, no duplicates), got %d", len(want), len(items))
	}
	for i, item := range items {
		gotJSON, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal input item %d: %v", i, err)
		}
		var got, expected any
		if err := json.Unmarshal(gotJSON, &got); err != nil {
			t.Fatalf("decode marshaled item %d: %v", i, err)
		}
		if err := json.Unmarshal(want[i], &expected); err != nil {
			t.Fatalf("decode fixture item %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("item %d mismatch:\n got: %s\nwant: %s", i, gotJSON, want[i])
		}
	}
}

func TestConvertResponsesInput_OpenAIReplayMalformed(t *testing.T) {
	msg := Message{Role: RoleAssistant, Blocks: []ContentBlock{{
		Type:   ContentTypeReplay,
		Replay: &ProviderReplay{Provider: "openai", Model: "gpt-5.2", Data: json.RawMessage(`{"type":"reasoning",`)},
	}}}

	if _, err := convertResponsesInput([]Message{msg}); err == nil {
		t.Fatal("expected error for malformed replay data, got nil")
	}
}

// Provider/model switches reject with ErrReplayMismatch before any HTTP
// request is made.
func TestOpenAIClient_CreateMessageOpenAIReplayPreflight(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be reached for replay identity mismatch")
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
			option.WithMaxRetries(0),
		),
		model: "gpt-5.2",
	}
	_, err := client.CreateMessage(context.Background(), &Request{
		Messages: []Message{{
			Role: RoleAssistant,
			Blocks: []ContentBlock{{
				Type: ContentTypeReplay,
				Replay: &ProviderReplay{
					Provider: "openai",
					Model:    "gpt-5.1",
					Data:     json.RawMessage(`{"type":"reasoning","id":"rs1","encrypted_content":"opaque"}`),
				},
			}},
		}},
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var mismatch *ErrReplayMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected *ErrReplayMismatch, got %T: %v", err, err)
	}
	if mismatch.Provider != "openai" || mismatch.Model != "gpt-5.2" || mismatch.ReplayModel != "gpt-5.1" {
		t.Errorf("mismatch identities: %+v", mismatch)
	}
}

// End-to-end: the converted response's blocks feed the next CreateMessage,
// whose wire body retains original item order, IDs, phase and encrypted
// bytes, requests reasoning.encrypted_content, and adds no duplicate
// text/tool items.
func TestOpenAIClient_CreateMessageOpenAIReplaySecondRequest(t *testing.T) {
	requests := 0
	var secondBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			w.Write([]byte(openAIReplayFixtureJSON))
			return
		}
		secondBody = body
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "resp_second",
			"object":     "response",
			"created_at": 0,
			"model":      "gpt-5.2",
			"status":     "completed",
			"output": []map[string]any{
				{
					"type":   "message",
					"id":     "msg_second",
					"role":   "assistant",
					"status": "completed",
					"content": []map[string]any{
						{"type": "output_text", "text": "ok", "annotations": []any{}},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
			option.WithMaxRetries(0),
		),
		model: "gpt-5.2",
	}

	first, err := client.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessage("run the tests")},
	})
	if err != nil {
		t.Fatalf("first CreateMessage: %v", err)
	}
	if len(first.Content) != 4 {
		t.Fatalf("expected 4 blocks in first response, got %d", len(first.Content))
	}

	second, err := client.CreateMessage(context.Background(), &Request{
		Messages: []Message{
			NewUserMessage("run the tests"),
			{Role: RoleAssistant, Blocks: first.Content},
		},
	})
	if err != nil {
		t.Fatalf("second CreateMessage: %v", err)
	}
	if second.ID != "resp_second" {
		t.Errorf("second response ID: got %q", second.ID)
	}
	if requests != 2 {
		t.Fatalf("expected 2 HTTP requests, got %d", requests)
	}

	include, ok := secondBody["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Errorf("include: got %#v", secondBody["include"])
	}

	input, ok := secondBody["input"].([]any)
	if !ok {
		t.Fatalf("input missing in second request: %#v", secondBody)
	}
	if len(input) != 5 {
		t.Fatalf("expected 5 input items (1 user + 4 replayed, no duplicates), got %d: %#v", len(input), input)
	}

	// The user message leads; the assistant's replayed items follow in the
	// original output order.
	firstItem, ok := input[0].(map[string]any)
	if !ok || firstItem["role"] != "user" || firstItem["content"] != "run the tests" {
		t.Errorf("input item 0 must be the user message: %#v", input[0])
	}

	wantTypes := []string{"reasoning", "message", "function_call", "message"}
	wantIDs := []string{"rs1", "msg1", "fc1", "msg2"}
	wantPhases := []any{nil, "commentary", nil, "final_answer"}
	for i := 0; i < 4; i++ {
		item, ok := input[i+1].(map[string]any)
		if !ok {
			t.Fatalf("input item %d not an object: %#v", i+1, input[i+1])
		}
		if item["type"] != wantTypes[i] || item["id"] != wantIDs[i] {
			t.Errorf("input item %d: got type %v id %v", i+1, item["type"], item["id"])
		}
		if item["phase"] != wantPhases[i] {
			t.Errorf("input item %d phase: got %v want %q", i+1, item["phase"], wantPhases[i])
		}
	}
	enc, ok := input[1].(map[string]any)["encrypted_content"]
	if !ok || enc != openAIReplayEncryptedContent {
		t.Errorf("input item 1 encrypted_content: got %v", enc)
	}
	if callID := input[3].(map[string]any)["call_id"]; callID != "c1" {
		t.Errorf("input item 3 call_id: got %v", callID)
	}
	if _, dup := input[3].(map[string]any)["input"]; dup {
		t.Errorf("function_call item must stay raw (no normalized input field): %#v", input[3])
	}
}

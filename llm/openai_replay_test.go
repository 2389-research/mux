// ABOUTME: OpenAI Responses replay preservation: reasoning items, message
// phase, and function_call envelopes survive conversion, streaming, re-request
// after a tool turn, and JSON persistence byte-for-byte, in original output
// order.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Stream parity: an SSE response.completed carrying the replay fixture
// converts through the same converter with the effective requested model, so
// the streamed final Content byte-matches the non-stream conversion. The
// client default model here ("gpt-5.1") deliberately differs from the
// fixture's snapshot model ("gpt-5.2") to prove the envelope pins the
// requested model, not the returned one. Partial function-call argument
// deltas never emit blocks on their own; the single intermediate block at
// arguments.done carries no Replay envelope, and the completed conversion
// remains authoritative.
func TestOpenAIClient_CreateMessageStreamOpenAIReplayParity(t *testing.T) {
	// SSE data frames must be single-line; compact the fixture without
	// reordering keys so the decoded output items keep their exact wire bytes.
	var compactedFixture bytes.Buffer
	if err := json.Compact(&compactedFixture, []byte(openAIReplayFixtureJSON)); err != nil {
		t.Fatalf("compact fixture: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("expected /responses request, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		writeOpenAIResponseSSE(t, w, "response.created", `{"type":"response.created","response":{"id":"resp_replay_1","status":"in_progress","model":"gpt-5.2"}}`)
		flusher.Flush()

		writeOpenAIResponseSSE(t, w, "response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs1","status":"in_progress","summary":[]}}`)
		flusher.Flush()

		writeOpenAIResponseSSE(t, w, "response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg1","role":"assistant","status":"in_progress","content":[]}}`)
		flusher.Flush()
		writeOpenAIResponseSSE(t, w, "response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg1","output_index":1,"content_index":0,"delta":"Checking the test output first."}`)
		flusher.Flush()

		writeOpenAIResponseSSE(t, w, "response.output_item.added", `{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"run_tests","arguments":"","status":"in_progress"}}`)
		flusher.Flush()
		writeOpenAIResponseSSE(t, w, "response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc1","output_index":2,"delta":"{\"package\":"}`)
		flusher.Flush()
		writeOpenAIResponseSSE(t, w, "response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","item_id":"fc1","output_index":2,"arguments":"{\"package\":\"./llm\"}"}`)
		flusher.Flush()

		writeOpenAIResponseSSE(t, w, "response.completed", fmt.Sprintf(`{"type":"response.completed","response":%s}`, compactedFixture.String()))
		flusher.Flush()
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
			option.WithMaxRetries(0),
		),
		model: "gpt-5.1",
	}

	eventChan, err := client.CreateMessageStream(context.Background(), &Request{
		Messages: []Message{NewUserMessage("run the tests")},
	})
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	var gotMessageStart bool
	var deltaText strings.Builder
	var intermediateBlocks []*ContentBlock
	var final *Response
	for event := range eventChan {
		switch event.Type {
		case EventMessageStart:
			gotMessageStart = true
		case EventContentDelta:
			deltaText.WriteString(event.Text)
		case EventContentStop:
			intermediateBlocks = append(intermediateBlocks, event.Block)
		case EventMessageStop:
			final = event.Response
		case EventError:
			t.Fatalf("unexpected error event: %v", event.Error)
		}
	}

	if !gotMessageStart {
		t.Error("expected MessageStart event")
	}
	if got := deltaText.String(); got != "Checking the test output first." {
		t.Errorf("streamed text deltas: got %q", got)
	}

	// The partial argument deltas emitted no block; the only intermediate
	// block arrives at arguments.done and carries no Replay envelope.
	if len(intermediateBlocks) != 1 {
		t.Fatalf("expected exactly 1 intermediate block (at arguments.done), got %d", len(intermediateBlocks))
	}
	intermediate := intermediateBlocks[0]
	if intermediate == nil {
		t.Fatal("intermediate block is nil")
	}
	if intermediate.Replay != nil {
		t.Errorf("intermediate block must not carry a Replay envelope: %#v", intermediate.Replay)
	}
	if intermediate.Type != ContentTypeToolUse || intermediate.ID != "c1" || intermediate.Name != "run_tests" {
		t.Errorf("intermediate tool block: got type %q id %q name %q", intermediate.Type, intermediate.ID, intermediate.Name)
	}
	if intermediate.Input["package"] != "./llm" {
		t.Errorf("intermediate tool input: got %v", intermediate.Input)
	}

	// The completed conversion is authoritative and byte-matches the
	// non-stream conversion of the same wire bytes with the same requested
	// model. Decoding want from the compacted payload both paths saw keeps
	// this a true byte-for-byte comparison.
	if final == nil {
		t.Fatal("expected MessageStop with final response")
	}
	var wantResp responses.Response
	if err := json.Unmarshal(compactedFixture.Bytes(), &wantResp); err != nil {
		t.Fatalf("unmarshal compacted fixture: %v", err)
	}
	want := convertOpenAIResponsesResponse(&wantResp, "gpt-5.1")
	if !reflect.DeepEqual(final.Content, want.Content) {
		t.Errorf("streamed final Content does not match non-stream conversion:\n got: %#v\nwant: %#v", final.Content, want.Content)
	}
	if final.ID != want.ID || final.Model != want.Model || final.StopReason != want.StopReason || final.Usage != want.Usage {
		t.Errorf("streamed final metadata: got id %q model %q stop %q usage %+v, want id %q model %q stop %q usage %+v",
			final.ID, final.Model, final.StopReason, final.Usage, want.ID, want.Model, want.StopReason, want.Usage)
	}

	// Every block's envelope pins the effective requested model (client
	// default), not the fixture snapshot model, with bytes intact.
	for i, block := range final.Content {
		if block.Replay == nil {
			t.Fatalf("final block %d must carry a Replay envelope", i)
		}
		if block.Replay.Provider != "openai" || block.Replay.Model != "gpt-5.1" {
			t.Errorf("final block %d replay identity: got %s/%s, want openai/gpt-5.1", i, block.Replay.Provider, block.Replay.Model)
		}
		if !bytes.Equal(block.Replay.Data, want.Content[i].Replay.Data) {
			t.Errorf("final block %d replay bytes differ:\n got: %s\nwant: %s", i, block.Replay.Data, want.Content[i].Replay.Data)
		}
	}
	rs1 := decodeReplayItem(t, final.Content[0].Replay.Data)
	if rs1["encrypted_content"] != openAIReplayEncryptedContent {
		t.Errorf("final reasoning encrypted_content: got %v", rs1["encrypted_content"])
	}
	msg1 := decodeReplayItem(t, final.Content[1].Replay.Data)
	if msg1["phase"] != "commentary" {
		t.Errorf("final message phase: got %v", msg1["phase"])
	}
	fc1 := decodeReplayItem(t, final.Content[2].Replay.Data)
	if fc1["id"] != "fc1" || fc1["call_id"] != "c1" {
		t.Errorf("final function_call raw item: got %v", fc1)
	}
}

// Tool turn plus persistence: the consumer append pattern (assistant message
// whose Blocks are the response Content, then a user message with the matching
// tool result) survives a JSON marshal/unmarshal of the whole conversation —
// the session/transcript save/load shape — and the next request receives the
// original reasoning/message/function_call items in exact order with IDs,
// phase, and encrypted bytes intact, followed by the function_call_output,
// with no duplicate text or tool items.
func TestOpenAIClient_OpenAIReplaySurvivesToolTurnAndJSONPersistence(t *testing.T) {
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

	// Consumer append pattern: response Content becomes the assistant
	// message's blocks; the tool result answers the function_call by call_id.
	messages := []Message{
		NewUserMessage("run the tests"),
		{Role: RoleAssistant, Blocks: first.Content},
		{Role: RoleUser, Blocks: []ContentBlock{{
			Type:      ContentTypeToolResult,
			ToolUseID: "c1",
			Name:      "run_tests",
			Text:      "ok: all tests pass",
		}}},
	}

	// Persistence: whole-conversation JSON save/load.
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	var loaded []Message
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("unmarshal conversation: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages after load, got %d", len(loaded))
	}

	// Replay data survives save/load semantically on every block: encoding/json
	// compacts RawMessage whitespace during marshal, so compare decoded value,
	// not wire formatting. The exact encrypted bytes then re-appear verbatim
	// on the second wire request, asserted in the handler capture below.
	for i, block := range loaded[1].Blocks {
		if block.Replay == nil {
			t.Fatalf("loaded assistant block %d lost its Replay envelope", i)
		}
		if !json.Valid(block.Replay.Data) {
			t.Errorf("loaded assistant block %d replay data is not valid JSON: %s", i, block.Replay.Data)
		}
		got := decodeReplayItem(t, block.Replay.Data)
		want := decodeReplayItem(t, messages[1].Blocks[i].Replay.Data)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("loaded assistant block %d replay item changed:\n got: %v\nwant: %v", i, got, want)
		}
	}
	if loaded[1].Blocks[2].Type != ContentTypeToolUse || loaded[1].Blocks[2].ID != "c1" {
		t.Errorf("loaded tool use block: got type %q id %q", loaded[1].Blocks[2].Type, loaded[1].Blocks[2].ID)
	}

	second, err := client.CreateMessage(context.Background(), &Request{Messages: loaded})
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
	// 1 user message + 4 replayed items + 1 function_call_output, no
	// duplicate text or tool items.
	if len(input) != 6 {
		t.Fatalf("expected 6 input items, got %d: %#v", len(input), input)
	}

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
			t.Errorf("input item %d: got type %v id %v, want %s/%s", i+1, item["type"], item["id"], wantTypes[i], wantIDs[i])
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

	// The tool result follows the replayed assistant items.
	toolOutput, ok := input[5].(map[string]any)
	if !ok || toolOutput["type"] != "function_call_output" {
		t.Fatalf("input item 5 must be the function_call_output: %#v", input[5])
	}
	if toolOutput["call_id"] != "c1" || toolOutput["output"] != "ok: all tests pass" {
		t.Errorf("function_call_output: got %#v", toolOutput)
	}

	// No duplicates: exactly one reasoning, two messages, one function_call,
	// one function_call_output (the leading user message has no type field).
	typeCounts := map[string]int{}
	for _, item := range input {
		if m, ok := item.(map[string]any); ok {
			if typ, ok := m["type"].(string); ok {
				typeCounts[typ]++
			}
		}
	}
	wantCounts := map[string]int{"reasoning": 1, "message": 2, "function_call": 1, "function_call_output": 1}
	if !reflect.DeepEqual(typeCounts, wantCounts) {
		t.Errorf("input item type counts: got %v, want %v", typeCounts, wantCounts)
	}
}

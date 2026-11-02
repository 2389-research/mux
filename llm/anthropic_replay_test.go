// ABOUTME: Tests that Anthropic signed thinking and redacted_thinking blocks
// ABOUTME: survive conversion, history JSON and streaming, and replay unmodified.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// sigThinkingStreamEvents is a signed-thinking tool turn as the API streams
// it: a thinking block whose signature arrives in two signature_delta
// fragments, a redacted_thinking block, then the tool call.
var sigThinkingStreamEvents = []string{
	`{"type":"message_start","message":{"id":"msg_sig","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
	`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opa"}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"que"}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"encrypted"}}`,
	`{"type":"content_block_stop","index":1}`,
	`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"c1","name":"read"}}`,
	`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"p\":\"a\"}"}}`,
	`{"type":"content_block_stop","index":2}`,
	`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":12}}`,
	`{"type":"message_stop"}`,
}

// sigWriteSSE writes each event as an SSE frame, flushing between frames.
func sigWriteSSE(t *testing.T, w http.ResponseWriter, events []string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	for _, event := range events {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(event), &envelope); err != nil {
			http.Error(w, "bad fixture", http.StatusInternalServerError)
			return
		}
		if _, err := w.Write([]byte("event: " + envelope.Type + "\ndata: " + event + "\n\n")); err != nil {
			return
		}
		flusher.Flush()
	}
}

// sigAnthropicClient points an AnthropicClient at a test server.
func sigAnthropicClient(baseURL, model string) *AnthropicClient {
	return &AnthropicClient{
		client: anthropic.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(baseURL)),
		model:  model,
	}
}

// sigAnthropicMessage decodes a raw Anthropic message body through the SDK so
// every content block carries the unmodified JSON the API sent.
func sigAnthropicMessage(t *testing.T, raw string) *anthropic.Message {
	t.Helper()
	var message anthropic.Message
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		t.Fatalf("decode fixture message: %v", err)
	}
	return &message
}

// sigConvertRequest converts a request and fails the test on error, so call
// sites stay a single expression.
func sigConvertRequest(t *testing.T, req *Request) anthropic.MessageNewParams {
	t.Helper()
	params, err := convertRequest(req)
	if err != nil {
		t.Fatalf("convertRequest: %v", err)
	}
	return params
}

func TestAnthropicThinkingReplay_SignedAndRedactedSurviveHistory(t *testing.T) {
	message := sigAnthropicMessage(t, `{"id":"m1","type":"message","role":"assistant","model":"model","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"reason","signature":"opaque"},{"type":"redacted_thinking","data":"encrypted"},{"type":"tool_use","id":"c1","name":"read","input":{}}],"usage":{"input_tokens":1,"output_tokens":2}}`)

	response := convertResponse(message, "model")

	if response.TextContent() != "" {
		t.Errorf("thinking and redacted data must stay out of display text, got %q", response.TextContent())
	}

	history := Message{Role: RoleAssistant, Blocks: response.Content}
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	var restored Message
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}

	params, err := convertRequest(&Request{Model: "model", Messages: []Message{restored}})
	if err != nil {
		t.Fatalf("convertRequest: %v", err)
	}
	wire, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wire, []byte(`"signature":"opaque"`)) {
		t.Errorf("replayed request lost the thinking signature: %s", wire)
	}
	if !bytes.Contains(wire, []byte(`"data":"encrypted"`)) {
		t.Errorf("replayed request lost the redacted thinking data: %s", wire)
	}
}

func TestAnthropicThinkingReplay_StreamedSignatureAndRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sigWriteSSE(t, w, sigThinkingStreamEvents)
	}))
	defer server.Close()

	client := sigAnthropicClient(server.URL, "claude-sonnet-4-20250514")
	events, err := client.CreateMessageStream(context.Background(), &Request{
		Messages: []Message{NewUserMessage("read a file")},
		Thinking: &ThinkingConfig{Enabled: true, Budget: 2048},
	})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}

	var final *Response
	for event := range events {
		if event.Type == EventError {
			t.Fatalf("unexpected error event: %v", event.Error)
		}
		if event.Type == EventContentDelta && strings.Contains(event.Text, "opa") {
			t.Errorf("signature fragment leaked into a displayed delta: %q", event.Text)
		}
		if event.Type == EventMessageStop {
			final = event.Response
		}
	}
	if final == nil {
		t.Fatal("stream produced no final response")
	}
	if len(final.Content) != 3 {
		t.Fatalf("expected thinking, redacted and tool blocks, got %+v", final.Content)
	}

	if final.Content[0].Type != ContentTypeThinking || final.Content[0].Thinking != "reason" {
		t.Errorf("first block: got %+v", final.Content[0])
	}
	if final.Content[0].Replay == nil {
		t.Fatal("streamed thinking block carries no replay envelope")
	}
	wantThinking := `{"type":"thinking","thinking":"reason","signature":"opaque"}`
	if string(final.Content[0].Replay.Data) != wantThinking {
		t.Errorf("thinking replay data: got %s, want %s", final.Content[0].Replay.Data, wantThinking)
	}
	if final.Content[0].Replay.Model != "claude-sonnet-4-20250514" {
		t.Errorf("replay must pin the effective requested model, got %q", final.Content[0].Replay.Model)
	}

	if final.Content[1].Type != ContentTypeReplay || final.Content[1].Text != "" {
		t.Errorf("redacted block must carry no display text: %+v", final.Content[1])
	}
	if final.Content[1].Replay == nil {
		t.Fatal("streamed redacted_thinking block carries no replay envelope")
	}
	wantRedacted := `{"type":"redacted_thinking","data":"encrypted"}`
	if string(final.Content[1].Replay.Data) != wantRedacted {
		t.Errorf("redacted replay data: got %s, want %s", final.Content[1].Replay.Data, wantRedacted)
	}

	if final.Content[2].Type != ContentTypeToolUse || final.Content[2].ID != "c1" {
		t.Errorf("third block: got %+v", final.Content[2])
	}
	if final.TextContent() != "" {
		t.Errorf("thinking must stay out of display text, got %q", final.TextContent())
	}

	// The accumulated blocks must replay onto the wire unchanged.
	params := sigConvertRequest(t, &Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []Message{{Role: RoleAssistant, Blocks: final.Content}},
	})
	wire, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wire, []byte(`"signature":"opaque"`)) || !bytes.Contains(wire, []byte(`"data":"encrypted"`)) {
		t.Errorf("streamed thinking did not replay intact: %s", wire)
	}
}

// sigSignedThinkingBody is a signed-thinking tool turn exactly as the API
// returns it. The signature and encrypted data are opaque: the test asserts
// they come back byte-for-byte, so they must never be edited to read nicer.
const sigSignedThinkingBody = `{"id":"msg_two","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"check the file first","signature":"ErUBCkYIBBgCIkDSig/+8vQ=="},{"type":"redacted_thinking","data":"EroBCoYBCAEQABgCKkBz3w=="},{"type":"tool_use","id":"toolu_sig","name":"read_file","input":{"path":"/tmp/x"}}],"usage":{"input_tokens":12,"output_tokens":34}}`

// sigSentMessages is the messages array of a captured Anthropic request body.
type sigSentMessages struct {
	Messages []struct {
		Role    string           `json:"role"`
		Content []map[string]any `json:"content"`
	} `json:"messages"`
}

func TestAnthropicThinkingReplay_SecondRequestKeepsSignature(t *testing.T) {
	var mu sync.Mutex
	var requests int
	var secondBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		requests++
		turn := requests
		if turn == 2 {
			secondBody = body
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if turn == 1 {
			_, _ = w.Write([]byte(sigSignedThinkingBody))
			return
		}
		_, _ = w.Write([]byte(`{"id":"msg_done","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":20,"output_tokens":3}}`))
	}))
	defer server.Close()

	client := sigAnthropicClient(server.URL, "claude-sonnet-4-20250514")
	history := make([]Message, 0, 3)
	history = append(history, NewUserMessage("read /tmp/x"))

	first, err := client.CreateMessage(context.Background(), &Request{
		Messages: history,
		Thinking: &ThinkingConfig{Enabled: true, Budget: 2048},
		Tools:    []ToolDefinition{{Name: "read_file", Description: "read a file"}},
	})
	if err != nil {
		t.Fatalf("first CreateMessage: %v", err)
	}

	history = append(history,
		Message{Role: RoleAssistant, Blocks: first.Content},
		Message{Role: RoleUser, Blocks: []ContentBlock{{
			Type:      ContentTypeToolResult,
			ToolUseID: "toolu_sig",
			Text:      "file contents",
		}}},
	)

	// Persisting and reloading the whole conversation must not strip the
	// signature: agents resume from stored history, not from live structs.
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded []Message
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}

	if _, err := client.CreateMessage(context.Background(), &Request{
		Messages: reloaded,
		Thinking: &ThinkingConfig{Enabled: true, Budget: 2048},
		Tools:    []ToolDefinition{{Name: "read_file", Description: "read a file"}},
	}); err != nil {
		t.Fatalf("second CreateMessage: %v", err)
	}

	mu.Lock()
	captured := secondBody
	total := requests
	mu.Unlock()
	if total != 2 {
		t.Fatalf("expected 2 requests, got %d", total)
	}

	var sent sigSentMessages
	if err := json.Unmarshal(captured, &sent); err != nil {
		t.Fatalf("decode second request body: %v\n%s", err, captured)
	}
	if len(sent.Messages) != 3 {
		t.Fatalf("expected user/assistant/user, got %d messages: %s", len(sent.Messages), captured)
	}

	assistant := sent.Messages[1]
	if assistant.Role != "assistant" {
		t.Fatalf("second message is %q, not the assistant turn", assistant.Role)
	}
	if len(assistant.Content) != 3 {
		t.Fatalf("assistant turn replayed %d blocks, want thinking, redacted_thinking and tool_use: %s",
			len(assistant.Content), captured)
	}

	thinking := assistant.Content[0]
	if thinking["type"] != "thinking" {
		t.Errorf("first replayed block is %v, want thinking", thinking["type"])
	}
	if thinking["thinking"] != "check the file first" {
		t.Errorf("thinking text changed: %v", thinking["thinking"])
	}
	if thinking["signature"] != "ErUBCkYIBBgCIkDSig/+8vQ==" {
		t.Errorf("thinking signature changed: %v", thinking["signature"])
	}

	redacted := assistant.Content[1]
	if redacted["type"] != "redacted_thinking" {
		t.Errorf("second replayed block is %v, want redacted_thinking", redacted["type"])
	}
	if redacted["data"] != "EroBCoYBCAEQABgCKkBz3w==" {
		t.Errorf("redacted thinking data changed: %v", redacted["data"])
	}

	toolUse := assistant.Content[2]
	if toolUse["type"] != "tool_use" || toolUse["id"] != "toolu_sig" {
		t.Errorf("tool_use block did not replay: %v", toolUse)
	}

	result := sent.Messages[2]
	if len(result.Content) != 1 || result.Content[0]["tool_use_id"] != "toolu_sig" {
		t.Errorf("tool result lost its pairing: %v", result.Content)
	}
}

// sigTwoTurnServer answers the first request with firstBody and captures the
// second request's body, so a test can assert what a replayed history sends.
// It reports the request count so a preflight rejection is visible as a turn
// that never reached the wire.
func sigTwoTurnServer(t *testing.T, firstBody string) (*httptest.Server, func() (int, []byte)) {
	t.Helper()
	var mu sync.Mutex
	var requests int
	var secondBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		requests++
		turn := requests
		if turn == 2 {
			secondBody = body
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if turn == 1 {
			_, _ = w.Write([]byte(firstBody))
			return
		}
		_, _ = w.Write([]byte(`{"id":"msg_done","type":"message","role":"assistant","model":"claude-haiku-4-5","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":20,"output_tokens":3}}`))
	}))
	t.Cleanup(server.Close)

	return server, func() (int, []byte) {
		mu.Lock()
		defer mu.Unlock()
		return requests, secondBody
	}
}

// sigPlainToolTurnBody is a tool turn with no thinking anywhere in it.
const sigPlainToolTurnBody = `{"id":"msg_plain","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","stop_reason":"tool_use","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"toolu_plain","name":"read_file","input":{"path":"/tmp/x"}}],"usage":{"input_tokens":12,"output_tokens":34}}`

// sigReplayHistory runs one request against server, then replays the response
// as assistant history (through JSON, as a resumed agent would) under model.
func sigReplayHistory(t *testing.T, server *httptest.Server, model string, edit func([]ContentBlock)) error {
	t.Helper()
	client := sigAnthropicClient(server.URL, "claude-sonnet-4-20250514")
	first, err := client.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessage("read /tmp/x")},
		Tools:    []ToolDefinition{{Name: "read_file", Description: "read a file"}},
	})
	if err != nil {
		t.Fatalf("first CreateMessage: %v", err)
	}
	if edit != nil {
		edit(first.Content)
	}

	history := []Message{
		NewUserMessage("read /tmp/x"),
		{Role: RoleAssistant, Blocks: first.Content},
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded []Message
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateMessage(context.Background(), &Request{
		Model:    model,
		Messages: reloaded,
		Tools:    []ToolDefinition{{Name: "read_file", Description: "read a file"}},
	})
	return err
}

func TestAnthropicThinkingReplay_TextOnlyHistorySurvivesModelSwitch(t *testing.T) {
	server, captured := sigTwoTurnServer(t, sigPlainToolTurnBody)

	// A turn with no thinking in it carries no opaque bytes, so switching
	// models must not fail preflight.
	if err := sigReplayHistory(t, server, "claude-haiku-4-5", nil); err != nil {
		t.Fatalf("model switch on a thinking-free history: %v", err)
	}

	total, body := captured()
	if total != 2 {
		t.Fatalf("expected 2 requests, got %d", total)
	}
	var sent sigSentMessages
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode second request body: %v\n%s", err, body)
	}
	assistant := sent.Messages[1]
	if len(assistant.Content) != 2 {
		t.Fatalf("assistant turn replayed %d blocks, want text and tool_use: %s", len(assistant.Content), body)
	}
	if assistant.Content[0]["text"] != "hello" {
		t.Errorf("text block did not replay: %v", assistant.Content[0])
	}
	if assistant.Content[1]["id"] != "toolu_plain" {
		t.Errorf("tool_use block did not replay: %v", assistant.Content[1])
	}
}

func TestAnthropicThinkingReplay_CallerEditToAssistantTextReachesWire(t *testing.T) {
	server, captured := sigTwoTurnServer(t, sigPlainToolTurnBody)

	// Text and tool_use round-trip through normalized fields, so a caller
	// rewriting them is authoritative — an envelope would discard the edit.
	err := sigReplayHistory(t, server, "claude-sonnet-4-20250514", func(blocks []ContentBlock) {
		for i := range blocks {
			switch blocks[i].Type {
			case ContentTypeText:
				blocks[i].Text = "EDITED BY CALLER"
			case ContentTypeToolUse:
				blocks[i].Input = map[string]any{"path": "/edited"}
			}
		}
	})
	if err != nil {
		t.Fatalf("second CreateMessage: %v", err)
	}

	total, body := captured()
	if total != 2 {
		t.Fatalf("expected 2 requests, got %d", total)
	}
	var sent sigSentMessages
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode second request body: %v\n%s", err, body)
	}
	assistant := sent.Messages[1]
	if assistant.Content[0]["text"] != "EDITED BY CALLER" {
		t.Errorf("caller edit to assistant text was discarded: %v", assistant.Content[0])
	}
	input, _ := assistant.Content[1]["input"].(map[string]any)
	if input["path"] != "/edited" {
		t.Errorf("caller edit to tool input was discarded: %v", assistant.Content[1])
	}
}

func TestAnthropicThinkingReplay_UnpreservableRedactedThinkingIsDropped(t *testing.T) {
	// A block the SDK did not decode from the wire has no raw JSON, so its
	// encrypted data cannot be replayed. Redacted thinking has nothing to
	// display either, so keeping it would leave a block that is empty on the
	// wire and empty on screen — an invalid state, not a lossy one.
	msg := &anthropic.Message{
		Model: "claude-sonnet-4-20250514",
		Content: []anthropic.ContentBlockUnion{
			{Type: "redacted_thinking", Data: "encrypted"},
			{Type: "text", Text: "visible"},
		},
	}

	resp := convertResponse(msg, "claude-sonnet-4-20250514")
	if len(resp.Content) != 1 {
		t.Fatalf("expected the unpreservable block to be dropped, got %+v", resp.Content)
	}
	if resp.Content[0].Type != ContentTypeText || resp.Content[0].Text != "visible" {
		t.Errorf("wrong block survived: %+v", resp.Content[0])
	}

	// A dropped block must not reappear as an empty replay block on the wire.
	params := sigConvertRequest(t, &Request{
		Messages: []Message{{Role: RoleAssistant, Blocks: resp.Content}},
	})
	wire, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, []byte("redacted_thinking")) {
		t.Errorf("dropped block leaked into the request: %s", wire)
	}
}

func TestAnthropicThinkingReplay_ModelSwitchDropsSignedThinking(t *testing.T) {
	server, captured := sigTwoTurnServer(t, sigSignedThinkingBody)

	// A model switch no longer rejects a signed-thinking history: it drops
	// the mismatched blocks and warns, and the second turn still reaches
	// the wire, matching the raw API's own tolerance for a stale envelope.
	if err := sigReplayHistory(t, server, "claude-haiku-4-5", nil); err != nil {
		t.Fatalf("model switch must warn and drop, not error: %v", err)
	}

	total, body := captured()
	if total != 2 {
		t.Fatalf("expected 2 requests, got %d", total)
	}
	var sent sigSentMessages
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode second request body: %v\n%s", err, body)
	}
	assistant := sent.Messages[1]
	if len(assistant.Content) != 1 {
		t.Fatalf("expected only the tool_use block to survive, got %d blocks: %s", len(assistant.Content), body)
	}
	if assistant.Content[0]["id"] != "toolu_sig" {
		t.Errorf("surviving block is not the tool_use call: %v", assistant.Content[0])
	}
	if bytes.Contains(body, []byte("thinking")) {
		t.Errorf("dropped thinking envelope leaked into the request: %s", body)
	}
}

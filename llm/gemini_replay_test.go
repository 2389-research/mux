// ABOUTME: Tests that Gemini thought signatures on function-call parts survive
// ABOUTME: conversion, history JSON and streaming, and replay byte-for-byte.
package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"
	"google.golang.org/genai"
)

// sigThoughtSignature is deliberately non-UTF8 and includes a zero byte: a
// signature is opaque bytes, and any lossy hop through a string would corrupt
// it. Gemini answers HTTP 400 when a replayed call's signature is wrong.
var sigThoughtSignature = []byte{0, 1, 254, 255}

// sigGeminiParallelCallsBody is a model turn with two function calls, as
// Gemini returns them: only the first call of the step carries a signature.
func sigGeminiParallelCallsBody() string {
	return fmt.Sprintf(`{"responseId":"r1","candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[`+
		`{"functionCall":{"name":"read_file","args":{"path":"/a"}},"thoughtSignature":%q},`+
		`{"functionCall":{"name":"read_file","args":{"path":"/b"}}}`+
		`]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":7}}`,
		base64.StdEncoding.EncodeToString(sigThoughtSignature))
}

// sigGeminiResponse decodes a raw generateContent body through the SDK, so the
// signature reaches the converter exactly as the wire carried it.
func sigGeminiResponse(t *testing.T, raw string) *genai.GenerateContentResponse {
	t.Helper()
	var resp genai.GenerateContentResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode fixture response: %v", err)
	}
	return &resp
}

// sigGeminiContents converts a request and fails the test on error, so call
// sites stay a single expression.
func sigGeminiContents(t *testing.T, req *Request) ([]*genai.Content, *genai.GenerateContentConfig) {
	t.Helper()
	contents, config, err := convertGeminiRequest(req)
	if err != nil {
		t.Fatalf("convertGeminiRequest: %v", err)
	}
	return contents, config
}

// sigGeminiContent converts one message and fails the test on error.
func sigGeminiContent(t *testing.T, msg Message) *genai.Content {
	t.Helper()
	content, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("convertMessage: %v", err)
	}
	return content
}

// sigGeminiResult converts a response and fails the test on error.
func sigGeminiResult(t *testing.T, resp *genai.GenerateContentResponse, model string) *Response {
	t.Helper()
	result, err := convertGeminiResponse(resp, model)
	if err != nil {
		t.Fatalf("convertGeminiResponse: %v", err)
	}
	return result
}

// sigDecodeSignature base64-decodes a thoughtSignature from a request body.
func sigDecodeSignature(t *testing.T, encoded string) []byte {
	t.Helper()
	if encoded == "" {
		t.Fatal("request carried no thoughtSignature")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("thoughtSignature is not base64: %v", err)
	}
	return decoded
}

func TestGeminiSignedPartReplay_ParallelCallsKeepSignature(t *testing.T) {
	response, err := convertGeminiResponse(sigGeminiResponse(t, sigGeminiParallelCallsBody()), "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("convertGeminiResponse: %v", err)
	}
	if len(response.Content) != 2 {
		t.Fatalf("expected two tool calls, got %+v", response.Content)
	}
	if response.Content[0].Replay == nil {
		t.Fatal("signed function call carries no replay envelope")
	}
	if response.Content[0].Replay.Provider != "gemini" || response.Content[0].Replay.Model != "gemini-2.5-pro" {
		t.Errorf("replay identity: got %+v", response.Content[0].Replay)
	}
	if response.Content[1].Replay != nil {
		t.Errorf("unsigned parallel call should stay normalized, got %+v", response.Content[1].Replay)
	}

	// Agents resume from stored history, so the envelope has to survive JSON.
	encoded, err := json.Marshal(Message{Role: RoleAssistant, Blocks: response.Content})
	if err != nil {
		t.Fatal(err)
	}
	var restored Message
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}

	contents, _, err := convertGeminiRequest(&Request{Model: "gemini-2.5-pro", Messages: []Message{restored}})
	if err != nil {
		t.Fatalf("convertGeminiRequest: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected one content, got %d", len(contents))
	}
	body, err := json.Marshal(contents[0])
	if err != nil {
		t.Fatal(err)
	}

	got := sigDecodeSignature(t, gjson.GetBytes(body, "parts.0.thoughtSignature").String())
	if string(got) != string(sigThoughtSignature) {
		t.Errorf("signature bytes changed: got %v want %v; body=%s", got, sigThoughtSignature, body)
	}
	if name := gjson.GetBytes(body, "parts.0.functionCall.name").String(); name != "read_file" {
		t.Errorf("signed call lost its name: %q; body=%s", name, body)
	}
	if path := gjson.GetBytes(body, "parts.0.functionCall.args.path").String(); path != "/a" {
		t.Errorf("signed call lost its arguments: %q; body=%s", path, body)
	}
	if path := gjson.GetBytes(body, "parts.1.functionCall.args.path").String(); path != "/b" {
		t.Errorf("second parallel call did not replay: %q; body=%s", path, body)
	}
	if gjson.GetBytes(body, "parts.1.thoughtSignature").Exists() {
		t.Errorf("unsigned call must not gain a signature; body=%s", body)
	}
}

func TestGeminiSignedPartReplay_SecondRequestKeepsSignature(t *testing.T) {
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
			_, _ = w.Write([]byte(sigGeminiParallelCallsBody()))
			return
		}
		_, _ = w.Write([]byte(`{"responseId":"r2","candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"done"}]}}]}`))
	}))
	defer server.Close()

	client, err := NewGeminiClientWithBaseURL(context.Background(), "test-key", "gemini-2.5-pro", server.URL)
	if err != nil {
		t.Fatalf("NewGeminiClientWithBaseURL: %v", err)
	}

	history := make([]Message, 0, 3)
	history = append(history, NewUserMessage("read /a and /b"))
	first, err := client.CreateMessage(context.Background(), &Request{
		Messages: history,
		Tools:    []ToolDefinition{{Name: "read_file", Description: "read a file"}},
	})
	if err != nil {
		t.Fatalf("first CreateMessage: %v", err)
	}

	history = append(history,
		Message{Role: RoleAssistant, Blocks: first.Content},
		Message{Role: RoleUser, Blocks: []ContentBlock{{
			Type: ContentTypeToolResult,
			Name: "read_file",
			Text: "file contents",
		}}},
	)

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

	got := sigDecodeSignature(t, gjson.GetBytes(captured, "contents.1.parts.0.thoughtSignature").String())
	if string(got) != string(sigThoughtSignature) {
		t.Errorf("replayed signature changed: got %v want %v; body=%s", got, sigThoughtSignature, captured)
	}
	if name := gjson.GetBytes(captured, "contents.1.parts.0.functionCall.name").String(); name != "read_file" {
		t.Errorf("replayed call lost its name: %q; body=%s", name, captured)
	}
	if name := gjson.GetBytes(captured, "contents.2.parts.0.functionResponse.name").String(); name != "read_file" {
		t.Errorf("tool result lost its pairing: %q; body=%s", name, captured)
	}
}

func TestGeminiSignedPartReplay_ModelSwitchDropsSignedPart(t *testing.T) {
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
		_, _ = w.Write([]byte(sigGeminiParallelCallsBody()))
	}))
	defer server.Close()

	client, err := NewGeminiClientWithBaseURL(context.Background(), "test-key", "gemini-2.5-pro", server.URL)
	if err != nil {
		t.Fatalf("NewGeminiClientWithBaseURL: %v", err)
	}

	first, err := client.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessage("read /a and /b")},
		Tools:    []ToolDefinition{{Name: "read_file", Description: "read a file"}},
	})
	if err != nil {
		t.Fatalf("first CreateMessage: %v", err)
	}

	// A model switch no longer rejects a signed-part history: it drops the
	// mismatched part's signature and warns. Unlike a thinking-only block, a
	// function call has a normalized fallback (name + args), so the call
	// itself still reaches the wire, just unsigned.
	if _, err := client.CreateMessage(context.Background(), &Request{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			NewUserMessage("read /a and /b"),
			{Role: RoleAssistant, Blocks: first.Content},
		},
	}); err != nil {
		t.Fatalf("model switch must warn and drop, not error: %v", err)
	}

	mu.Lock()
	total := requests
	captured := secondBody
	mu.Unlock()
	if total != 2 {
		t.Fatalf("expected 2 requests, got %d", total)
	}

	if sig := gjson.GetBytes(captured, "contents.1.parts.0.thoughtSignature"); sig.Exists() && sig.String() != "" {
		t.Errorf("dropped signature reached the wire: %q; body=%s", sig.String(), captured)
	}
	if name := gjson.GetBytes(captured, "contents.1.parts.0.functionCall.name").String(); name != "read_file" {
		t.Errorf("first call lost its name after the drop: %q; body=%s", name, captured)
	}
	if path := gjson.GetBytes(captured, "contents.1.parts.0.functionCall.args.path").String(); path != "/a" {
		t.Errorf("first call lost its arguments after the drop: %q; body=%s", path, captured)
	}
	if path := gjson.GetBytes(captured, "contents.1.parts.1.functionCall.args.path").String(); path != "/b" {
		t.Errorf("second parallel call did not survive: %q; body=%s", path, captured)
	}
}

// TestSigReplayPayloadCheck covers both branches of validateReplay's
// provider-specific payload check: Gemini parts have no "type" discriminator,
// every other provider's items do.
func TestSigReplayPayloadCheck(t *testing.T) {
	signedPart := `{"functionCall":{"name":"read_file"},"thoughtSignature":"AAH+/w=="}`

	cases := []struct {
		name     string
		provider string
		data     string
		wantErr  bool
	}{
		{"gemini part without a type field", "gemini", signedPart, false},
		{"gemini text part", "gemini", `{"text":"hi"}`, false},
		{"gemini empty object", "gemini", `{}`, true},
		{"gemini unrelated object", "gemini", `{"totally":"unrelated"}`, true},
		{"gemini part with one unknown field", "gemini", `{"text":"hi","sneaky":1}`, true},
		{"gemini non-object", "gemini", `["text"]`, true},
		{"anthropic block with a type field", "anthropic", `{"type":"thinking","signature":"x"}`, false},
		{"anthropic block without a type field", "anthropic", signedPart, true},
		{"anthropic block with an empty type", "anthropic", `{"type":""}`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{{
				Type: ContentTypeToolUse,
				Replay: &ProviderReplay{
					Provider: tc.provider,
					Model:    "m",
					Data:     json.RawMessage(tc.data),
				},
			}}}}
			_, err := validateReplay(tc.provider, "m", messages)
			if tc.wantErr && err == nil {
				t.Fatalf("want an error for %s payload %s", tc.provider, tc.data)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %s payload %s: %v", tc.provider, tc.data, err)
			}
			// Opaque provider bytes must not leak into the error text.
			if err != nil && len(tc.data) > 32 && strings.Contains(err.Error(), tc.data) {
				t.Errorf("error leaked the whole payload: %v", err)
			}
		})
	}
}

func TestGeminiSignedPartReplay_StreamedToolCallKeepsSignature(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("data: " + sigGeminiParallelCallsBody() + "\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client, err := NewGeminiClientWithBaseURL(context.Background(), "test-key", "gemini-2.5-pro", server.URL)
	if err != nil {
		t.Fatalf("NewGeminiClientWithBaseURL: %v", err)
	}

	events, err := client.CreateMessageStream(context.Background(), &Request{
		Messages: []Message{NewUserMessage("read /a and /b")},
		Tools:    []ToolDefinition{{Name: "read_file", Description: "read a file"}},
	})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}

	var blocks []ContentBlock
	var final *Response
	for event := range events {
		switch event.Type {
		case EventError:
			t.Fatalf("unexpected error event: %v", event.Error)
		case EventContentStop:
			if event.Block != nil {
				blocks = append(blocks, *event.Block)
			}
		case EventMessageStop:
			final = event.Response
		}
	}
	if len(blocks) != 2 {
		t.Fatalf("expected two streamed tool blocks, got %+v", blocks)
	}
	if blocks[0].Replay == nil {
		t.Fatal("streamed signed call carries no replay envelope")
	}
	if blocks[1].Replay != nil {
		t.Errorf("streamed unsigned call should stay normalized: %+v", blocks[1].Replay)
	}
	if final == nil {
		t.Fatal("stream produced no final response")
	}

	contents, _, err := convertGeminiRequest(&Request{
		Model:    "gemini-2.5-pro",
		Messages: []Message{{Role: RoleAssistant, Blocks: blocks}},
	})
	if err != nil {
		t.Fatalf("convertGeminiRequest: %v", err)
	}
	body, err := json.Marshal(contents[0])
	if err != nil {
		t.Fatal(err)
	}
	got := sigDecodeSignature(t, gjson.GetBytes(body, "parts.0.thoughtSignature").String())
	if string(got) != string(sigThoughtSignature) {
		t.Errorf("streamed signature changed: got %v want %v; body=%s", got, sigThoughtSignature, body)
	}
}

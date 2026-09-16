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

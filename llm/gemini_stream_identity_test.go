// ABOUTME: Regression coverage for typed Gemini stream block identity and
// ABOUTME: honest tool-argument limits, driven through the real SDK/httptest path.
package llm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// geminiFlushedSSEServer serves each payload as its own flushed SSE frame, so
// the SDK observes them as separate reads rather than one batched body. This is
// how the plan's Unicode-split criterion is exercised: a multi-byte character's
// bytes must survive a transport boundary.
func geminiFlushedSSEServer(t *testing.T, payloads ...string) *httptest.Server {
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
	}))
}

// TestGeminiStreamUnicodeSplitWrites pins that real multi-byte text survives
// split transport writes and that the same-channel continuation merges across
// chunk boundaries into one block with one stable identity.
func TestGeminiStreamUnicodeSplitWrites(t *testing.T) {
	server := geminiFlushedSSEServer(t,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"東"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"京"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"タワー"}]},"finishReason":"STOP"}]}`,
	)
	defer server.Close()

	client, err := NewGeminiClientWithBaseURL(context.Background(), "fixture", "fixture", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	events, err := client.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var blockIDs = map[string]bool{}
	var final *Response
	var refs []StreamBlockRef
	for event := range events {
		if event.Type == EventError {
			t.Fatalf("unexpected error: %v", event.Error)
		}
		if event.Type == EventContentDelta {
			if event.BlockID == "" {
				t.Fatal("missing block identity on Unicode delta")
			}
			blockIDs[event.BlockID] = true
			text += event.Text
		}
		if event.Type == EventMessageStop {
			final = event.Response
			refs = event.FinalBlocks
		}
	}
	if text != "東京タワー" {
		t.Fatalf("streamed text = %q, want %q", text, "東京タワー")
	}
	if len(blockIDs) != 1 {
		t.Errorf("Unicode continuation used %d block identities, want 1: %v", len(blockIDs), blockIDs)
	}
	if final == nil || final.TextContent() != "東京タワー" {
		t.Fatalf("final = %+v", final)
	}
	if len(final.Content) != 1 || len(refs) != 1 {
		t.Errorf("content = %+v refs = %+v, want one merged block", final.Content, refs)
	}
}

// geminiIdentityChunks is the exact wire sequence from the stream-gemini plan:
// a thought part and a text part in one chunk, the text continuing with a
// function call on the next chunk, then a metadata-only trailer.
var geminiIdentityChunks = []string{
	`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"consider","thought":true},{"text":"東"}]}}],"modelVersion":"fixture","responseId":"r1"}`,
	`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"京"},{"functionCall":{"id":"c1","name":"read","args":{"path":"a"}}}]}}],"modelVersion":"fixture","responseId":"r1"}`,
	`{"candidates":[{"index":0,"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":5,"totalTokenCount":8},"modelVersion":"fixture","responseId":"r1"}`,
}

// TestGeminiStreamIdentity is the plan's acceptance regression: every live
// text/thinking delta carries a stable stream-local BlockID, a thinking part
// never leaks into answer text, a function call is a complete block with no
// invented argument delta, and the terminal event maps every final block to
// the identity that produced it.
func TestGeminiStreamIdentity(t *testing.T) {
	server := geminiSSEServer(t, geminiIdentityChunks...)

	client, err := NewGeminiClientWithBaseURL(context.Background(), "fixture", "fixture", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	events, err := client.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("test")}})
	if err != nil {
		t.Fatal(err)
	}
	var final *Response
	var refs []StreamBlockRef
	var text string
	var thought string
	var argumentDeltas int
	for event := range events {
		if event.Type == EventError {
			t.Fatal(event.Error)
		}
		if event.Type == EventContentDelta {
			if event.BlockID == "" {
				t.Fatal("missing block identity")
			}
			switch event.DeltaKind {
			case StreamDeltaText:
				text += event.Text
			case StreamDeltaThinking:
				thought += event.Text
			case StreamDeltaToolInput:
				argumentDeltas++
			}
		}
		if event.Type == EventMessageStop {
			final = event.Response
			refs = event.FinalBlocks
		}
	}
	if final == nil || text != "東京" || thought != "consider" || argumentDeltas != 0 {
		t.Fatalf("final=%+v text=%q thought=%q args=%d", final, text, thought, argumentDeltas)
	}
	if final.TextContent() != text || len(refs) != len(final.Content) || final.ToolUses()[0].ID != "c1" {
		t.Fatalf("final=%+v refs=%+v", final, refs)
	}
}

// TestGeminiStreamSameNameCalls pins that calls are keyed by native ID, not by
// tool name: two same-name calls with distinct IDs stay distinct blocks, and
// a chunk repeating a part index does not collapse them.
func TestGeminiStreamSameNameCalls(t *testing.T) {
	server := geminiSSEServer(t,
		`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"read","args":{"path":"a"}}}]}}]}`,
		`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"id":"c2","name":"read","args":{"path":"b"}}}]}}]}`,
		`{"candidates":[{"index":0,"content":{"role":"model","parts":[]},"finishReason":"STOP"}]}`,
	)

	client, err := NewGeminiClientWithBaseURL(context.Background(), "fixture", "fixture", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	events, err := client.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("read both")}})
	if err != nil {
		t.Fatal(err)
	}
	var stopIDs []string
	var final *Response
	var refs []StreamBlockRef
	for event := range events {
		if event.Type == EventError {
			t.Fatalf("unexpected error: %v", event.Error)
		}
		if event.Type == EventContentStop && event.Block != nil && event.Block.Type == ContentTypeToolUse {
			stopIDs = append(stopIDs, event.BlockID)
		}
		if event.Type == EventMessageStop {
			final = event.Response
			refs = event.FinalBlocks
		}
	}
	if final == nil {
		t.Fatal("no final response")
	}
	uses := final.ToolUses()
	if len(uses) != 2 {
		t.Fatalf("tool uses = %d, want 2: %+v", len(uses), final.Content)
	}
	if uses[0].ID != "c1" || uses[1].ID != "c2" {
		t.Errorf("call ids = %q/%q, want c1/c2", uses[0].ID, uses[1].ID)
	}
	if len(stopIDs) != 2 || stopIDs[0] == stopIDs[1] || stopIDs[0] == "" || stopIDs[1] == "" {
		t.Errorf("tool stop block ids = %v, want two distinct non-empty identities", stopIDs)
	}
	if len(refs) != len(final.Content) {
		t.Errorf("refs = %+v, content = %+v", refs, final.Content)
	}
}

// TestGeminiStreamNoFinal pins that a stream which ends without a meaningful
// finish reason is a protocol failure, not a successful empty turn: both a
// wholly empty stream and content followed by a bare EOF must error.
func TestGeminiStreamNoFinal(t *testing.T) {
	t.Run("empty stream", func(t *testing.T) {
		server := geminiSSEServer(t)
		client, err := NewGeminiClientWithBaseURL(context.Background(), "fixture", "fixture", server.URL)
		if err != nil {
			t.Fatal(err)
		}
		gotError, gotStop := drainGeminiStream(t, client)
		if gotError == nil {
			t.Fatal("expected a protocol error for an empty stream, got none")
		}
		if gotStop {
			t.Error("an unterminated stream must not also emit a successful message_stop")
		}
		assertGeminiProtocolError(t, gotError)
	})

	t.Run("content without finish", func(t *testing.T) {
		server := geminiSSEServer(t,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`,
		)
		client, err := NewGeminiClientWithBaseURL(context.Background(), "fixture", "fixture", server.URL)
		if err != nil {
			t.Fatal(err)
		}
		gotError, gotStop := drainGeminiStream(t, client)
		if gotError == nil {
			t.Fatal("expected a protocol error for content ending without a finish reason, got none")
		}
		if gotStop {
			t.Error("an unterminated stream must not also emit a successful message_stop")
		}
		assertGeminiProtocolError(t, gotError)
	})
}

// TestGeminiStreamThoughtPrivate pins that a thought part is delivered only as
// public thinking text: it is never classified as a reasoning summary, and
// opaque thought-signature bytes never reach a consumer-visible Text field.
func TestGeminiStreamThoughtPrivate(t *testing.T) {
	signature := base64.StdEncoding.EncodeToString([]byte("opaque-signature-bytes"))
	server := geminiSSEServer(t,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"secret reasoning","thought":true,"thoughtSignature":"`+signature+`"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`,
	)

	client, err := NewGeminiClientWithBaseURL(context.Background(), "fixture", "fixture", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	events, err := client.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("think")}})
	if err != nil {
		t.Fatal(err)
	}
	var thought string
	var text string
	var final *Response
	for event := range events {
		if event.Type == EventError {
			t.Fatalf("unexpected error: %v", event.Error)
		}
		if event.Type == EventContentDelta {
			if event.DeltaKind == StreamDeltaReasoningSummary {
				t.Error("a thought part must not be classified as a reasoning summary")
			}
			if strings.Contains(event.Text, signature) {
				t.Errorf("thought signature leaked into delta text: %q", event.Text)
			}
			switch event.DeltaKind {
			case StreamDeltaThinking:
				thought += event.Text
			case StreamDeltaText:
				text += event.Text
			}
		}
		if event.Type == EventMessageStop {
			final = event.Response
		}
	}
	if thought != "secret reasoning" {
		t.Errorf("thinking = %q, want %q", thought, "secret reasoning")
	}
	if text != "answer" {
		t.Errorf("text = %q, want %q", text, "answer")
	}
	if final == nil {
		t.Fatal("no final response")
	}
	if strings.Contains(final.TextContent(), signature) {
		t.Errorf("thought signature leaked into final text: %q", final.TextContent())
	}
}

// TestGeminiStreamConflictingCall pins the duplicate-call rule: a repeated
// native ID describing the same completed call is tolerated once, while the
// same ID with changed name or arguments is a protocol error rather than a
// silent merge.
func TestGeminiStreamConflictingCall(t *testing.T) {
	t.Run("identical duplicate accepted once", func(t *testing.T) {
		server := geminiSSEServer(t,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"read","args":{"path":"a"}}}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"read","args":{"path":"a"}}}]},"finishReason":"STOP"}]}`,
		)
		client, err := NewGeminiClientWithBaseURL(context.Background(), "fixture", "fixture", server.URL)
		if err != nil {
			t.Fatal(err)
		}
		gotError, _ := drainGeminiStream(t, client)
		if gotError != nil {
			t.Fatalf("identical duplicate should be accepted, got %v", gotError)
		}
	})

	t.Run("changed duplicate rejected", func(t *testing.T) {
		server := geminiSSEServer(t,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"read","args":{"path":"a"}}}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"read","args":{"path":"b"}}}]},"finishReason":"STOP"}]}`,
		)
		client, err := NewGeminiClientWithBaseURL(context.Background(), "fixture", "fixture", server.URL)
		if err != nil {
			t.Fatal(err)
		}
		gotError, _ := drainGeminiStream(t, client)
		if gotError == nil {
			t.Fatal("expected a protocol error for a changed duplicate call, got none")
		}
		assertGeminiProtocolError(t, gotError)
	})
}

// drainGeminiStream consumes a stream to completion, returning the first error
// event (if any) and whether a successful message_stop was seen.
func drainGeminiStream(t *testing.T, client *GeminiClient) (error, bool) {
	t.Helper()
	events, err := client.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}
	var gotError error
	var gotStop bool
	for event := range events {
		switch event.Type {
		case EventError:
			if gotError == nil {
				gotError = event.Error
			}
		case EventMessageStop:
			gotStop = true
		}
	}
	return gotError, gotStop
}

// assertGeminiProtocolError fails unless err is a *StreamProtocolError from the
// Gemini provider with a fixed, sanitized reason.
func assertGeminiProtocolError(t *testing.T, err error) {
	t.Helper()
	var pe *StreamProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *StreamProtocolError, got %T: %v", err, err)
	}
	if pe.Provider != "gemini" {
		t.Errorf("provider = %q, want gemini", pe.Provider)
	}
	if pe.Reason == "" {
		t.Error("protocol error carried an empty reason")
	}
}

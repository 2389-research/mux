// ABOUTME: Regression coverage for typed Anthropic stream block identity.
// ABOUTME: Exercises the real SDK/httptest streaming path, not the accumulator directly.
package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// identityStreamEvents is the exact wire sequence from the stream-anthropic
// plan: a text block followed by a tool_use block whose input arrives as two
// input_json_delta fragments.
var identityStreamEvents = []struct {
	kind string
	data string
}{
	{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
	{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
	{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"東京"}}`},
	{"content_block_stop", `{"type":"content_block_stop","index":0}`},
	{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-1","name":"read","input":{}}}`},
	{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`},
	{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"a\"}"}}`},
	{"content_block_stop", `{"type":"content_block_stop","index":1}`},
	{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":8}}`},
	{"message_stop", `{"type":"message_stop"}`},
}

// writeIdentitySSE writes each (event, data) pair as an SSE frame, flushing
// between frames so the client observes them as separate reads.
func writeIdentitySSE(t *testing.T, w http.ResponseWriter, events []struct{ kind, data string }) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Error("expected http.ResponseWriter to be an http.Flusher")
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	for _, event := range events {
		if _, err := w.Write([]byte("event: " + event.kind + "\ndata: " + event.data + "\n\n")); err != nil {
			return
		}
		flusher.Flush()
	}
}

// deltaTuple is the (BlockID, DeltaKind, Text) triple TestAnthropicStreamIdentity
// checks against the plan's expected delta sequence.
type deltaTuple struct {
	BlockID string
	Kind    StreamDeltaKind
	Text    string
}

func TestAnthropicStreamIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeIdentitySSE(t, w, identityStreamEvents)
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("fixture"),
			option.WithBaseURL(server.URL),
			option.WithMaxRetries(0),
		),
		model: "fixture",
	}

	eventChan, err := client.CreateMessageStream(context.Background(), &Request{
		Messages: []Message{NewUserMessage("read a file")},
	})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}

	var deltas []deltaTuple
	var stopBlock1 *ContentBlock
	var final *Response
	var finalRefs []StreamBlockRef
	for event := range eventChan {
		if event.Type == EventError {
			t.Fatalf("unexpected error event: %v", event.Error)
		}
		if event.Type == EventContentDelta {
			deltas = append(deltas, deltaTuple{BlockID: event.BlockID, Kind: event.DeltaKind, Text: event.Text})
		}
		if event.Type == EventContentStop && event.Index == 1 {
			stopBlock1 = event.Block
		}
		if event.Type == EventMessageStop {
			final = event.Response
			finalRefs = event.FinalBlocks
		}
	}

	wantDeltas := []deltaTuple{
		{BlockID: "anthropic:0", Kind: StreamDeltaText, Text: "東京"},
		{BlockID: "anthropic:1", Kind: StreamDeltaToolInput, Text: `{"path":`},
		{BlockID: "anthropic:1", Kind: StreamDeltaToolInput, Text: `"a"}`},
	}
	if len(deltas) != len(wantDeltas) {
		t.Fatalf("delta count: got %d %+v, want %d %+v", len(deltas), deltas, len(wantDeltas), wantDeltas)
	}
	for i, want := range wantDeltas {
		if deltas[i] != want {
			t.Errorf("delta %d: got %+v, want %+v", i, deltas[i], want)
		}
	}

	if stopBlock1 == nil {
		t.Fatal("expected content_block_stop for index 1 to carry Block")
	}
	if stopBlock1.ID != "call-1" {
		t.Errorf("stop block 1 ID: got %q, want call-1", stopBlock1.ID)
	}

	if final == nil {
		t.Fatal("expected a final response from message_stop")
	}
	wantRefs := []StreamBlockRef{{BlockID: "anthropic:0", ContentIndex: 0}, {BlockID: "anthropic:1", ContentIndex: 1}}
	if len(finalRefs) != len(wantRefs) {
		t.Fatalf("final refs: got %+v, want %+v", finalRefs, wantRefs)
	}
	for i, want := range wantRefs {
		if finalRefs[i] != want {
			t.Errorf("final ref %d: got %+v, want %+v", i, finalRefs[i], want)
		}
	}

	if final.TextContent() != "東京" {
		t.Errorf("final text: got %q, want 東京 exactly once", final.TextContent())
	}
}

// TestAnthropicStreamExplicitEmptyToolInput covers the acceptance half of the
// stream-anthropic plan's tool-input rule: a tool_use block whose
// content_block_start declares an explicit empty input object, and which
// receives no input_json_delta at all, is a genuine zero-argument call. That
// is a real answer, distinct from a block that never says anything about its
// arguments (see TestAnthropicStreamMalformed/no_start_info_and_no_deltas,
// which the accumulator must reject) — both reach content_block_stop with an
// empty accumulated inputRaw, so this test is the only regression pinning
// hasStartInput as the signal that tells them apart.
func TestAnthropicStreamExplicitEmptyToolInput(t *testing.T) {
	events := []struct{ kind, data string }{
		{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"ping","input":{}}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":3}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeIdentitySSE(t, w, events)
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("fixture"),
			option.WithBaseURL(server.URL),
			option.WithMaxRetries(0),
		),
		model: "fixture",
	}

	eventChan, err := client.CreateMessageStream(context.Background(), &Request{
		Messages: []Message{NewUserMessage("go")},
	})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}

	var final *Response
	for event := range eventChan {
		if event.Type == EventError {
			t.Fatalf("unexpected error event: %v", event.Error)
		}
		if event.Type == EventMessageStop {
			final = event.Response
		}
	}

	if final == nil {
		t.Fatal("expected a final response from message_stop")
	}
	if len(final.Content) != 1 {
		t.Fatalf("expected exactly one content block, got %+v", final.Content)
	}
	input := final.Content[0].Input
	if input == nil || len(input) != 0 {
		t.Errorf("expected a non-nil empty input map, got %+v", input)
	}
}

// TestAnthropicStreamMalformed exercises the accumulator's protocol-violation
// detection through the real streaming path: structural sequencing errors,
// tool-input JSON mux cannot safely execute, and an unrecognized delta
// variant all must surface as a StreamProtocolError rather than a silently
// wrong final response.
func TestAnthropicStreamMalformed(t *testing.T) {
	cases := []struct {
		name   string
		events []struct{ kind, data string }
		// wantMessageStop is true only for a fixture whose FIRST message_stop
		// is itself fully valid (a complete block, properly started and
		// stopped) and the violation is a second, later terminal event. Per
		// the plan's state machine, a stream emits its final response once
		// and treats anything after that terminal as a protocol error — so
		// that first, valid message_stop is expected to succeed even though
		// the stream as a whole is malformed.
		wantMessageStop bool
		// wantReason pins the case to its specific StreamProtocolError.Reason
		// constant. Without this, a case only proves some error fired: the
		// post-loop "stream ended early" fallback fires for almost any
		// truncated fixture, so a broken specific check can go undetected
		// while the case still passes for the wrong reason.
		wantReason string
		// wantBlockID is asserted whenever the reason is block-scoped. Left
		// at its zero value "" for the message-level reasons, which never
		// set BlockID.
		wantBlockID string
	}{
		{
			name: "delta for unstarted block",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
			},
			wantReason:  reasonDeltaUnstartedBlock,
			wantBlockID: "anthropic:0",
		},
		{
			name: "stop for unstarted block",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			},
			wantReason:  reasonStopUnstartedBlock,
			wantBlockID: "anthropic:0",
		},
		{
			name: "repeated block start",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			},
			wantReason:  reasonDuplicateBlockStart,
			wantBlockID: "anthropic:0",
		},
		{
			name: "repeated block stop",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			},
			wantReason:  reasonDuplicateBlockStop,
			wantBlockID: "anthropic:0",
		},
		{
			name: "delta after block stop",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
			},
			wantReason:  reasonDeltaAfterStop,
			wantBlockID: "anthropic:0",
		},
		{
			name: "unrecognized delta variant",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{}}}`},
			},
			wantReason:  reasonUnknownDeltaVariant,
			wantBlockID: "anthropic:0",
		},
		{
			name: "message_stop without message_start",
			events: []struct{ kind, data string }{
				{"message_stop", `{"type":"message_stop"}`},
			},
			wantReason: reasonMessageStopNoStart,
		},
		{
			// Two blocks stopped, two left open: the lowest UNFINISHED index
			// (2) must be reported, not merely the lowest of all indexes.
			// Regression coverage for reporting this deterministically
			// regardless of Go's randomized map iteration order lives in
			// TestAnthropicStreamMessageStopUnfinishedBlockIsDeterministic,
			// which repeats this exact shape many times in one process.
			name: "message_stop with unfinished block",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
				{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":1}`},
				{"content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`},
				{"content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"text","text":""}}`},
				{"message_stop", `{"type":"message_stop"}`},
			},
			wantReason:  reasonMessageStopUnfinished,
			wantBlockID: "anthropic:2",
		},
		{
			name: "duplicate message_stop",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
				{"message_stop", `{"type":"message_stop"}`},
				{"message_stop", `{"type":"message_stop"}`},
			},
			wantMessageStop: true,
			wantReason:      reasonEventAfterTerminal,
			wantBlockID:     "anthropic:0",
		},
		{
			name: "eof before block stop",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
			},
			wantReason: reasonStreamEndedEarly,
		},
		{
			name: "eof before message_stop with block stopped",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			},
			wantReason: reasonStreamEndedEarly,
		},
		{
			name: "partial tool input object",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"get_weather"}}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{"}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			},
			wantReason:  reasonInvalidToolInput,
			wantBlockID: "anthropic:0",
		},
		{
			name: "null tool input object",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"get_weather"}}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"null"}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			},
			wantReason:  reasonInvalidToolInput,
			wantBlockID: "anthropic:0",
		},
		{
			name: "array tool input object",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"get_weather"}}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"[]"}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			},
			wantReason:  reasonInvalidToolInput,
			wantBlockID: "anthropic:0",
		},
		{
			name: "no start info and no deltas",
			events: []struct{ kind, data string }{
				{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"get_weather"}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			},
			wantReason:  reasonInvalidToolInput,
			wantBlockID: "anthropic:0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeIdentitySSE(t, w, tc.events)
			}))
			defer server.Close()

			client := &AnthropicClient{
				client: anthropic.NewClient(
					option.WithAPIKey("fixture"),
					option.WithBaseURL(server.URL),
					option.WithMaxRetries(0),
				),
				model: "fixture",
			}

			eventChan, err := client.CreateMessageStream(context.Background(), &Request{
				Messages: []Message{NewUserMessage("go")},
			})
			if err != nil {
				t.Fatalf("CreateMessageStream: %v", err)
			}

			var sawError bool
			var sawMessageStop bool
			var protocolErr *StreamProtocolError
			for event := range eventChan {
				if event.Type == EventError {
					sawError = true
					if !errors.As(event.Error, &protocolErr) {
						t.Errorf("error event carried non-protocol error: %v", event.Error)
					}
				}
				if event.Type == EventMessageStop {
					sawMessageStop = true
				}
			}

			if !sawError {
				t.Fatal("expected an error event, got none")
			}
			if sawMessageStop != tc.wantMessageStop {
				t.Errorf("sawMessageStop = %v, want %v", sawMessageStop, tc.wantMessageStop)
			}
			if protocolErr != nil {
				if protocolErr.Reason != tc.wantReason {
					t.Errorf("Reason = %q, want %q", protocolErr.Reason, tc.wantReason)
				}
				if protocolErr.BlockID != tc.wantBlockID {
					t.Errorf("BlockID = %q, want %q", protocolErr.BlockID, tc.wantBlockID)
				}
			}
		})
	}
}

// TestAnthropicStreamMessageStopUnfinishedBlockIsDeterministic repeats a
// message_stop with two unfinished blocks (indexes 2 and 3; 0 and 1 are
// properly stopped) many times in one process. The unfinished-block scan
// ranges over an accumulator map, and Go randomizes map iteration order per
// range: a single run can land on the correct lowest-unfinished-index answer
// by chance even when the scan does not sort first, so one green execution
// does not prove determinism. Before llm/anthropic.go sorted the indexes,
// an equivalent 4-block fixture reported anthropic:0/1/2/3 in a roughly
// 131/21/23/25 split across 200 runs instead of the correct, constant
// anthropic:2.
func TestAnthropicStreamMessageStopUnfinishedBlockIsDeterministic(t *testing.T) {
	events := []struct{ kind, data string }{
		{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"fixture","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":1}`},
		{"content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`},
		{"content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"text","text":""}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}

	const iterations = 50
	for i := 0; i < iterations; i++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeIdentitySSE(t, w, events)
		}))

		client := &AnthropicClient{
			client: anthropic.NewClient(
				option.WithAPIKey("fixture"),
				option.WithBaseURL(server.URL),
				option.WithMaxRetries(0),
			),
			model: "fixture",
		}

		eventChan, err := client.CreateMessageStream(context.Background(), &Request{
			Messages: []Message{NewUserMessage("go")},
		})
		if err != nil {
			server.Close()
			t.Fatalf("iteration %d: CreateMessageStream: %v", i, err)
		}

		var protocolErr *StreamProtocolError
		for event := range eventChan {
			if event.Type == EventError {
				errors.As(event.Error, &protocolErr)
			}
		}
		server.Close()

		if protocolErr == nil {
			t.Fatalf("iteration %d: expected a protocol error, got none", i)
		}
		if protocolErr.Reason != reasonMessageStopUnfinished {
			t.Fatalf("iteration %d: Reason = %q, want %q", i, protocolErr.Reason, reasonMessageStopUnfinished)
		}
		if protocolErr.BlockID != "anthropic:2" {
			t.Fatalf("iteration %d: BlockID = %q, want \"anthropic:2\" (lowest unfinished index)", i, protocolErr.BlockID)
		}
	}
}

// TestAnthropicStreamUnicodeTransportSplit proves a UTF-8 multi-byte
// character split across two raw transport writes, inside a single SSE data
// field, reassembles into one exact delta rather than two corrupted
// fragments or two fabricated model deltas.
func TestAnthropicStreamUnicodeTransportSplit(t *testing.T) {
	full := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"東京\"}}\n\n")
	// "東" is E6 9D B1 in UTF-8. Split after the first byte of that
	// sequence, inside the JSON string value, so neither write ends on a
	// character boundary.
	splitAt := indexUTF8Split(full)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("expected http.ResponseWriter to be an http.Flusher")
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		start := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"fixture\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		blockStart := []byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = w.Write(start)
		flusher.Flush()
		_, _ = w.Write(blockStart)
		flusher.Flush()

		_, _ = w.Write(full[:splitAt])
		flusher.Flush()
		_, _ = w.Write(full[splitAt:])
		flusher.Flush()

		tail := []byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		_, _ = w.Write(tail)
		flusher.Flush()
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("fixture"),
			option.WithBaseURL(server.URL),
			option.WithMaxRetries(0),
		),
		model: "fixture",
	}

	eventChan, err := client.CreateMessageStream(context.Background(), &Request{
		Messages: []Message{NewUserMessage("go")},
	})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}

	var texts []string
	var final *Response
	for event := range eventChan {
		if event.Type == EventError {
			t.Fatalf("unexpected error event: %v", event.Error)
		}
		if event.Type == EventContentDelta {
			texts = append(texts, event.Text)
		}
		if event.Type == EventMessageStop {
			final = event.Response
		}
	}

	if len(texts) != 1 || texts[0] != "東京" {
		t.Fatalf("expected exactly one delta with text 東京, got %+v", texts)
	}
	if final == nil || final.TextContent() != "東京" {
		t.Fatalf("expected final text 東京, got %+v", final)
	}
}

// indexUTF8Split returns a byte offset inside full that falls in the
// middle of a multi-byte UTF-8 sequence, so a caller can split a write there
// to simulate a transport chunk boundary landing mid-character.
func indexUTF8Split(full []byte) int {
	for i, b := range full {
		// A UTF-8 continuation byte (10xxxxxx) never starts a character;
		// splitting just before one guarantees the split lands mid-sequence.
		if b&0xC0 == 0x80 {
			return i
		}
	}
	panic("fixture has no multi-byte UTF-8 sequence to split")
}

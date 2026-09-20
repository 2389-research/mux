// ABOUTME: Tests for the shared Chat Completions stream accumulator that
// ABOUTME: Ollama and OpenRouter use to preserve interleaved text/tool fragments.
package llm

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/openai/openai-go/v3"
)

// TestChatStreamInterleaved drives the accumulator with the exact interleaved
// wire sequence from the stream-chat plan: text "東" arrives alongside the
// opening fragment of tool index 0, the next chunk interleaves tool index 1
// with the closing fragment of tool index 0, and a final finish_reason chunk
// closes both. It compares actual normalized adapter output, not a
// re-derivation of it.
func TestChatStreamInterleaved(t *testing.T) {
	chunks := []string{
		`{"id":"r1","choices":[{"index":0,"delta":{"role":"assistant","content":"東","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`,
		`{"id":"r1","choices":[{"index":0,"delta":{"content":"京","tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"read","arguments":"{\"path\":\"b\"}"}},{"index":0,"function":{"arguments":"\"a\"}"}}]}}]}`,
		`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
	}
	a := newChatStreamAccumulator("ollama")
	got := make([]StreamEvent, 0, len(chunks))
	for _, wire := range chunks {
		var chunk openai.ChatCompletionChunk
		if err := json.Unmarshal([]byte(wire), &chunk); err != nil {
			t.Fatal(err)
		}
		events, err := a.Add(chunk)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, events...)
	}
	resp, refs, err := a.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if resp.TextContent() != "東京" {
		t.Fatalf("text=%q", resp.TextContent())
	}
	uses := resp.ToolUses()
	if len(uses) != 2 || uses[0].ID != "c1" || uses[1].ID != "c2" {
		t.Fatalf("calls=%+v", uses)
	}
	if len(refs) != len(resp.Content) {
		t.Fatalf("refs=%+v", refs)
	}
	var args []string
	for _, event := range got {
		if event.DeltaKind == StreamDeltaToolInput {
			args = append(args, event.BlockID+"="+event.Text)
		}
	}
	want := []string{`chat:0:tool:0={"path":`, `chat:0:tool:1={"path":"b"}`, `chat:0:tool:0="a"}`}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%q", args)
	}

	// The interleaved fragments must have been reassembled into complete
	// argument objects, keyed by the native tool index rather than by a map
	// iteration order.
	if uses[0].Name != "read" || uses[1].Name != "read" {
		t.Fatalf("names=%q,%q", uses[0].Name, uses[1].Name)
	}
	if uses[0].Input["path"] != "a" || uses[1].Input["path"] != "b" {
		t.Fatalf("inputs=%+v", uses)
	}
	wantRefs := []StreamBlockRef{
		{BlockID: "chat:0:text", ContentIndex: 0},
		{BlockID: "chat:0:tool:0", ContentIndex: 1},
		{BlockID: "chat:0:tool:1", ContentIndex: 2},
	}
	if !reflect.DeepEqual(refs, wantRefs) {
		t.Fatalf("refs=%+v want %+v", refs, wantRefs)
	}
	if resp.StopReason != StopReasonToolUse {
		t.Fatalf("stop reason=%q", resp.StopReason)
	}
	if resp.ID != "r1" {
		t.Fatalf("id=%q", resp.ID)
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}

// feedChatStream unmarshals each wire string and feeds it to the accumulator,
// returning every emitted event and the first error from Add or Finish.
func feedChatStream(t *testing.T, a *chatStreamAccumulator, wires []string) ([]StreamEvent, *Response, error) {
	t.Helper()
	got := make([]StreamEvent, 0, len(wires))
	for _, wire := range wires {
		var chunk openai.ChatCompletionChunk
		if err := json.Unmarshal([]byte(wire), &chunk); err != nil {
			t.Fatalf("fixture chunk is not valid JSON: %v", err)
		}
		events, err := a.Add(chunk)
		got = append(got, events...)
		if err != nil {
			return got, nil, err
		}
	}
	resp, _, err := a.Finish()
	return got, resp, err
}

// TestChatStreamMalformed is the plan's failure matrix: every case must fail
// with a sanitized *StreamProtocolError and must not yield a successful final
// response, so a truncated or contradictory stream can never be executed as a
// complete turn.
func TestChatStreamMalformed(t *testing.T) {
	cases := []struct {
		name  string
		wires []string
	}{
		{
			name: "null arguments",
			wires: []string{
				`{"id":"r1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"null"}}]}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			},
		},
		{
			name: "two tool calls claiming one ID",
			wires: []string{
				`{"id":"r1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			},
		},
		{
			name: "tool call missing ID and name",
			wires: []string{
				`{"id":"r1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"a\"}"}}]}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			},
		},
		{
			name: "no finish reason",
			wires: []string{
				`{"id":"r1","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
			},
		},
		{
			name: "content after finish",
			wires: []string{
				`{"id":"r1","choices":[{"index":0,"delta":{"content":"done"}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{"content":"more"}}]}`,
			},
		},
		{
			name: "second finish reason",
			wires: []string{
				`{"id":"r1","choices":[{"index":0,"delta":{"content":"done"}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			},
		},
		{
			name: "unselected choice index",
			wires: []string{
				`{"id":"r1","choices":[{"index":0,"delta":{"content":"done"}}]}`,
				`{"id":"r1","choices":[{"index":1,"delta":{"content":"other"}}]}`,
			},
		},
		{
			name: "conflicting tool ID for one index",
			wires: []string{
				`{"id":"r1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c2","function":{"arguments":""}}]}}]}`,
				`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newChatStreamAccumulator("ollama")
			_, resp, err := feedChatStream(t, a, tc.wires)
			if err == nil {
				t.Fatalf("expected protocol failure, got success: resp=%+v", resp)
			}
			if resp != nil {
				t.Fatalf("failure produced a successful final response: %+v", resp)
			}
			var protocol *StreamProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("expected *StreamProtocolError, got %T: %v", err, err)
			}
			if protocol.Provider != "ollama" {
				t.Errorf("provider = %q, want ollama", protocol.Provider)
			}
			if protocol.Reason == "" {
				t.Error("protocol error carried no fixed reason")
			}
		})
	}
}

// TestChatStreamMalformedReasonsAreSanitized pins that no protocol failure
// embeds accumulated tool arguments in its message: the reasons are fixed
// strings, so a failure can be reported without leaking provider payload.
func TestChatStreamMalformedReasonsAreSanitized(t *testing.T) {
	a := newChatStreamAccumulator("openrouter")
	_, _, err := feedChatStream(t, a, []string{
		`{"id":"r1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"secret\":\"hunter2\""}}]}}]}`,
		`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})
	var protocol *StreamProtocolError
	if !errors.As(err, &protocol) {
		t.Fatalf("expected *StreamProtocolError, got %T: %v", err, err)
	}
	if got := protocol.Error(); got != "openrouter stream protocol: invalid tool input object (chat:0:tool:0)" {
		t.Fatalf("error text = %q", got)
	}
}

// ABOUTME: Tests for internal/llmcopy's owned-copy guarantees: nested
// ABOUTME: containers, media/replay bytes, nil-vs-empty, and cycle handling.
package llmcopy_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/2389-research/mux/internal/llmcopy"
	"github.com/2389-research/mux/llm"
)

// TestCloneBlockNestedInputIsOwned is the kata's own sample: a nested
// map/slice tool Input plus a Replay envelope, mutated after cloning to
// prove neither the nested value nor the replay bytes alias the original.
func TestCloneBlockNestedInputIsOwned(t *testing.T) {
	block := llm.ContentBlock{
		Type: llm.ContentTypeToolUse, ID: "call-a", Name: "effect",
		Input:  map[string]any{"rows": []any{map[string]any{"n": json.Number("9007199254740993")}}},
		Replay: &llm.ProviderReplay{Provider: "openai", Model: "model", Data: json.RawMessage(`{"type":"function_call","call_id":"call-a"}`)},
	}
	cloned := llmcopy.CloneBlock(block)
	cloned.Input["rows"].([]any)[0].(map[string]any)["n"] = json.Number("2")
	cloned.Replay.Data[2] = 'X'
	if block.Input["rows"].([]any)[0].(map[string]any)["n"] != json.Number("9007199254740993") {
		t.Fatal("nested alias")
	}
	if bytes.Equal(cloned.Replay.Data, block.Replay.Data) {
		t.Fatal("replay alias")
	}
}

// TestCloneBlockSourceBytesAreOwned confirms Source is cloned to a new
// *MediaSource whose Bytes is a separate backing array.
func TestCloneBlockSourceBytesAreOwned(t *testing.T) {
	block := llm.ContentBlock{
		Type:      llm.ContentTypeImage,
		MediaType: "image/png",
		Source:    &llm.MediaSource{Kind: llm.SourceKindBytes, Bytes: []byte{1, 2, 3}},
	}
	cloned := llmcopy.CloneBlock(block)
	if cloned.Source == block.Source {
		t.Fatal("cloned block shares the original *MediaSource")
	}
	cloned.Source.Bytes[0] = 0xFF
	if block.Source.Bytes[0] != 1 {
		t.Fatalf("original Source.Bytes mutated through the clone: got %v", block.Source.Bytes)
	}
}

// TestCloneBlockReplayDataIsOwned isolates Replay ownership: a new
// *ProviderReplay struct, and Data backed by a new byte slice.
func TestCloneBlockReplayDataIsOwned(t *testing.T) {
	block := llm.ContentBlock{
		Type:   llm.ContentTypeToolUse,
		Replay: &llm.ProviderReplay{Provider: "openai", Model: "model", Data: json.RawMessage(`{"type":"x"}`)},
	}
	cloned := llmcopy.CloneBlock(block)
	if cloned.Replay == block.Replay {
		t.Fatal("cloned block shares the original *ProviderReplay")
	}
	cloned.Replay.Data[0] = 'X'
	if bytes.Equal(cloned.Replay.Data, block.Replay.Data) {
		t.Fatal("Replay.Data still aliases the original after mutation")
	}
	if string(block.Replay.Data) != `{"type":"x"}` {
		t.Fatalf("original Replay.Data mutated through the clone: got %s", block.Replay.Data)
	}
}

// TestCloneBlockNilVsEmptySlice confirms a nil []any inside Input clones to
// nil, and a non-nil empty []any clones to a non-nil empty slice.
func TestCloneBlockNilVsEmptySlice(t *testing.T) {
	block := llm.ContentBlock{
		Type: llm.ContentTypeToolUse,
		Input: map[string]any{
			"nilSlice":   []any(nil),
			"emptySlice": []any{},
		},
	}
	cloned := llmcopy.CloneBlock(block)
	if s := cloned.Input["nilSlice"].([]any); s != nil {
		t.Fatalf("nil slice became non-nil after clone: %#v", s)
	}
	if s := cloned.Input["emptySlice"].([]any); s == nil {
		t.Fatal("non-nil empty slice became nil after clone")
	}
}

// TestCloneMessageNilVsEmptyBlocks confirms the same nil-vs-empty rule
// holds one level up, for Message.Blocks.
func TestCloneMessageNilVsEmptyBlocks(t *testing.T) {
	nilBlocks := llm.Message{Role: llm.RoleUser, Content: "hi"}
	if got := llmcopy.CloneMessage(nilBlocks).Blocks; got != nil {
		t.Fatalf("nil Blocks became non-nil after clone: %#v", got)
	}

	emptyBlocks := llm.Message{Role: llm.RoleUser, Blocks: []llm.ContentBlock{}}
	if got := llmcopy.CloneMessage(emptyBlocks).Blocks; got == nil {
		t.Fatal("non-nil empty Blocks became nil after clone")
	}
}

// TestCloneMessagesNilVsEmpty confirms the same nil-vs-empty rule at the
// top level: CloneMessages(nil) is nil, CloneMessages of a non-nil empty
// slice is a non-nil empty slice.
func TestCloneMessagesNilVsEmpty(t *testing.T) {
	if got := llmcopy.CloneMessages(nil); got != nil {
		t.Fatalf("nil messages became non-nil after clone: %#v", got)
	}
	if got := llmcopy.CloneMessages([]llm.Message{}); got == nil {
		t.Fatal("non-nil empty messages became nil after clone")
	}
}

// TestCloneBlockJSONNumberPreserved confirms json.Number survives cloning
// exactly, including a value outside float64's exact integer range
// (2^53), which a marshal/unmarshal round-trip would corrupt.
func TestCloneBlockJSONNumberPreserved(t *testing.T) {
	const want = json.Number("9007199254740993")
	block := llm.ContentBlock{
		Type:  llm.ContentTypeToolUse,
		Input: map[string]any{"n": want},
	}
	cloned := llmcopy.CloneBlock(block)
	got, ok := cloned.Input["n"].(json.Number)
	if !ok {
		t.Fatalf("cloned value is %T, want json.Number", cloned.Input["n"])
	}
	if got != want {
		t.Fatalf("json.Number = %s, want %s", got, want)
	}
}

// TestCloneBlockSelfReferentialContainerTerminates confirms the seen map
// breaks a map-into-itself cycle instead of recursing until the goroutine
// stack overflows, and that the resulting clone is still cyclic enough
// that json.Marshal rejects it (proving the cycle was preserved in the
// clone, not silently broken by dropping the self-reference).
func TestCloneBlockSelfReferentialContainerTerminates(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	block := llm.ContentBlock{
		Type:  llm.ContentTypeToolUse,
		Input: map[string]any{"cycle": cyclic},
	}

	// This call must return. A broken seen-map recurses until the test
	// binary's goroutine stack overflows, which crashes the process rather
	// than failing the test.
	cloned := llmcopy.CloneBlock(block)

	if _, err := json.Marshal(cloned.Input); err == nil {
		t.Fatal("expected json.Marshal to reject the cloned self-referential container, got nil error")
	}
}

// TestCloneBlockUnsupportedValuesPassThrough confirms a value cloneValue
// cannot walk (here, a channel — the same sentinel agent/transcript_test.go
// uses to force an encoding failure) survives the clone unchanged rather
// than being silently dropped or zeroed, so the durable serialization
// boundary can still reject it.
func TestCloneBlockUnsupportedValuesPassThrough(t *testing.T) {
	ch := make(chan int)
	block := llm.ContentBlock{
		Type:  llm.ContentTypeToolUse,
		Input: map[string]any{"unsupported": ch},
	}
	cloned := llmcopy.CloneBlock(block)
	got, ok := cloned.Input["unsupported"].(chan int)
	if !ok || got != ch {
		t.Fatalf("channel value was not passed through unchanged: %#v", cloned.Input["unsupported"])
	}
	if _, err := json.Marshal(cloned.Input); err == nil {
		t.Fatal("expected json.Marshal to reject the channel value, got nil error")
	}
}

// TestCloneMessagesIndependentFromOriginal confirms CloneMessages clones
// every message and every block in it, not just the outer slice.
func TestCloneMessagesIndependentFromOriginal(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Blocks: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ID: "call-1", Input: map[string]any{"x": 1}},
		}},
	}
	cloned := llmcopy.CloneMessages(messages)

	cloned[1].Blocks[0].Input["x"] = 2
	if messages[1].Blocks[0].Input["x"] != 1 {
		t.Fatalf("CloneMessages shares an Input map with the original: got %v", messages[1].Blocks[0].Input["x"])
	}

	cloned[1].Blocks[0].ID = "changed"
	if messages[1].Blocks[0].ID != "call-1" {
		t.Fatalf("CloneMessages shares a ContentBlock with the original: got %q", messages[1].Blocks[0].ID)
	}
}

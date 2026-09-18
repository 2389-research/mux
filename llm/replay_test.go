// ABOUTME: Tests for the provider replay envelope: JSON byte-equality of
// Replay.Data, validateReplay preflight identity/payload checks, and
// byte-safe copying when cloning responses.
package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func replayTestBlock(provider, model, data string) ContentBlock {
	return ContentBlock{
		Type: ContentTypeReplay,
		Replay: &ProviderReplay{
			Provider: provider,
			Model:    model,
			Data:     json.RawMessage(data),
		},
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything written to it. No other test in this package captures stderr,
// so this stays a local helper rather than a shared one.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	captured, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}
	return string(captured)
}

func TestProviderReplay_JSONByteEquality(t *testing.T) {
	block := ContentBlock{Type: ContentTypeReplay, Replay: &ProviderReplay{Provider: "openai", Model: "model", Data: json.RawMessage(`{"type":"reasoning","id":"rs1","summary":[],"encrypted_content":"opaque"}`)}}
	raw, err := json.Marshal(Message{Role: RoleAssistant, Blocks: []ContentBlock{block}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded Message
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Blocks[0].Replay.Data, block.Replay.Data) {
		t.Fatal("replay bytes changed")
	}
}

func TestValidateReplay_MismatchedProvider(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
		replayTestBlock("anthropic", "claude-sonnet-4-20250514", `{"type":"reasoning","id":"rs1"}`),
	}}}

	var result []Message
	var err error
	warning := captureStderr(t, func() {
		result, err = validateReplay("openai", "gpt-5", messages)
	})
	if err != nil {
		t.Fatalf("mismatch must warn and drop, not error: %v", err)
	}
	if result[0].Blocks[0].Replay != nil {
		t.Fatalf("expected the mismatched block's replay envelope dropped, got %+v", result[0].Blocks[0].Replay)
	}
	if messages[0].Blocks[0].Replay == nil {
		t.Fatal("validateReplay must not mutate the caller's original messages")
	}
	for _, identity := range []string{"openai", "gpt-5", "anthropic", "claude-sonnet-4-20250514"} {
		if !strings.Contains(warning, identity) {
			t.Errorf("warning %q must name identity %q", warning, identity)
		}
	}
}

func TestValidateReplay_MismatchedModel(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
		replayTestBlock("anthropic", "claude-sonnet-4-20250514", `{"type":"reasoning","id":"rs1"}`),
	}}}

	var result []Message
	var err error
	warning := captureStderr(t, func() {
		result, err = validateReplay("anthropic", "claude-opus-4-1-20250805", messages)
	})
	if err != nil {
		t.Fatalf("mismatch must warn and drop, not error: %v", err)
	}
	if result[0].Blocks[0].Replay != nil {
		t.Fatalf("expected the mismatched block's replay envelope dropped, got %+v", result[0].Blocks[0].Replay)
	}
	if messages[0].Blocks[0].Replay == nil {
		t.Fatal("validateReplay must not mutate the caller's original messages")
	}
	for _, identity := range []string{"claude-opus-4-1-20250805", "claude-sonnet-4-20250514"} {
		if !strings.Contains(warning, identity) {
			t.Errorf("warning %q must name identity %q", warning, identity)
		}
	}
}

// TestValidateReplay_MismatchLeavesOtherMessagesAliased confirms the
// copy-on-write contract: dropping a block clones only the message that
// held it. A sibling message with nothing to drop keeps its original
// Blocks backing array — proven here by mutating through the result and
// observing the mutation land in the original, which a real clone would
// not show. The touched message's original is proven independent instead:
// its Replay pointer must survive the drop applied to the returned copy.
func TestValidateReplay_MismatchLeavesOtherMessagesAliased(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Blocks: []ContentBlock{{Type: ContentTypeText, Text: "unrelated"}}},
		{Role: RoleAssistant, Blocks: []ContentBlock{
			replayTestBlock("anthropic", "claude-sonnet-4-20250514", `{"type":"reasoning","id":"rs1"}`),
		}},
	}

	var result []Message
	captureStderr(t, func() {
		var err error
		result, err = validateReplay("openai", "gpt-5", messages)
		if err != nil {
			t.Fatalf("mismatch must warn and drop, not error: %v", err)
		}
	})

	result[0].Blocks[0].Text = "mutated through the result"
	if messages[0].Blocks[0].Text != "mutated through the result" {
		t.Errorf("untouched message must stay aliased to the original, got %q", messages[0].Blocks[0].Text)
	}
	if messages[1].Blocks[0].Replay == nil {
		t.Fatal("dropping a block must not mutate the caller's original message")
	}
}

func TestValidateReplay_MatchingIdentityPasses(t *testing.T) {
	messages := []Message{
		{Role: RoleAssistant, Blocks: []ContentBlock{
			replayTestBlock("openai", "gpt-5", `{"type":"reasoning","id":"rs1","summary":[]}`),
			{Type: ContentTypeText, Text: "visible answer"},
		}},
		{Role: RoleUser, Blocks: []ContentBlock{{Type: ContentTypeToolResult, ToolUseID: "call_1"}}},
	}

	if _, err := validateReplay("openai", "gpt-5", messages); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateReplay_EmptyData(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
		replayTestBlock("gemini", "gemini-2.5-pro", ""),
	}}}

	_, err := validateReplay("gemini", "gemini-2.5-pro", messages)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "gemini") && strings.Contains(err.Error(), "gemini-2.5-pro") && strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("empty data must not report an identity mismatch: %v", err)
	}
	if !strings.Contains(err.Error(), "replay.data") {
		t.Errorf("error must name the replay.data field, got %q", err.Error())
	}
}

func TestValidateReplay_InvalidJSON(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
		replayTestBlock("openai", "gpt-5", `{"type":"reasoning",`),
	}}}

	_, err := validateReplay("openai", "gpt-5", messages)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "replay.data") {
		t.Errorf("error must name the replay.data field, got %q", err.Error())
	}
}

func TestValidateReplay_UnsupportedPayload(t *testing.T) {
	cases := map[string]string{
		"empty object":    `{}`,
		"empty type":      `{"type":""}`,
		"non-object":      `["reasoning"]`,
		"non-string type": `{"type":42}`,
		"null":            `null`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
				replayTestBlock("ollama", "qwen3", data),
			}}}
			_, err := validateReplay("ollama", "qwen3", messages)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "replay.data") {
				t.Errorf("error must name the replay.data field, got %q", err.Error())
			}
		})
	}
}

func TestValidateReplay_NilReplayIgnored(t *testing.T) {
	messages := []Message{
		{Role: RoleAssistant, Blocks: []ContentBlock{{Type: ContentTypeText, Text: "no replay here"}}},
		{Role: RoleUser, Content: "plain content"},
	}

	if _, err := validateReplay("openai", "gpt-5", messages); err != nil {
		t.Fatalf("blocks without replay must pass, got %v", err)
	}
}

func TestValidateReplay_ReplayBlockWithoutPayload(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
		{Type: ContentTypeReplay},
	}}}

	_, err := validateReplay("openai", "gpt-5", messages)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "replay") {
		t.Errorf("error must name the replay field, got %q", err.Error())
	}
}

func TestCloneResponse_ReplayAliasIsolation(t *testing.T) {
	original := &Response{
		Content: []ContentBlock{
			replayTestBlock("openai", "gpt-5", `{"type":"reasoning","id":"rs1","encrypted_content":"opaque"}`),
			{Type: ContentTypeText, Text: "answer"},
		},
	}

	clone := cloneResponse(original)
	if clone == original {
		t.Fatal("cloneResponse must return a new Response")
	}
	if len(clone.Content) != len(original.Content) {
		t.Fatalf("clone Content length: got %d, want %d", len(clone.Content), len(original.Content))
	}
	if clone.Content[0].Replay == original.Content[0].Replay {
		t.Fatal("clone must not share the Replay pointer with the original")
	}

	clone.Content[0].Replay.Data[0] = 'X'
	if !bytes.Equal(original.Content[0].Replay.Data, []byte(`{"type":"reasoning","id":"rs1","encrypted_content":"opaque"}`)) {
		t.Errorf("mutating the clone changed the original replay data: %s", original.Content[0].Replay.Data)
	}

	original.Content[0].Replay.Data[1] = 'Y'
	if !bytes.Equal(clone.Content[0].Replay.Data, []byte(`X"type":"reasoning","id":"rs1","encrypted_content":"opaque"}`)) {
		t.Errorf("mutating the original changed the clone replay data: %s", clone.Content[0].Replay.Data)
	}
}

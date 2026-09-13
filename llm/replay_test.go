// ABOUTME: Tests for the provider replay envelope: JSON byte-equality of
// Replay.Data, validateReplay preflight identity/payload checks, and
// byte-safe copying when cloning responses.
package llm

import (
	"bytes"
	"encoding/json"
	"errors"
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

	err := validateReplay("openai", "gpt-5", messages)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var mismatch *ErrReplayMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected *ErrReplayMismatch, got %T: %v", err, err)
	}
	if mismatch.Provider != "openai" || mismatch.Model != "gpt-5" {
		t.Errorf("request identity: got %q/%q, want openai/gpt-5", mismatch.Provider, mismatch.Model)
	}
	if mismatch.ReplayProvider != "anthropic" || mismatch.ReplayModel != "claude-sonnet-4-20250514" {
		t.Errorf("replay identity: got %q/%q, want anthropic/claude-sonnet-4-20250514", mismatch.ReplayProvider, mismatch.ReplayModel)
	}
	for _, identity := range []string{"openai", "anthropic"} {
		if !strings.Contains(mismatch.Error(), identity) {
			t.Errorf("Error() %q must name identity %q", mismatch.Error(), identity)
		}
	}
}

func TestValidateReplay_MismatchedModel(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
		replayTestBlock("anthropic", "claude-sonnet-4-20250514", `{"type":"reasoning","id":"rs1"}`),
	}}}

	err := validateReplay("anthropic", "claude-opus-4-1-20250805", messages)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var mismatch *ErrReplayMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected *ErrReplayMismatch, got %T: %v", err, err)
	}
	if mismatch.Provider != "anthropic" || mismatch.Model != "claude-opus-4-1-20250805" {
		t.Errorf("request identity: got %q/%q, want anthropic/claude-opus-4-1-20250805", mismatch.Provider, mismatch.Model)
	}
	if mismatch.ReplayProvider != "anthropic" || mismatch.ReplayModel != "claude-sonnet-4-20250514" {
		t.Errorf("replay identity: got %q/%q, want anthropic/claude-sonnet-4-20250514", mismatch.ReplayProvider, mismatch.ReplayModel)
	}
	for _, identity := range []string{"claude-opus-4-1-20250805", "claude-sonnet-4-20250514"} {
		if !strings.Contains(mismatch.Error(), identity) {
			t.Errorf("Error() %q must name identity %q", mismatch.Error(), identity)
		}
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

	if err := validateReplay("openai", "gpt-5", messages); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateReplay_EmptyData(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
		replayTestBlock("gemini", "gemini-2.5-pro", ""),
	}}}

	err := validateReplay("gemini", "gemini-2.5-pro", messages)
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

	err := validateReplay("openai", "gpt-5", messages)
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
			err := validateReplay("ollama", "qwen3", messages)
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

	if err := validateReplay("openai", "gpt-5", messages); err != nil {
		t.Fatalf("blocks without replay must pass, got %v", err)
	}
}

func TestValidateReplay_ReplayBlockWithoutPayload(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
		{Type: ContentTypeReplay},
	}}}

	err := validateReplay("openai", "gpt-5", messages)
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

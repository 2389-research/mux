// ABOUTME: Tests for the provider replay envelope: JSON byte-equality of
// Replay.Data, validateReplay preflight identity/payload checks, and
// byte-safe copying when cloning responses.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
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
	if len(result[0].Blocks) != 0 {
		t.Fatalf("a replay-only block must be dropped entirely, got %+v", result[0].Blocks)
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
	if len(result[0].Blocks) != 0 {
		t.Fatalf("a replay-only block must be dropped entirely, got %+v", result[0].Blocks)
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

// TestValidateReplay_ResultIsValidInput proves the drop result is safe to
// feed back into validateReplay. A dropped block keeps its Type set, and a
// caller that re-validates (or the package's own RetryClient, which re-sends
// the same *Request) must not see a structural failure invented by the drop.
func TestValidateReplay_ResultIsValidInput(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
		replayTestBlock("anthropic", "claude-sonnet-4-20250514", `{"type":"thinking","thinking":"t","signature":"s"}`),
		{Type: ContentTypeToolUse, ID: "toolu_1", Name: "read_file", Input: map[string]any{"path": "/a"}},
	}}}

	var dropped []Message
	captureStderr(t, func() {
		var err error
		dropped, err = validateReplay("anthropic", "claude-haiku-4-5", messages)
		if err != nil {
			t.Fatalf("mismatch must warn and drop, not error: %v", err)
		}
	})

	// Second pass over the first pass's own output, as any adapter re-entry
	// does (RetryClient re-uses the same *Request).
	var twice []Message
	captureStderr(t, func() {
		var err error
		twice, err = validateReplay("anthropic", "claude-haiku-4-5", dropped)
		if err != nil {
			t.Fatalf("validateReplay's own output must be valid input to it: %v", err)
		}
	})

	// The block that lost its only payload must be gone, not left as an
	// empty shell that the structural check rejects.
	for _, block := range twice[0].Blocks {
		if block.Type == ContentTypeReplay && block.Replay == nil {
			t.Fatalf("dropped block survived as a payload-less replay block: %+v", twice[0].Blocks)
		}
		if block.Replay != nil {
			t.Errorf("no envelope should survive a mismatch, got %+v", block.Replay)
		}
	}
	// The tool call has a normalized fallback and must still be there.
	var sawTool bool
	for _, block := range twice[0].Blocks {
		if block.Type == ContentTypeToolUse && block.ID == "toolu_1" {
			sawTool = true
		}
	}
	if !sawTool {
		t.Errorf("the droppable tool call must survive the drop: %+v", twice[0].Blocks)
	}
}

// replayReuseAnthropicServer answers every request with a valid message body.
func replayReuseAnthropicServer(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(server.Close)
	return server, func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}
}

// TestAnthropicClient_SameRequestIsResendable pins the Client contract that
// dropping a mismatched envelope must not make: sending one *Request twice
// works. This is exactly what the package's own RetryClient does on a
// retryable 429/500/502/503/504 — it re-invokes the inner client with the
// same *Request.
func TestAnthropicClient_SameRequestIsResendable(t *testing.T) {
	server, requests := replayReuseAnthropicServer(t)
	client := &AnthropicClient{
		client: anthropic.NewClient(anthropicoption.WithAPIKey("test-key"), anthropicoption.WithBaseURL(server.URL), anthropicoption.WithMaxRetries(0)),
		model:  "claude-haiku-4-5",
	}
	req := &Request{
		Messages: []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
			replayTestBlock("anthropic", "claude-sonnet-4-20250514", `{"type":"thinking","thinking":"t","signature":"s"}`),
			{Type: ContentTypeToolUse, ID: "toolu_1", Name: "read_file", Input: map[string]any{"path": "/a"}},
		}}},
	}

	for attempt := 1; attempt <= 2; attempt++ {
		captureStderr(t, func() {
			if _, err := client.CreateMessage(context.Background(), req); err != nil {
				t.Fatalf("call %d with the same Request: %v", attempt, err)
			}
		})
	}
	if got := requests(); got != 2 {
		t.Fatalf("expected both calls to reach the wire, got %d requests", got)
	}
	// The caller's Request must not be left self-invalidating, and the
	// adapter must not destroy the caller's own envelopes either: base never
	// mutated req.Messages, and a caller that keeps a Request around (or a
	// RetryClient re-sending it) must not lose data it still owns.
	if _, err := validateReplay("anthropic", "claude-haiku-4-5", req.Messages); err != nil {
		t.Errorf("caller's Request was poisoned by the drop: %v", err)
	}
	if len(req.Messages[0].Blocks) != 2 {
		t.Fatalf("adapter rewrote the caller's blocks: got %+v", req.Messages[0].Blocks)
	}
	if req.Messages[0].Blocks[0].Replay == nil {
		t.Error("adapter destroyed the caller's replay envelope")
	}
}

// TestOpenAIClient_SameRequestIsResendable is the OpenAI analogue: the shared
// preflight is per-adapter, so both providers need the guarantee.
func TestOpenAIClient_SameRequestIsResendable(t *testing.T) {
	var mu sync.Mutex
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","created_at":0,"model":"gpt-5.2","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}]}`))
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(openaioption.WithAPIKey("test-key"), openaioption.WithBaseURL(server.URL), openaioption.WithMaxRetries(0)),
		model:  "gpt-5.2",
	}
	req := &Request{
		Messages: []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
			replayTestBlock("openai", "gpt-5.1", `{"type":"reasoning","id":"rs1","encrypted_content":"opaque"}`),
			{Type: ContentTypeText, Text: "visible"},
		}}},
	}

	for attempt := 1; attempt <= 2; attempt++ {
		captureStderr(t, func() {
			if _, err := client.CreateMessage(context.Background(), req); err != nil {
				t.Fatalf("call %d with the same Request: %v", attempt, err)
			}
		})
	}
	mu.Lock()
	total := requests
	mu.Unlock()
	if total != 2 {
		t.Fatalf("expected both calls to reach the wire, got %d requests", total)
	}
	if len(req.Messages[0].Blocks) != 2 || req.Messages[0].Blocks[0].Replay == nil {
		t.Fatalf("adapter must not rewrite the caller's blocks: %+v", req.Messages[0].Blocks)
	}
}

// TestRetryClient_RetriesAfterReplayDrop is the end-to-end reachability
// case from the review: a history carrying a stale envelope gets a transient
// 500 from the provider, so RetryClient re-invokes the same adapter with the
// same *Request. That retry must succeed rather than die at preflight with a
// structural error the caller's history never contained.
func TestRetryClient_RetriesAfterReplayDrop(t *testing.T) {
	var mu sync.Mutex
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		turn := requests
		mu.Unlock()
		// First attempt is a transient failure; the retry succeeds.
		if turn == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"transient"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	adapter := &AnthropicClient{
		client: anthropic.NewClient(anthropicoption.WithAPIKey("test-key"), anthropicoption.WithBaseURL(server.URL), anthropicoption.WithMaxRetries(0)),
		model:  "claude-haiku-4-5",
	}
	req := &Request{
		Messages: []Message{{Role: RoleAssistant, Blocks: []ContentBlock{
			replayTestBlock("anthropic", "claude-sonnet-4-20250514", `{"type":"thinking","thinking":"t","signature":"s"}`),
			{Type: ContentTypeToolUse, ID: "toolu_1", Name: "read_file", Input: map[string]any{"path": "/a"}},
		}}},
	}

	client := NewRetryClient(adapter, &RetryConfig{MaxRetries: 2, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, Multiplier: 1})
	captureStderr(t, func() {
		if _, err := client.CreateMessage(context.Background(), req); err != nil {
			t.Fatalf("retry after a replay drop must succeed, got %v", err)
		}
	})

	mu.Lock()
	total := requests
	mu.Unlock()
	if total != 2 {
		t.Fatalf("expected the retry to reach the wire, got %d requests", total)
	}
}

// ABOUTME: Cross-provider acceptance test for the shared status policy.
// ABOUTME: Asserts all five providers return the identical shape for
// ABOUTME: token-limit truncation and content filtering (wepb, yyb7).

package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCrossProviderStatusPolicy is the acceptance centerpiece for the
// Response-everywhere policy: token-limit truncation and content filtering
// are successful Responses carrying the named StopReason with partial text
// preserved — identically across Anthropic, Gemini, Ollama, OpenRouter, and
// OpenAI, on the non-streaming call shape. (OpenAI stream parity is covered
// by TestOpenAIClient_StreamResponsesIncompleteEvent; the other providers'
// streams funnel through the same converters exercised here.)
func TestCrossProviderStatusPolicy(t *testing.T) {
	const partialText = "partial answer"

	tests := []struct {
		name string
		// newClient points a provider client at an httptest server that
		// answers every request with an HTTP-200 body.
		newClient func(t *testing.T, contentType, body string) Client
		// truncBody is the fixture that stops generation at the token limit.
		truncBody string
		// filterBody is the fixture that stops generation via content
		// filtering. Empty means the provider's pinned SDK has no stop
		// reason for filtering; the skip is documented on the row and the
		// provider's nearest analog is covered at converter level in
		// stop_reason_test.go.
		filterBody string
	}{
		{
			name: "anthropic",
			newClient: func(t *testing.T, contentType, body string) Client {
				return NewAnthropicClientWithBaseURL("test-key", "claude-sonnet-4-20250514", statusPolicyServerURL(t, contentType, body))
			},
			truncBody: `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","content":[{"type":"text","text":"partial answer"}],"stop_reason":"max_tokens","usage":{"input_tokens":10,"output_tokens":5}}`,
			// The Anthropic API has no content-filter stop reason; policy
			// refusals surface as stop_reason "refusal" -> StopReasonRefusal,
			// pinned at converter level in TestAnthropicStopReasons.
			filterBody: "",
		},
		{
			name: "gemini",
			newClient: func(t *testing.T, contentType, body string) Client {
				client, err := NewGeminiClientWithBaseURL(context.Background(), "test-key", "gemini-2.0-flash", statusPolicyServerURL(t, contentType, body))
				if err != nil {
					t.Fatalf("failed to create Gemini client: %v", err)
				}
				return client
			},
			truncBody:  `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial answer"}]},"finishReason":"MAX_TOKENS","index":0}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
			filterBody: `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial answer"}]},"finishReason":"SAFETY","index":0}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
		},
		{
			name: "ollama",
			newClient: func(t *testing.T, contentType, body string) Client {
				return NewOllamaClient(statusPolicyServerURL(t, contentType, body), "llama3.2")
			},
			truncBody:  `{"id":"chatcmpl-1","model":"llama3.2","choices":[{"index":0,"message":{"role":"assistant","content":"partial answer"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			filterBody: `{"id":"chatcmpl-1","model":"llama3.2","choices":[{"index":0,"message":{"role":"assistant","content":"partial answer"},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		},
		{
			name: "openrouter",
			newClient: func(t *testing.T, contentType, body string) Client {
				return NewOpenRouterClientWithBaseURL("test-key", "anthropic/claude-3.5-sonnet", statusPolicyServerURL(t, contentType, body))
			},
			truncBody:  `{"id":"chatcmpl-1","model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"message":{"role":"assistant","content":"partial answer"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
			filterBody: `{"id":"chatcmpl-1","model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"message":{"role":"assistant","content":"partial answer"},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		},
		{
			name: "openai",
			newClient: func(t *testing.T, contentType, body string) Client {
				return NewOpenAIClientWithBaseURL("test-key", "gpt-5.2", statusPolicyServerURL(t, contentType, body))
			},
			truncBody:  `{"id":"r1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial answer"}]}]}`,
			filterBody: `{"id":"r1","status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial answer"}]}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name+"/truncation", func(t *testing.T) {
			client := tc.newClient(t, "application/json", tc.truncBody)
			resp, err := client.CreateMessage(context.Background(), &Request{
				Messages: []Message{NewUserMessage("Hello")},
			})
			assertStatusPolicyResponse(t, resp, err, StopReasonMaxTokens, partialText)
		})

		t.Run(tc.name+"/content_filter", func(t *testing.T) {
			if tc.filterBody == "" {
				t.Skip("provider SDK has no content-filter stop reason; see row comment and stop_reason_test.go converter coverage")
			}
			client := tc.newClient(t, "application/json", tc.filterBody)
			resp, err := client.CreateMessage(context.Background(), &Request{
				Messages: []Message{NewUserMessage("Hello")},
			})
			assertStatusPolicyResponse(t, resp, err, StopReasonContentFilter, partialText)
		})
	}
}

// assertStatusPolicyResponse pins the single shape every provider must
// produce for truncation and filtering: a non-nil successful Response
// carrying the named StopReason with the partial text preserved — never an
// error.
func assertStatusPolicyResponse(t *testing.T, resp *Response, err error, wantStop StopReason, wantText string) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected successful %s response, got error: %v", wantStop, err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.StopReason != wantStop {
		t.Errorf("expected stop reason %q, got %q", wantStop, resp.StopReason)
	}
	if resp.TextContent() != wantText {
		t.Errorf("expected partial text %q, got %q", wantText, resp.TextContent())
	}
}

// statusPolicyServerURL starts an httptest server that answers every request
// with an HTTP-200 body and returns its URL. Request paths are ignored so
// one fixture serves any provider endpoint shape.
func statusPolicyServerURL(t *testing.T, contentType, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

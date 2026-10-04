// ABOUTME: Pins supported provider defaults for every public constructor variant.
// ABOUTME: Prevents empty model selections from returning retired model identifiers.
package llm

import "testing"

func TestProviderDefaultModels(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		get  func() string
	}{
		{"anthropic", "claude-sonnet-4-6", func() string { return NewAnthropicClient("test-key", "").model }},
		{"anthropic_base_url", "claude-sonnet-4-6", func() string {
			return NewAnthropicClientWithBaseURL("test-key", "", "http://localhost").model
		}},
		{"openrouter", "anthropic/claude-sonnet-4.6", func() string { return NewOpenRouterClient("test-key", "").model }},
		{"openrouter_headers", "anthropic/claude-sonnet-4.6", func() string {
			return NewOpenRouterClientWithHeaders("test-key", "", "", "").model
		}},
		{"openrouter_base_url", "anthropic/claude-sonnet-4.6", func() string {
			return NewOpenRouterClientWithBaseURL("test-key", "", "http://localhost").model
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.get(); got != tc.want {
				t.Fatalf("default model = %q, want supported successor %q", got, tc.want)
			}
		})
	}
}

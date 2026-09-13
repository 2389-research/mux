// ABOUTME: Table tests for the provider stop-reason mapping helpers.
// ABOUTME: Locks every native finish reason to its mux StopReason value.
package llm

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3/responses"
	"google.golang.org/genai"
)

func TestChatStopReasons(t *testing.T) {
	cases := []struct {
		name   string
		native string
		want   StopReason
	}{
		{name: "stop", native: "stop", want: StopReasonEndTurn},
		{name: "tool_calls", native: "tool_calls", want: StopReasonToolUse},
		{name: "function_call", native: "function_call", want: StopReasonToolUse},
		{name: "length", native: "length", want: StopReasonMaxTokens},
		{name: "content_filter", native: "content_filter", want: StopReasonContentFilter},
		{name: "empty", native: "", want: StopReasonOther},
		{name: "future", native: "future", want: StopReasonOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapChatStopReason(tc.native); got != tc.want {
				t.Errorf("mapChatStopReason(%q) = %q, want %q", tc.native, got, tc.want)
			}
		})
	}
}

func TestAnthropicStopReasons(t *testing.T) {
	cases := []struct {
		name   string
		native anthropic.StopReason
		want   StopReason
	}{
		{name: "end_turn", native: anthropic.StopReasonEndTurn, want: StopReasonEndTurn},
		{name: "tool_use", native: anthropic.StopReasonToolUse, want: StopReasonToolUse},
		{name: "max_tokens", native: anthropic.StopReasonMaxTokens, want: StopReasonMaxTokens},
		{name: "stop_sequence", native: anthropic.StopReasonStopSequence, want: StopReasonStopSequence},
		{name: "refusal", native: anthropic.StopReasonRefusal, want: StopReasonRefusal},
		{name: "pause_turn", native: anthropic.StopReasonPauseTurn, want: StopReasonPauseTurn},
		{name: "empty", native: "", want: ""},
		{name: "future", native: "future", want: StopReasonOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapAnthropicStopReason(tc.native); got != tc.want {
				t.Errorf("mapAnthropicStopReason(%q) = %q, want %q", tc.native, got, tc.want)
			}
		})
	}
}

func TestGeminiStopReasons(t *testing.T) {
	cases := []struct {
		name     string
		reason   string
		hasTools bool
		want     StopReason
	}{
		{name: "stop with tools", reason: string(genai.FinishReasonStop), hasTools: true, want: StopReasonToolUse},
		{name: "stop", reason: string(genai.FinishReasonStop), want: StopReasonEndTurn},
		{name: "max_tokens", reason: string(genai.FinishReasonMaxTokens), want: StopReasonMaxTokens},
		{name: "safety", reason: string(genai.FinishReasonSafety), want: StopReasonContentFilter},
		{name: "recitation", reason: string(genai.FinishReasonRecitation), want: StopReasonContentFilter},
		{name: "blocklist", reason: string(genai.FinishReasonBlocklist), want: StopReasonContentFilter},
		{name: "prohibited_content", reason: string(genai.FinishReasonProhibitedContent), want: StopReasonContentFilter},
		{name: "spii", reason: string(genai.FinishReasonSPII), want: StopReasonContentFilter},
		{name: "malformed_function_call", reason: string(genai.FinishReasonMalformedFunctionCall), want: StopReasonOther},
		{name: "other", reason: string(genai.FinishReasonOther), want: StopReasonOther},
		{name: "empty", reason: "", want: StopReasonOther},
		{name: "future", reason: "FINISH_REASON_FUTURE", want: StopReasonOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapGeminiStopReason(tc.reason, tc.hasTools); got != tc.want {
				t.Errorf("mapGeminiStopReason(%q, hasTools=%v) = %q, want %q", tc.reason, tc.hasTools, got, tc.want)
			}
		})
	}
}

func TestResponsesStopReasons(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		reason   string
		hasTools bool
		want     StopReason
	}{
		{name: "completed", status: string(responses.ResponseStatusCompleted), want: StopReasonEndTurn},
		{name: "completed with tools", status: string(responses.ResponseStatusCompleted), hasTools: true, want: StopReasonToolUse},
		{name: "incomplete max_output_tokens", status: string(responses.ResponseStatusIncomplete), reason: "max_output_tokens", want: StopReasonMaxTokens},
		{name: "incomplete content_filter", status: string(responses.ResponseStatusIncomplete), reason: "content_filter", want: StopReasonContentFilter},
		{name: "incomplete empty reason", status: string(responses.ResponseStatusIncomplete), reason: "", want: StopReasonOther},
		{name: "failed", status: string(responses.ResponseStatusFailed), want: StopReasonOther},
		{name: "in_progress", status: string(responses.ResponseStatusInProgress), want: StopReasonOther},
		{name: "cancelled", status: string(responses.ResponseStatusCancelled), want: StopReasonOther},
		{name: "queued", status: string(responses.ResponseStatusQueued), want: StopReasonOther},
		{name: "empty status", status: "", want: StopReasonOther},
		{name: "future_status", status: "future_status", want: StopReasonOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapResponsesStopReason(tc.status, tc.reason, tc.hasTools); got != tc.want {
				t.Errorf("mapResponsesStopReason(%q, %q, hasTools=%v) = %q, want %q", tc.status, tc.reason, tc.hasTools, got, tc.want)
			}
		})
	}
}

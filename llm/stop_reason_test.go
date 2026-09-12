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
	for native, want := range map[string]StopReason{
		"stop":           StopReasonEndTurn,
		"tool_calls":     StopReasonToolUse,
		"function_call":  StopReasonToolUse,
		"length":         StopReasonMaxTokens,
		"content_filter": StopReasonContentFilter,
		"":               StopReasonOther,
		"future":         StopReasonOther,
	} {
		if got := mapChatStopReason(native); got != want {
			t.Fatalf("%q=%q", native, got)
		}
	}
}

func TestAnthropicStopReasons(t *testing.T) {
	for native, want := range map[anthropic.StopReason]StopReason{
		anthropic.StopReasonEndTurn:      StopReasonEndTurn,
		anthropic.StopReasonToolUse:      StopReasonToolUse,
		anthropic.StopReasonMaxTokens:    StopReasonMaxTokens,
		anthropic.StopReasonStopSequence: StopReasonStopSequence,
		anthropic.StopReasonRefusal:      StopReasonRefusal,
		anthropic.StopReasonPauseTurn:    StopReasonPauseTurn,
		"future":                         StopReasonOther,
	} {
		if got := mapAnthropicStopReason(native); got != want {
			t.Fatalf("%q=%q", native, got)
		}
	}
}

func TestGeminiStopReasons(t *testing.T) {
	cases := []struct {
		reason   string
		hasTools bool
		want     StopReason
	}{
		{reason: string(genai.FinishReasonStop), hasTools: true, want: StopReasonToolUse},
		{reason: string(genai.FinishReasonStop), want: StopReasonEndTurn},
		{reason: string(genai.FinishReasonMaxTokens), want: StopReasonMaxTokens},
		{reason: string(genai.FinishReasonSafety), want: StopReasonContentFilter},
		{reason: string(genai.FinishReasonRecitation), want: StopReasonContentFilter},
		{reason: string(genai.FinishReasonBlocklist), want: StopReasonContentFilter},
		{reason: string(genai.FinishReasonProhibitedContent), want: StopReasonContentFilter},
		{reason: string(genai.FinishReasonSPII), want: StopReasonContentFilter},
		{reason: string(genai.FinishReasonMalformedFunctionCall), want: StopReasonOther},
		{reason: string(genai.FinishReasonOther), want: StopReasonOther},
		{reason: "", want: StopReasonOther},
		{reason: "FINISH_REASON_FUTURE", want: StopReasonOther},
	}
	for _, tc := range cases {
		if got := mapGeminiStopReason(tc.reason, tc.hasTools); got != tc.want {
			t.Fatalf("%q hasTools=%v=%q", tc.reason, tc.hasTools, got)
		}
	}
}

func TestResponsesStopReasons(t *testing.T) {
	cases := []struct {
		status   string
		reason   string
		hasTools bool
		want     StopReason
	}{
		{status: string(responses.ResponseStatusCompleted), want: StopReasonEndTurn},
		{status: string(responses.ResponseStatusCompleted), hasTools: true, want: StopReasonToolUse},
		{status: string(responses.ResponseStatusIncomplete), reason: "max_output_tokens", want: StopReasonMaxTokens},
		{status: string(responses.ResponseStatusIncomplete), reason: "content_filter", want: StopReasonContentFilter},
		{status: string(responses.ResponseStatusIncomplete), reason: "", want: StopReasonOther},
		{status: string(responses.ResponseStatusFailed), want: StopReasonOther},
		{status: string(responses.ResponseStatusInProgress), want: StopReasonOther},
		{status: string(responses.ResponseStatusCancelled), want: StopReasonOther},
		{status: string(responses.ResponseStatusQueued), want: StopReasonOther},
		{status: "", want: StopReasonOther},
		{status: "future_status", want: StopReasonOther},
	}
	for _, tc := range cases {
		if got := mapResponsesStopReason(tc.status, tc.reason, tc.hasTools); got != tc.want {
			t.Fatalf("status=%q reason=%q hasTools=%v=%q", tc.status, tc.reason, tc.hasTools, got)
		}
	}
}

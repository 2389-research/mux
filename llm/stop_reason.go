// ABOUTME: Provider stop-reason mapping helpers - one exhaustive mapping per
// ABOUTME: provider family from native finish reasons to mux StopReason values.

package llm

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3/responses"
	"google.golang.org/genai"
)

// mapChatStopReason maps an OpenAI-compatible finish reason to our StopReason.
// The v3 SDK carries finish reasons as plain strings; anything unrecognized
// (including empty) maps to StopReasonOther.
func mapChatStopReason(reason string) StopReason {
	switch reason {
	case "stop":
		return StopReasonEndTurn
	case "tool_calls", "function_call":
		return StopReasonToolUse
	case "length":
		return StopReasonMaxTokens
	case "content_filter":
		return StopReasonContentFilter
	default:
		return StopReasonOther
	}
}

// mapAnthropicStopReason maps an Anthropic stop reason to our StopReason using
// the SDK's named constants. An empty reason means "not finished yet"
// (message_start snapshots, usage-only message_deltas) and stays empty;
// anything else unrecognized maps to StopReasonOther.
func mapAnthropicStopReason(reason anthropic.StopReason) StopReason {
	switch reason {
	case "":
		return ""
	case anthropic.StopReasonEndTurn:
		return StopReasonEndTurn
	case anthropic.StopReasonToolUse:
		return StopReasonToolUse
	case anthropic.StopReasonMaxTokens:
		return StopReasonMaxTokens
	case anthropic.StopReasonStopSequence:
		return StopReasonStopSequence
	case anthropic.StopReasonRefusal:
		return StopReasonRefusal
	case anthropic.StopReasonPauseTurn:
		return StopReasonPauseTurn
	default:
		return StopReasonOther
	}
}

// mapGeminiStopReason maps a Gemini finish reason to our StopReason. STOP with
// actual function calls indicates tool use; safety-class reasons become
// StopReasonContentFilter; malformed, OTHER, and unknown values (including
// empty) become StopReasonOther.
func mapGeminiStopReason(reason string, hasTools bool) StopReason {
	switch genai.FinishReason(reason) {
	case genai.FinishReasonStop:
		if hasTools {
			return StopReasonToolUse
		}
		return StopReasonEndTurn
	case genai.FinishReasonMaxTokens:
		return StopReasonMaxTokens
	case genai.FinishReasonSafety,
		genai.FinishReasonRecitation,
		genai.FinishReasonBlocklist,
		genai.FinishReasonProhibitedContent,
		genai.FinishReasonSPII:
		return StopReasonContentFilter
	default:
		return StopReasonOther
	}
}

// mapResponsesStopReason maps an OpenAI Responses API status (plus the
// incomplete_details reason when the status is "incomplete") to our
// StopReason. completed means ToolUse when the response carries tool output,
// else EndTurn. The v3 SDK defines no named constants for the
// incomplete_details reasons, so they are matched as plain strings.
// failed, in-flight, and unknown statuses map to StopReasonOther. Refusal
// output content is detected by the converter and overrides a completed
// normal reason to StopReasonRefusal.
func mapResponsesStopReason(status, reason string, hasTools bool) StopReason {
	switch responses.ResponseStatus(status) {
	case responses.ResponseStatusCompleted:
		if hasTools {
			return StopReasonToolUse
		}
		return StopReasonEndTurn
	case responses.ResponseStatusIncomplete:
		switch reason {
		case "max_output_tokens":
			return StopReasonMaxTokens
		case "content_filter":
			return StopReasonContentFilter
		default:
			return StopReasonOther
		}
	default:
		return StopReasonOther
	}
}

// ABOUTME: Typed stream delta/block identity shared across provider streams.
// ABOUTME: Defines StreamDeltaKind, StreamBlockRef, StreamProtocolError and tool-input parsing.
package llm

import (
	"encoding/json"
	"fmt"
)

// StreamDeltaKind says how to interpret a content_block_delta event's Text.
type StreamDeltaKind string

const (
	// StreamDeltaText is a text content fragment.
	StreamDeltaText StreamDeltaKind = "text"
	// StreamDeltaThinking is a public reasoning/thinking text fragment.
	StreamDeltaThinking StreamDeltaKind = "thinking"
	// StreamDeltaReasoningSummary is an explicitly reviewed reasoning summary
	// fragment. No current provider emits it; it exists so a future adapter
	// with a genuinely public, reviewed summary event has a typed kind to use
	// rather than overloading StreamDeltaThinking.
	StreamDeltaReasoningSummary StreamDeltaKind = "reasoning_summary"
	// StreamDeltaToolInput is a fragment of a tool call's accumulating JSON
	// argument object.
	StreamDeltaToolInput StreamDeltaKind = "tool_input_json"
)

// StreamBlockRef maps a stream-local block identity to its position in the
// final Response.Content, delivered once on the terminal event.
type StreamBlockRef struct {
	BlockID      string
	ContentIndex int
}

// StreamProtocolError reports a provider stream that violated the expected
// event sequence or carried a tool-input document mux cannot safely execute.
// Reason is a fixed, sanitized description: it must never embed raw
// accumulated content, which may hold sensitive tool arguments.
type StreamProtocolError struct {
	Provider string
	BlockID  string
	Reason   string
}

func (e *StreamProtocolError) Error() string {
	return fmt.Sprintf("%s stream protocol: %s (%s)", e.Provider, e.Reason, e.BlockID)
}

// reasonInvalidToolInput is the fixed, sanitized reason parseStreamToolInput
// reports for every rejection. It never varies with the input, so an error
// message can never leak accumulated tool arguments.
const reasonInvalidToolInput = "invalid tool input object"

// parseStreamToolInput decodes a streamed tool call's fully accumulated JSON
// into its argument object. It requires a single JSON object — including an
// explicit empty object "{}" — and rejects an absent document (raw == ""),
// null, arrays, malformed JSON, and trailing content after the object.
//
// raw == "" covers two different provider situations that must both fail
// here: a tool_use block whose content_block_start declared no input and
// which received no input_json_delta before content_block_stop, and one
// whose deltas happened to accumulate to the empty string. Neither is a
// safe stand-in for "{}"; the caller is responsible for treating an
// explicit empty object declared at content_block_start, with no deltas at
// all, as valid input without ever calling this function.
func parseStreamToolInput(provider, blockID, raw string) (map[string]any, error) {
	if raw == "" {
		return nil, &StreamProtocolError{Provider: provider, BlockID: blockID, Reason: reasonInvalidToolInput}
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil, &StreamProtocolError{Provider: provider, BlockID: blockID, Reason: reasonInvalidToolInput}
	}
	if input == nil {
		// json.Unmarshal of a JSON "null" into a map succeeds and leaves it
		// nil without error; {} is required, so reject explicitly.
		return nil, &StreamProtocolError{Provider: provider, BlockID: blockID, Reason: reasonInvalidToolInput}
	}
	return input, nil
}

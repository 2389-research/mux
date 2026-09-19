// ABOUTME: Decodes and encodes tool-call argument JSON preserving numeric
// ABOUTME: literal text, so a large integer never rounds through float64.
package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// decodeToolInputObject decodes a JSON tool-call argument object.
//
// It decodes with json.Decoder.UseNumber, so every number keeps its literal
// text as a json.Number instead of becoming a float64. A plain interface{}
// decode rounds any integer past 2^53 — a wire input of 10000000000000001
// rebuilds as 10000000000000000 — and mux then sends the rounded value back
// on the next request. Anthropic invalidates the signature on every thinking
// block after a changed tool_use input, so that silent rounding can corrupt
// reasoning with no error raised anywhere.
//
// The decode requires exactly one JSON value with no trailing content. It
// does not reject a JSON "null" or a non-object: callers keep their own
// handling of those, since the accept/reject rule differs per provider path.
func decodeToolInputObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var input map[string]any
	if err := dec.Decode(&input); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing content after top-level JSON value")
	}
	return input, nil
}

// marshalToolInput encodes a tool-call input map to raw JSON. encoding/json
// emits a json.Number using its literal text, so a 17-digit integer survives
// digit-for-digit.
//
// The raw bytes are what callers pass to an SDK, not the decoded map: the
// Anthropic SDK shims encoding/json and would render a stdlib json.Number as
// a quoted string, whereas every SDK passes a json.RawMessage through
// verbatim.
func marshalToolInput(input map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// toolInputParam returns the value to hand an SDK for a tool_use input.
//
// A non-nil map is pre-marshalled to a json.RawMessage so numeric literal
// text survives even through an SDK that shims encoding/json. A nil map is
// returned unchanged (a typed nil map in the interface), which is exactly
// what the SDKs received before this helper existed.
func toolInputParam(input map[string]any) (any, error) {
	if input == nil {
		return input, nil
	}
	raw, err := marshalToolInput(input)
	if err != nil {
		return nil, fmt.Errorf("encoding tool input: %w", err)
	}
	return raw, nil
}

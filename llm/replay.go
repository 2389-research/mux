// ABOUTME: Provider replay envelope for preserving raw provider items
// (e.g. OpenAI reasoning items) across tool history, persistence, and the
// next request, plus the preflight validator guarding identity and payload.
package llm

import (
	"encoding/json"
	"fmt"
	"os"
)

// ContentTypeReplay identifies a block that carries a raw provider item in
// its Replay field. The raw item is authoritative when Replay exists:
// editing normalized fields does not rewrite signed or opaque data, and
// callers must explicitly remove Replay to author a changed or
// new-provider message.
const ContentTypeReplay ContentType = "replay"

// ProviderReplay pins a raw provider item to the identity it is valid for.
// Data holds the item's raw JSON, preserved byte-for-byte.
//
// Model records the effective requested model (after client defaults), not
// the returned snapshot model, so provider alias resolution does not
// falsely mismatch.
type ProviderReplay struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Data     json.RawMessage `json:"data"`

	// Data is the provider's own item shape, never a mux invention, so its
	// structural check in validateReplayBlock is provider-specific.
}

// validateReplay checks every block carrying a Replay envelope against the
// request's provider/model identity and the payload's structural integrity,
// and returns the messages to actually send. Structural failures (empty
// payload, invalid JSON, no type discriminator) have no safe normalized
// fallback, so they still fail preflight with zero HTTP requests. An
// identity mismatch is not a preflight failure: the raw provider APIs
// themselves tolerate a stale envelope on a model switch, so mux warns to
// stderr — naming both identities — and drops that block's envelope instead
// of failing the whole request. A block without a normalized fallback (a
// thinking or provider-item-only block) then contributes nothing to the
// outgoing request; a block with one, such as a tool call, still reaches
// the wire without its signature.
//
// Identity is checked before structure, so a block that is both mismatched
// and malformed is dropped with the warning rather than rejected: the block
// is going to be discarded either way, and failing the request over a
// payload that is not going to be sent would be the same over-strictness
// this function exists to remove.
//
// The returned slice may alias messages: a message with nothing to drop
// keeps its original Blocks slice, so callers must treat both the input and
// the result as read-only after this call rather than assuming they are
// independent. Blocks without Replay are ignored; a ContentTypeReplay block
// without a payload is rejected.
func validateReplay(provider, model string, messages []Message) ([]Message, error) {
	var result []Message
	for i := range messages {
		blocks, changed, err := validateReplayBlocks(provider, model, i, messages[i].Blocks)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		if result == nil {
			result = make([]Message, len(messages))
			copy(result, messages)
		}
		result[i].Blocks = blocks
	}
	if result == nil {
		return messages, nil
	}
	return result, nil
}

// validateReplayBlocks runs validateReplayBlock over one message's blocks.
// It returns the input slice unchanged (changed=false) when nothing needed
// dropping. Otherwise it clones the slice on first drop — leaving the
// caller's original untouched — clears the mismatched blocks' Replay
// pointers in the clone, and returns that.
func validateReplayBlocks(provider, model string, msgIdx int, blocks []ContentBlock) ([]ContentBlock, bool, error) {
	var cloned []ContentBlock
	for j, block := range blocks {
		if block.Replay == nil {
			if block.Type == ContentTypeReplay {
				return nil, false, fmt.Errorf("message[%d].blocks[%d].replay: replay block has no payload", msgIdx, j)
			}
			continue
		}
		drop, err := validateReplayBlock(provider, model, msgIdx, j, block.Replay)
		if err != nil {
			return nil, false, err
		}
		if !drop {
			continue
		}
		if cloned == nil {
			cloned = make([]ContentBlock, len(blocks))
			copy(cloned, blocks)
		}
		cloned[j].Replay = nil
	}
	if cloned == nil {
		return blocks, false, nil
	}
	return cloned, true, nil
}

// validateReplayBlock checks one block's replay envelope. It reports
// whether the block's envelope should be dropped (an identity mismatch,
// warned to stderr) rather than erroring; any returned error is a
// structural failure with no drop-and-continue option.
func validateReplayBlock(provider, model string, msgIdx, blockIdx int, replay *ProviderReplay) (drop bool, err error) {
	field := fmt.Sprintf("message[%d].blocks[%d].replay.data", msgIdx, blockIdx)
	if replay.Provider != provider || replay.Model != model {
		fmt.Fprintf(os.Stderr, "Warning: replay identity mismatch: request is %s/%s but replay data is %s/%s\n",
			provider, model, replay.Provider, replay.Model)
		return true, nil
	}
	if len(replay.Data) == 0 {
		return false, fmt.Errorf("%s: replay data is empty", field)
	}
	if !json.Valid(replay.Data) {
		return false, fmt.Errorf("%s: replay data is not valid JSON", field)
	}
	// Truncate the payload in every error below: opaque provider bytes must
	// not leak into logs. The field path above identifies the block.
	//
	// A genai.Part has no top-level "type" discriminator — its kind is implied
	// by which field is set — so Gemini payloads are checked against the SDK
	// struct instead. Every other provider's items carry one.
	if provider == "gemini" {
		if err := validateGeminiReplayPayload(field, replay.Data); err != nil {
			return false, err
		}
		return false, nil
	}
	var item struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(replay.Data, &item); err != nil || item.Type == "" {
		return false, fmt.Errorf("%s: unsupported replay item payload: %.32q", field, replay.Data)
	}
	return false, nil
}

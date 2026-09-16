// ABOUTME: Provider replay envelope for preserving raw provider items
// (e.g. OpenAI reasoning items) across tool history, persistence, and the
// next request, plus the preflight validator guarding identity and payload.
package llm

import (
	"encoding/json"
	"fmt"
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

// ErrReplayMismatch indicates a replay envelope was carried into a request
// for a different provider or model. The error names both identities:
// the request's (Provider, Model) and the replay's (ReplayProvider,
// ReplayModel). Provider/model migration is an explicit rejection, not a
// silent drop of the raw item.
type ErrReplayMismatch struct {
	Provider, Model             string
	ReplayProvider, ReplayModel string
}

func (e *ErrReplayMismatch) Error() string {
	return fmt.Sprintf("replay identity mismatch: request is %s/%s but replay data is %s/%s",
		e.Provider, e.Model, e.ReplayProvider, e.ReplayModel)
}

// validateReplay checks every block carrying a Replay envelope against the
// request's provider/model identity and the payload's structural integrity.
// It runs before any network call, so a mismatched or malformed replay
// fails preflight with zero HTTP requests. Blocks without Replay are
// ignored; a ContentTypeReplay block without a payload is rejected.
func validateReplay(provider, model string, messages []Message) error {
	for i, msg := range messages {
		for j, block := range msg.Blocks {
			if block.Replay == nil {
				if block.Type == ContentTypeReplay {
					return fmt.Errorf("message[%d].blocks[%d].replay: replay block has no payload", i, j)
				}
				continue
			}
			if err := validateReplayBlock(provider, model, i, j, block.Replay); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateReplayBlock(provider, model string, msgIdx, blockIdx int, replay *ProviderReplay) error {
	field := fmt.Sprintf("message[%d].blocks[%d].replay.data", msgIdx, blockIdx)
	if replay.Provider != provider || replay.Model != model {
		return &ErrReplayMismatch{
			Provider:       provider,
			Model:          model,
			ReplayProvider: replay.Provider,
			ReplayModel:    replay.Model,
		}
	}
	if len(replay.Data) == 0 {
		return fmt.Errorf("%s: replay data is empty", field)
	}
	if !json.Valid(replay.Data) {
		return fmt.Errorf("%s: replay data is not valid JSON", field)
	}
	// Truncate the payload in every error below: opaque provider bytes must
	// not leak into logs. The field path above identifies the block.
	//
	// A genai.Part has no top-level "type" discriminator — its kind is implied
	// by which field is set — so Gemini payloads are checked against the SDK
	// struct instead. Every other provider's items carry one.
	if provider == "gemini" {
		return validateGeminiReplayPayload(field, replay.Data)
	}
	var item struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(replay.Data, &item); err != nil || item.Type == "" {
		return fmt.Errorf("%s: unsupported replay item payload: %.32q", field, replay.Data)
	}
	return nil
}

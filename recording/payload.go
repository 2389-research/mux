// ABOUTME: The 18 durable record kinds, their 16 typed payload shapes, and
// ABOUTME: ValidatePayload, the strict per-kind schema check EncodeRecord uses.
package recording

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/2389-research/mux/llm"
)

// TurnStartedPayload begins a new turn from an accepted input, or from a
// recovery request when Source is "recovery"; Text is only ever a new,
// recorded instruction, never a reappended original input.
type TurnStartedPayload struct {
	InputID string `json:"input_id"`
	Source  string `json:"source"`
	Text    string `json:"text"`
}

// TurnRecoveredPayload marks that a turn resumed from a prior interrupted
// attempt, citing the evidence that justified doing so.
type TurnRecoveredPayload struct {
	PreviousTurnID string `json:"previous_turn_id"`
	EvidenceRef    string `json:"evidence_ref"`
}

// MessageStartedPayload opens one assistant message attempt.
type MessageStartedPayload struct {
	AttemptID string `json:"attempt_id"`
	Mode      string `json:"mode"`
}

// MessageDeltaPayload is one streamed fragment of a message block. Text is
// concatenated verbatim across deltas for the same BlockID; tool-argument
// deltas stay raw partial text, never executable tool input.
type MessageDeltaPayload struct {
	BlockID string `json:"block_id"`
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

// PublicBlock is one committed content block, stripped of anything private:
// no generic thinking, replay envelope, media source, or raw SDK bytes.
type PublicBlock struct {
	ID           string          `json:"id"`
	ContentIndex int             `json:"content_index"`
	Type         llm.ContentType `json:"type"`
	Channel      string          `json:"channel"`
	Text         string          `json:"text,omitempty"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        map[string]any  `json:"input,omitempty"`
}

// MessageCommittedPayload is the final, canonical form of one assistant
// message.
type MessageCommittedPayload struct {
	Blocks     []PublicBlock  `json:"blocks"`
	StopReason llm.StopReason `json:"stop_reason"`
}

// MessageAbortedPayload marks a message interrupted before completion,
// preserving why.
type MessageAbortedPayload struct {
	Reason string `json:"reason"`
}

// ToolIntentPayload commits a tool call's identity and arguments before
// dispatch. RetryOfToolCallID is set only for an explicit, host-admitted
// retry of a known operation.
type ToolIntentPayload struct {
	Name              string         `json:"name"`
	Arguments         map[string]any `json:"arguments"`
	RequestSHA256     string         `json:"request_sha256"`
	RetryOfToolCallID string         `json:"retry_of_tool_call_id,omitempty"`
}

// ToolStartedPayload marks that a committed intent actually began
// executing.
type ToolStartedPayload struct {
	RequestSHA256 string `json:"request_sha256"`
}

// ToolOutputPayload is one ordered chunk of a tool's public incremental
// output.
type ToolOutputPayload struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// ToolResultValue is the tool's own reported outcome, nested inside
// ToolResultPayload.
type ToolResultValue struct {
	Name    string `json:"name"`
	Output  string `json:"output"`
	Success bool   `json:"success"`
}

// ToolResultPayload commits a tool call's authoritative result. EvidenceRef,
// when set, is a host pointer into its own durable evidence for the result —
// what RecoveryPlan.ResultEventID later points a result_known disposition
// at.
type ToolResultPayload struct {
	Outcome     string          `json:"outcome"`
	Result      ToolResultValue `json:"result"`
	Replayed    bool            `json:"replayed,omitempty"`
	EvidenceRef string          `json:"evidence_ref,omitempty"`
}

// ToolOutcomeUnknownPayload records that a dispatched tool's outcome could
// not be determined; it never overwrites a later authoritative result.
type ToolOutcomeUnknownPayload struct {
	Reason      string `json:"reason"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
}

// ProviderUsagePayload reports one provider call's token accounting.
// llm.Usage is nested by value so a future field on it is carried, not
// silently discarded, by this payload.
type ProviderUsagePayload struct {
	Usage llm.Usage `json:"usage"`
}

// TurnTerminalPayload is shared by turn.completed, turn.failed and
// turn.cancelled: three kinds, one payload shape.
type TurnTerminalPayload struct {
	Reason string `json:"reason"`
}

// ContextCheckpointedPayload marks a paired checkpoint boundary; the actual
// snapshot travels alongside the record via Recorder.CommitCheckpoint, not
// inside this payload.
type ContextCheckpointedPayload struct {
	Reason string `json:"reason"`
}

// ContextRestoredPayload marks that context was rebuilt from a checkpoint.
// Provenance and SeedVersion are set together only for a fork-imported seed;
// an ordinary restore omits both.
type ContextRestoredPayload struct {
	CheckpointID string          `json:"checkpoint_id"`
	ThroughSeq   uint64          `json:"through_seq"`
	Provenance   *ForkProvenance `json:"provenance,omitempty"`
	SeedVersion  int             `json:"seed_version,omitempty"`
}

// ContextCompactedPayload records a summarization pass replacing a source
// range with a summary message.
type ContextCompactedPayload struct {
	SourceFromSeq    uint64 `json:"source_from_seq"`
	SourceThroughSeq uint64 `json:"source_through_seq"`
	SourceSHA256     string `json:"source_sha256"`
	SummaryMessageID string `json:"summary_message_id"`
}

// payloadKinds maps each of the 18 record kinds to a constructor for its
// payload type (three terminal kinds share TurnTerminalPayload, so there are
// 16 distinct payload types).
var payloadKinds = map[string]func() any{
	"context.restored":     func() any { return &ContextRestoredPayload{} },
	"context.compacted":    func() any { return &ContextCompactedPayload{} },
	"turn.started":         func() any { return &TurnStartedPayload{} },
	"turn.recovered":       func() any { return &TurnRecoveredPayload{} },
	"message.started":      func() any { return &MessageStartedPayload{} },
	"message.delta":        func() any { return &MessageDeltaPayload{} },
	"message.committed":    func() any { return &MessageCommittedPayload{} },
	"message.aborted":      func() any { return &MessageAbortedPayload{} },
	"tool.intent":          func() any { return &ToolIntentPayload{} },
	"tool.started":         func() any { return &ToolStartedPayload{} },
	"tool.output":          func() any { return &ToolOutputPayload{} },
	"tool.result":          func() any { return &ToolResultPayload{} },
	"tool.outcome_unknown": func() any { return &ToolOutcomeUnknownPayload{} },
	"turn.completed":       func() any { return &TurnTerminalPayload{} },
	"turn.failed":          func() any { return &TurnTerminalPayload{} },
	"turn.cancelled":       func() any { return &TurnTerminalPayload{} },
	"provider.usage":       func() any { return &ProviderUsagePayload{} },
	"context.checkpointed": func() any { return &ContextCheckpointedPayload{} },
}

var hex64Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidatePayload strict-decodes payload against the schema named by kind
// and checks every field-level constraint from the payload specification:
// required non-omitempty fields present, enums exact, SHA-256 fields 64
// lowercase hex, IDs within their bounds, and no unknown fields. It uses the
// same strict canonical decoder as EncodeRecord/DecodeRecord, so duplicate
// keys and invalid UTF-8 cannot pass validation unnoticed.
func ValidatePayload(kind string, payload json.RawMessage) error {
	const op = "ValidatePayload"
	newPayload, ok := payloadKinds[kind]
	if !ok {
		return &Error{Kind: InvalidRecord, Op: op, Cause: fmt.Errorf("unknown kind %q", kind)}
	}
	v := newPayload()
	if err := decodeStrict(payload, v); err != nil {
		return &Error{Kind: InvalidRecord, Op: op, Cause: fmt.Errorf("kind %q: %w", kind, err)}
	}
	if err := validatePayloadFields(kind, v); err != nil {
		return &Error{Kind: InvalidRecord, Op: op, Cause: fmt.Errorf("kind %q: %w", kind, err)}
	}
	return nil
}

// nonemptyMax reports whether s is nonempty and at most max bytes.
func nonemptyMax(s string, max int) bool {
	return len(s) > 0 && len(s) <= max
}

// validatePayloadFields checks the semantic constraints from the payload
// specification that strict struct decoding alone cannot express: enum
// membership, SHA-256 shape, ID length bounds, and the few cross-field
// rules such as PublicBlock's Type/Channel pairing.
func validatePayloadFields(kind string, v any) error {
	switch p := v.(type) {
	case *TurnStartedPayload:
		if !nonemptyMax(p.InputID, 160) {
			return fmt.Errorf("input_id must be 1-160 bytes")
		}
		if p.Source != "user" && p.Source != "recovery" {
			return fmt.Errorf("source must be user or recovery")
		}
	case *TurnRecoveredPayload:
		if !nonemptyMax(p.PreviousTurnID, 160) {
			return fmt.Errorf("previous_turn_id must be 1-160 bytes")
		}
		if p.EvidenceRef == "" {
			return fmt.Errorf("evidence_ref is required")
		}
	case *MessageStartedPayload:
		if !nonemptyMax(p.AttemptID, 160) {
			return fmt.Errorf("attempt_id must be 1-160 bytes")
		}
		if p.Mode != "stream" && p.Mode != "nonstream" {
			return fmt.Errorf("mode must be stream or nonstream")
		}
	case *MessageDeltaPayload:
		if !nonemptyMax(p.BlockID, 160) {
			return fmt.Errorf("block_id must be 1-160 bytes")
		}
		if !isDeltaChannel(p.Channel) {
			return fmt.Errorf("channel must be assistant_text, tool_arguments or reasoning_summary")
		}
	case *MessageCommittedPayload:
		if len(p.Blocks) == 0 {
			return fmt.Errorf("blocks must be nonempty")
		}
		for i := range p.Blocks {
			if err := validatePublicBlock(&p.Blocks[i]); err != nil {
				return fmt.Errorf("blocks[%d]: %w", i, err)
			}
		}
		if !isStopReason(p.StopReason) {
			return fmt.Errorf("stop_reason is not a recognized value")
		}
	case *MessageAbortedPayload:
		if p.Reason == "" {
			return fmt.Errorf("reason is required")
		}
	case *ToolIntentPayload:
		if !nonemptyMax(p.Name, 160) {
			return fmt.Errorf("name must be 1-160 bytes")
		}
		if p.Arguments == nil {
			return fmt.Errorf("arguments must be an object, not null")
		}
		if !hex64Pattern.MatchString(p.RequestSHA256) {
			return fmt.Errorf("request_sha256 must be 64 lowercase hex characters")
		}
		if p.RetryOfToolCallID != "" && !nonemptyMax(p.RetryOfToolCallID, 512) {
			return fmt.Errorf("retry_of_tool_call_id must be at most 512 bytes")
		}
	case *ToolStartedPayload:
		if !hex64Pattern.MatchString(p.RequestSHA256) {
			return fmt.Errorf("request_sha256 must be 64 lowercase hex characters")
		}
	case *ToolOutputPayload:
		if p.Stream != "stdout" && p.Stream != "stderr" {
			return fmt.Errorf("stream must be stdout or stderr")
		}
	case *ToolResultPayload:
		if !isToolOutcome(p.Outcome) {
			return fmt.Errorf("outcome is not a recognized value")
		}
		if p.Result.Name == "" {
			return fmt.Errorf("result.name is required")
		}
	case *ToolOutcomeUnknownPayload:
		if p.Reason == "" {
			return fmt.Errorf("reason is required")
		}
	case *ProviderUsagePayload:
		if p.Usage.InputTokens < 0 || p.Usage.OutputTokens < 0 || p.Usage.ThinkingTokens < 0 {
			return fmt.Errorf("usage counters must be nonnegative")
		}
	case *TurnTerminalPayload:
		if p.Reason == "" {
			return fmt.Errorf("reason is required")
		}
	case *ContextCheckpointedPayload:
		if p.Reason == "" {
			return fmt.Errorf("reason is required")
		}
	case *ContextRestoredPayload:
		if !nonemptyMax(p.CheckpointID, 160) {
			return fmt.Errorf("checkpoint_id must be 1-160 bytes")
		}
		if p.Provenance != nil && p.SeedVersion != 1 {
			return fmt.Errorf("seed_version must be 1 when provenance is set")
		}
		if p.Provenance == nil && p.SeedVersion != 0 {
			return fmt.Errorf("seed_version must be omitted without provenance")
		}
	case *ContextCompactedPayload:
		if p.SourceThroughSeq < p.SourceFromSeq {
			return fmt.Errorf("source_through_seq must not precede source_from_seq")
		}
		if !hex64Pattern.MatchString(p.SourceSHA256) {
			return fmt.Errorf("source_sha256 must be 64 lowercase hex characters")
		}
		if !nonemptyMax(p.SummaryMessageID, 160) {
			return fmt.Errorf("summary_message_id must be 1-160 bytes")
		}
	default:
		return fmt.Errorf("kind %q has no field validator", kind)
	}
	return nil
}

func isDeltaChannel(c string) bool {
	switch c {
	case "assistant_text", "tool_arguments", "reasoning_summary":
		return true
	}
	return false
}

func isToolOutcome(o string) bool {
	switch o {
	case "succeeded", "failed", "cancelled", "outcome_unknown":
		return true
	}
	return false
}

func isStopReason(r llm.StopReason) bool {
	switch r {
	case llm.StopReasonEndTurn, llm.StopReasonToolUse, llm.StopReasonMaxTokens,
		llm.StopReasonStopSequence, llm.StopReasonRefusal, llm.StopReasonContentFilter,
		llm.StopReasonPauseTurn, llm.StopReasonOther:
		return true
	}
	return false
}

// validatePublicBlock checks one committed block: nonempty bounded ID,
// nonnegative content index, and the fixed Type/Channel pairing a public
// block is allowed to carry.
func validatePublicBlock(b *PublicBlock) error {
	if !nonemptyMax(b.ID, 160) {
		return fmt.Errorf("id must be 1-160 bytes")
	}
	if b.ContentIndex < 0 {
		return fmt.Errorf("content_index must be nonnegative")
	}
	switch {
	case b.Type == llm.ContentTypeText && b.Channel == "assistant_text":
	case b.Type == llm.ContentTypeToolUse && b.Channel == "tool_arguments":
	case b.Type == llm.ContentTypeThinking && b.Channel == "reasoning_summary":
	default:
		return fmt.Errorf("type %q is not valid for channel %q", b.Type, b.Channel)
	}
	return nil
}

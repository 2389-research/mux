// ABOUTME: Tests for ValidatePayload across all 18 record kinds: the valid
// ABOUTME: shape for each, and a targeted rejection case per constraint.
package recording

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/2389-research/mux/llm"
)

// validHex64 is a well-formed 64-lowercase-hex string usable anywhere a
// SHA-256 field is required, when its exact digits do not matter.
var validHex64 = strings.Repeat("a", 64)

// mustMarshal encodes v with the standard library's ordinary (non-canonical)
// Marshal: ValidatePayload strict-decodes whatever bytes it is given, it
// does not require them to already be in mux-json/1 canonical form.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return raw
}

// assertPayloadInvalid fails the test unless err is an InvalidRecord *Error
// whose message names the specific constraint in wantSubstring.
func assertPayloadInvalid(t *testing.T, err error, wantSubstring string) {
	t.Helper()
	assertKind(t, err, InvalidRecord)
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("error %q does not contain %q", err.Error(), wantSubstring)
	}
}

func TestValidatePayload_TurnStarted(t *testing.T) {
	valid := TurnStartedPayload{InputID: "input-1", Source: "user", Text: "hello"}
	if err := ValidatePayload("turn.started", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	atBound := TurnStartedPayload{InputID: strings.Repeat("a", 160), Source: "recovery"}
	if err := ValidatePayload("turn.started", mustMarshal(t, atBound)); err != nil {
		t.Fatalf("160-byte input_id must be accepted: %v", err)
	}

	cases := []struct {
		name    string
		payload TurnStartedPayload
		want    string
	}{
		{"empty input id", TurnStartedPayload{InputID: "", Source: "user"}, "input_id must be 1-160 bytes"},
		{"input id too long", TurnStartedPayload{InputID: strings.Repeat("a", 161), Source: "user"}, "input_id must be 1-160 bytes"},
		{"bad source", TurnStartedPayload{InputID: "input-1", Source: "bogus"}, "source must be user or recovery"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePayload("turn.started", mustMarshal(t, tc.payload))
			assertPayloadInvalid(t, err, tc.want)
		})
	}
}

func TestValidatePayload_TurnRecovered(t *testing.T) {
	valid := TurnRecoveredPayload{PreviousTurnID: "turn-0", EvidenceRef: "evidence-1"}
	if err := ValidatePayload("turn.recovered", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	cases := []struct {
		name    string
		payload TurnRecoveredPayload
		want    string
	}{
		{"empty previous turn id", TurnRecoveredPayload{PreviousTurnID: "", EvidenceRef: "e"}, "previous_turn_id must be 1-160 bytes"},
		{"previous turn id too long", TurnRecoveredPayload{PreviousTurnID: strings.Repeat("a", 161), EvidenceRef: "e"}, "previous_turn_id must be 1-160 bytes"},
		{"empty evidence ref", TurnRecoveredPayload{PreviousTurnID: "turn-0", EvidenceRef: ""}, "evidence_ref is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePayload("turn.recovered", mustMarshal(t, tc.payload))
			assertPayloadInvalid(t, err, tc.want)
		})
	}
}

func TestValidatePayload_MessageStarted(t *testing.T) {
	valid := MessageStartedPayload{AttemptID: "attempt-1", Mode: "stream"}
	if err := ValidatePayload("message.started", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	nonstream := MessageStartedPayload{AttemptID: "attempt-1", Mode: "nonstream"}
	if err := ValidatePayload("message.started", mustMarshal(t, nonstream)); err != nil {
		t.Fatalf("nonstream mode must be accepted: %v", err)
	}

	cases := []struct {
		name    string
		payload MessageStartedPayload
		want    string
	}{
		{"empty attempt id", MessageStartedPayload{AttemptID: "", Mode: "stream"}, "attempt_id must be 1-160 bytes"},
		{"attempt id too long", MessageStartedPayload{AttemptID: strings.Repeat("a", 161), Mode: "stream"}, "attempt_id must be 1-160 bytes"},
		{"bad mode", MessageStartedPayload{AttemptID: "attempt-1", Mode: "bogus"}, "mode must be stream or nonstream"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePayload("message.started", mustMarshal(t, tc.payload))
			assertPayloadInvalid(t, err, tc.want)
		})
	}
}

func TestValidatePayload_MessageDelta(t *testing.T) {
	for _, ch := range []string{"assistant_text", "tool_arguments", "reasoning_summary"} {
		valid := MessageDeltaPayload{BlockID: "block-1", Channel: ch, Text: "partial"}
		if err := ValidatePayload("message.delta", mustMarshal(t, valid)); err != nil {
			t.Fatalf("valid channel %q rejected: %v", ch, err)
		}
	}

	cases := []struct {
		name    string
		payload MessageDeltaPayload
		want    string
	}{
		{"empty block id", MessageDeltaPayload{BlockID: "", Channel: "assistant_text"}, "block_id must be 1-160 bytes"},
		{"block id too long", MessageDeltaPayload{BlockID: strings.Repeat("a", 161), Channel: "assistant_text"}, "block_id must be 1-160 bytes"},
		{"bad channel", MessageDeltaPayload{BlockID: "block-1", Channel: "bogus"}, "channel must be assistant_text, tool_arguments or reasoning_summary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePayload("message.delta", mustMarshal(t, tc.payload))
			assertPayloadInvalid(t, err, tc.want)
		})
	}
}

func TestValidatePayload_MessageCommitted(t *testing.T) {
	pairings := []struct {
		typ     llm.ContentType
		channel string
	}{
		{llm.ContentTypeText, "assistant_text"},
		{llm.ContentTypeToolUse, "tool_arguments"},
		{llm.ContentTypeThinking, "reasoning_summary"},
	}
	for _, p := range pairings {
		valid := MessageCommittedPayload{
			Blocks:     []PublicBlock{{ID: "block-1", ContentIndex: 0, Type: p.typ, Channel: p.channel}},
			StopReason: llm.StopReasonEndTurn,
		}
		if err := ValidatePayload("message.committed", mustMarshal(t, valid)); err != nil {
			t.Fatalf("valid pairing %s/%s rejected: %v", p.typ, p.channel, err)
		}
	}

	for _, sr := range []llm.StopReason{
		llm.StopReasonEndTurn, llm.StopReasonToolUse, llm.StopReasonMaxTokens,
		llm.StopReasonStopSequence, llm.StopReasonRefusal, llm.StopReasonContentFilter,
		llm.StopReasonPauseTurn, llm.StopReasonOther,
	} {
		valid := MessageCommittedPayload{
			Blocks:     []PublicBlock{{ID: "block-1", ContentIndex: 0, Type: llm.ContentTypeText, Channel: "assistant_text"}},
			StopReason: sr,
		}
		if err := ValidatePayload("message.committed", mustMarshal(t, valid)); err != nil {
			t.Fatalf("valid stop reason %q rejected: %v", sr, err)
		}
	}

	baseBlock := PublicBlock{ID: "block-1", ContentIndex: 0, Type: llm.ContentTypeText, Channel: "assistant_text"}

	t.Run("empty blocks", func(t *testing.T) {
		p := MessageCommittedPayload{Blocks: nil, StopReason: llm.StopReasonEndTurn}
		err := ValidatePayload("message.committed", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "blocks must be nonempty")
	})

	t.Run("block id empty", func(t *testing.T) {
		b := baseBlock
		b.ID = ""
		p := MessageCommittedPayload{Blocks: []PublicBlock{b}, StopReason: llm.StopReasonEndTurn}
		err := ValidatePayload("message.committed", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "blocks[0]: id must be 1-160 bytes")
	})

	t.Run("negative content index", func(t *testing.T) {
		b := baseBlock
		b.ContentIndex = -1
		p := MessageCommittedPayload{Blocks: []PublicBlock{b}, StopReason: llm.StopReasonEndTurn}
		err := ValidatePayload("message.committed", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "blocks[0]: content_index must be nonnegative")
	})

	t.Run("type channel mismatch", func(t *testing.T) {
		b := baseBlock
		b.Channel = "tool_arguments"
		p := MessageCommittedPayload{Blocks: []PublicBlock{b}, StopReason: llm.StopReasonEndTurn}
		err := ValidatePayload("message.committed", mustMarshal(t, p))
		assertPayloadInvalid(t, err, `blocks[0]: type "text" is not valid for channel "tool_arguments"`)
	})

	t.Run("unrecognized stop reason", func(t *testing.T) {
		p := MessageCommittedPayload{Blocks: []PublicBlock{baseBlock}, StopReason: llm.StopReason("bogus")}
		err := ValidatePayload("message.committed", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "stop_reason is not a recognized value")
	})
}

func TestValidatePayload_MessageAborted(t *testing.T) {
	valid := MessageAbortedPayload{Reason: "interrupted"}
	if err := ValidatePayload("message.aborted", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	invalid := MessageAbortedPayload{Reason: ""}
	err := ValidatePayload("message.aborted", mustMarshal(t, invalid))
	assertPayloadInvalid(t, err, "reason is required")
}

func TestValidatePayload_ToolIntent(t *testing.T) {
	valid := ToolIntentPayload{
		Name:          "search_web",
		Arguments:     map[string]any{},
		RequestSHA256: validHex64,
	}
	if err := ValidatePayload("tool.intent", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	cases := []struct {
		name    string
		payload ToolIntentPayload
		want    string
	}{
		{
			"empty name",
			ToolIntentPayload{Name: "", Arguments: map[string]any{}, RequestSHA256: validHex64},
			"name must be 1-160 bytes",
		},
		{
			"name too long",
			ToolIntentPayload{Name: strings.Repeat("a", 161), Arguments: map[string]any{}, RequestSHA256: validHex64},
			"name must be 1-160 bytes",
		},
		{
			"nil arguments",
			ToolIntentPayload{Name: "search_web", Arguments: nil, RequestSHA256: validHex64},
			"arguments must be an object, not null",
		},
		{
			"sha too short",
			ToolIntentPayload{Name: "search_web", Arguments: map[string]any{}, RequestSHA256: "abc"},
			"request_sha256 must be 64 lowercase hex characters",
		},
		{
			"sha uppercase not allowed",
			ToolIntentPayload{Name: "search_web", Arguments: map[string]any{}, RequestSHA256: strings.Repeat("A", 64)},
			"request_sha256 must be 64 lowercase hex characters",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePayload("tool.intent", mustMarshal(t, tc.payload))
			assertPayloadInvalid(t, err, tc.want)
		})
	}
}

// TestValidatePayload_ToolIntent_RetryOfToolCallIDBoundIs512NotStandard160
// pins the amendment's deliberate deviation: retry_of_tool_call_id is
// 1-512 bytes, matching top-level tool_call_id, not the 1-160 bound every
// other ID in this package uses. A regression that "normalizes" it back to
// 160 must fail the 300-byte and 512-byte acceptance checks below.
func TestValidatePayload_ToolIntent_RetryOfToolCallIDBoundIs512NotStandard160(t *testing.T) {
	base := ToolIntentPayload{
		Name:          "search_web",
		Arguments:     map[string]any{},
		RequestSHA256: validHex64,
	}

	beyond160 := base
	beyond160.RetryOfToolCallID = strings.Repeat("a", 300)
	if err := ValidatePayload("tool.intent", mustMarshal(t, beyond160)); err != nil {
		t.Fatalf("300-byte retry_of_tool_call_id must be accepted (bound is 512, not 160): %v", err)
	}

	at512 := base
	at512.RetryOfToolCallID = strings.Repeat("a", 512)
	if err := ValidatePayload("tool.intent", mustMarshal(t, at512)); err != nil {
		t.Fatalf("512-byte retry_of_tool_call_id must be accepted: %v", err)
	}

	at513 := base
	at513.RetryOfToolCallID = strings.Repeat("a", 513)
	err := ValidatePayload("tool.intent", mustMarshal(t, at513))
	assertPayloadInvalid(t, err, "retry_of_tool_call_id must be at most 512 bytes")
}

func TestValidatePayload_ToolStarted(t *testing.T) {
	valid := ToolStartedPayload{RequestSHA256: validHex64}
	if err := ValidatePayload("tool.started", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	invalid := ToolStartedPayload{RequestSHA256: ""}
	err := ValidatePayload("tool.started", mustMarshal(t, invalid))
	assertPayloadInvalid(t, err, "request_sha256 must be 64 lowercase hex characters")
}

func TestValidatePayload_ToolOutput(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		valid := ToolOutputPayload{Stream: stream, Text: "line"}
		if err := ValidatePayload("tool.output", mustMarshal(t, valid)); err != nil {
			t.Fatalf("valid stream %q rejected: %v", stream, err)
		}
	}

	invalid := ToolOutputPayload{Stream: "bogus", Text: "line"}
	err := ValidatePayload("tool.output", mustMarshal(t, invalid))
	assertPayloadInvalid(t, err, "stream must be stdout or stderr")
}

func TestValidatePayload_ToolResult(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed", "cancelled", "outcome_unknown"} {
		valid := ToolResultPayload{
			Outcome: outcome,
			Result:  ToolResultValue{Name: "tool-1", Output: "ok", Success: outcome == "succeeded"},
		}
		if err := ValidatePayload("tool.result", mustMarshal(t, valid)); err != nil {
			t.Fatalf("valid outcome %q rejected: %v", outcome, err)
		}
	}

	t.Run("bad outcome", func(t *testing.T) {
		p := ToolResultPayload{Outcome: "bogus", Result: ToolResultValue{Name: "tool-1"}}
		err := ValidatePayload("tool.result", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "outcome is not a recognized value")
	})

	t.Run("empty result name", func(t *testing.T) {
		p := ToolResultPayload{Outcome: "succeeded", Result: ToolResultValue{Name: ""}}
		err := ValidatePayload("tool.result", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "result.name is required")
	})

	// EvidenceRef is json:"evidence_ref,omitempty", so mustMarshal on the
	// Go struct can never produce a present-but-empty key: these two cases
	// go through raw JSON literals instead, the same way a host handing
	// ValidatePayload bytes from elsewhere could.
	t.Run("absent evidence ref is valid", func(t *testing.T) {
		raw := json.RawMessage(`{"outcome":"succeeded","result":{"name":"tool-1","output":"ok","success":true}}`)
		if err := ValidatePayload("tool.result", raw); err != nil {
			t.Fatalf("absent evidence_ref must be accepted: %v", err)
		}
	})

	t.Run("present but empty evidence ref is rejected", func(t *testing.T) {
		raw := json.RawMessage(`{"outcome":"succeeded","result":{"name":"tool-1","output":"ok","success":true},"evidence_ref":""}`)
		err := ValidatePayload("tool.result", raw)
		assertPayloadInvalid(t, err, "evidence_ref must not be empty when present")
	})
}

func TestValidatePayload_ToolOutcomeUnknown(t *testing.T) {
	valid := ToolOutcomeUnknownPayload{Reason: "timeout"}
	if err := ValidatePayload("tool.outcome_unknown", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	invalid := ToolOutcomeUnknownPayload{Reason: ""}
	err := ValidatePayload("tool.outcome_unknown", mustMarshal(t, invalid))
	assertPayloadInvalid(t, err, "reason is required")
}

func TestValidatePayload_ProviderUsage(t *testing.T) {
	valid := ProviderUsagePayload{Usage: llm.Usage{InputTokens: 10, OutputTokens: 20, ThinkingTokens: 0}}
	if err := ValidatePayload("provider.usage", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	cases := []struct {
		name    string
		payload ProviderUsagePayload
	}{
		{"negative input tokens", ProviderUsagePayload{Usage: llm.Usage{InputTokens: -1}}},
		{"negative output tokens", ProviderUsagePayload{Usage: llm.Usage{OutputTokens: -1}}},
		{"negative thinking tokens", ProviderUsagePayload{Usage: llm.Usage{ThinkingTokens: -1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePayload("provider.usage", mustMarshal(t, tc.payload))
			assertPayloadInvalid(t, err, "usage counters must be nonnegative")
		})
	}
}

func TestValidatePayload_TurnCompleted(t *testing.T) {
	valid := TurnTerminalPayload{Reason: "done"}
	if err := ValidatePayload("turn.completed", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	err := ValidatePayload("turn.completed", mustMarshal(t, TurnTerminalPayload{Reason: ""}))
	assertPayloadInvalid(t, err, "reason is required")
}

func TestValidatePayload_TurnFailed(t *testing.T) {
	valid := TurnTerminalPayload{Reason: "provider_error"}
	if err := ValidatePayload("turn.failed", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	err := ValidatePayload("turn.failed", mustMarshal(t, TurnTerminalPayload{Reason: ""}))
	assertPayloadInvalid(t, err, "reason is required")
}

func TestValidatePayload_TurnCancelled(t *testing.T) {
	valid := TurnTerminalPayload{Reason: "user_cancelled"}
	if err := ValidatePayload("turn.cancelled", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	err := ValidatePayload("turn.cancelled", mustMarshal(t, TurnTerminalPayload{Reason: ""}))
	assertPayloadInvalid(t, err, "reason is required")
}

func TestValidatePayload_ContextCheckpointed(t *testing.T) {
	valid := ContextCheckpointedPayload{Reason: "periodic"}
	if err := ValidatePayload("context.checkpointed", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	invalid := ContextCheckpointedPayload{Reason: ""}
	err := ValidatePayload("context.checkpointed", mustMarshal(t, invalid))
	assertPayloadInvalid(t, err, "reason is required")
}

func TestValidatePayload_ContextRestored(t *testing.T) {
	plain := ContextRestoredPayload{CheckpointID: "chk-1", ThroughSeq: 5}
	if err := ValidatePayload("context.restored", mustMarshal(t, plain)); err != nil {
		t.Fatalf("valid plain restore rejected: %v", err)
	}

	forked := ContextRestoredPayload{
		CheckpointID: "chk-1",
		ThroughSeq:   5,
		Provenance: &ForkProvenance{
			SessionID:    "source-sess",
			ThroughSeq:   5,
			CheckpointID: "chk-0",
			StateSHA256:  validHex64,
			HistoryRef:   "ref-1",
		},
		SeedVersion: 1,
	}
	if err := ValidatePayload("context.restored", mustMarshal(t, forked)); err != nil {
		t.Fatalf("valid fork-provenance restore rejected: %v", err)
	}

	t.Run("empty checkpoint id", func(t *testing.T) {
		p := ContextRestoredPayload{CheckpointID: ""}
		err := ValidatePayload("context.restored", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "checkpoint_id must be 1-160 bytes")
	})

	t.Run("checkpoint id too long", func(t *testing.T) {
		p := ContextRestoredPayload{CheckpointID: strings.Repeat("a", 161)}
		err := ValidatePayload("context.restored", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "checkpoint_id must be 1-160 bytes")
	})

	t.Run("provenance set but seed version zero", func(t *testing.T) {
		p := ContextRestoredPayload{
			CheckpointID: "chk-1",
			Provenance:   &ForkProvenance{SessionID: "s", CheckpointID: "c", StateSHA256: validHex64},
			SeedVersion:  0,
		}
		err := ValidatePayload("context.restored", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "seed_version must be 1 when provenance is set")
	})

	t.Run("provenance set but seed version wrong", func(t *testing.T) {
		p := ContextRestoredPayload{
			CheckpointID: "chk-1",
			Provenance:   &ForkProvenance{SessionID: "s", CheckpointID: "c", StateSHA256: validHex64},
			SeedVersion:  2,
		}
		err := ValidatePayload("context.restored", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "seed_version must be 1 when provenance is set")
	})

	t.Run("seed version set without provenance", func(t *testing.T) {
		raw := json.RawMessage(`{"checkpoint_id":"chk-1","through_seq":5,"seed_version":1}`)
		err := ValidatePayload("context.restored", raw)
		assertPayloadInvalid(t, err, "seed_version must be omitted without provenance")
	})
}

func TestValidatePayload_ContextCompacted(t *testing.T) {
	valid := ContextCompactedPayload{
		SourceFromSeq:    1,
		SourceThroughSeq: 5,
		SourceSHA256:     validHex64,
		SummaryMessageID: "summary-1",
	}
	if err := ValidatePayload("context.compacted", mustMarshal(t, valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	equalBound := ContextCompactedPayload{
		SourceFromSeq:    5,
		SourceThroughSeq: 5,
		SourceSHA256:     validHex64,
		SummaryMessageID: "summary-1",
	}
	if err := ValidatePayload("context.compacted", mustMarshal(t, equalBound)); err != nil {
		t.Fatalf("equal from/through seq must be accepted: %v", err)
	}

	t.Run("through precedes from", func(t *testing.T) {
		p := ContextCompactedPayload{
			SourceFromSeq: 5, SourceThroughSeq: 1,
			SourceSHA256: validHex64, SummaryMessageID: "summary-1",
		}
		err := ValidatePayload("context.compacted", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "source_through_seq must not precede source_from_seq")
	})

	t.Run("bad source sha", func(t *testing.T) {
		p := ContextCompactedPayload{
			SourceFromSeq: 1, SourceThroughSeq: 5,
			SourceSHA256: "not-a-hash", SummaryMessageID: "summary-1",
		}
		err := ValidatePayload("context.compacted", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "source_sha256 must be 64 lowercase hex characters")
	})

	t.Run("empty summary message id", func(t *testing.T) {
		p := ContextCompactedPayload{
			SourceFromSeq: 1, SourceThroughSeq: 5,
			SourceSHA256: validHex64, SummaryMessageID: "",
		}
		err := ValidatePayload("context.compacted", mustMarshal(t, p))
		assertPayloadInvalid(t, err, "summary_message_id must be 1-160 bytes")
	})
}

func TestValidatePayload_RejectsUnknownKind(t *testing.T) {
	err := ValidatePayload("no.such.kind", json.RawMessage(`{}`))
	assertPayloadInvalid(t, err, `unknown kind "no.such.kind"`)
}

// TestValidatePayload_RejectsUnknownField confirms ValidatePayload wires its
// own kind-specific type into the same strict decoder EncodeRecord/
// DecodeRecord use, so an extra field is rejected here too, not just at the
// outer Record level.
func TestValidatePayload_RejectsUnknownField(t *testing.T) {
	raw := json.RawMessage(`{"input_id":"input-1","source":"user","text":"hi","extra_field":"nope"}`)
	err := ValidatePayload("turn.started", raw)
	assertKind(t, err, InvalidRecord)
	if !strings.Contains(err.Error(), "extra_field") {
		t.Fatalf("error %q does not mention the unknown field", err.Error())
	}
}

// TestValidatePayload_RejectsDuplicateKey confirms ValidatePayload's decode
// path runs the same duplicate-key check as EncodePayload, not a laxer one.
func TestValidatePayload_RejectsDuplicateKey(t *testing.T) {
	raw := json.RawMessage(`{"reason":"a","reason":"b"}`)
	err := ValidatePayload("message.aborted", raw)
	assertKind(t, err, InvalidRecord)
	if !strings.Contains(err.Error(), "duplicate object key") {
		t.Fatalf("error %q does not mention the duplicate key", err.Error())
	}
}

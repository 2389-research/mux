// ABOUTME: CheckpointState and its nested value types: the pure, versioned
// ABOUTME: projection a recovery reducer builds and a snapshot codec encodes.
//
// These declarations belong to values (this package); the snapshot codec
// that encodes/decodes them into a Snapshot's public/private wire envelopes,
// and the reducer that builds them from a committed tail, are implemented
// elsewhere against this exact shape. JSON tags are snake_case and every
// boolean/numeric field is encoded without omitempty, since a snapshot must
// distinguish "false"/"0" from "absent".
package recording

import "github.com/2389-research/mux/llm"

// SnapshotCodec identifies the versioned wire format CheckpointState is
// encoded under.
const SnapshotCodec = "mux-state/1"

// UsageState accumulates token counters as plain integers: a snapshot must
// not carry a copied mutex.
type UsageState struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	ThinkingTokens   int64 `json:"thinking_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	RequestCount     int64 `json:"request_count"`
}

// InputState records one accepted turn input (user or recovery), so a
// repeated InputID can be rejected without replaying history.
type InputState struct {
	ID     string `json:"id"`
	TurnID string `json:"turn_id"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

// EventDigest fingerprints one applied committed event, so a duplicate
// EventID with different bytes or a different Seq is detectable across
// snapshot boundaries.
type EventDigest struct {
	EventID    string `json:"event_id"`
	DataSHA256 string `json:"data_sha256"`
	Seq        uint64 `json:"seq"`
}

// BlockState is the reduced public projection of one streamed content
// block: concatenated text under one channel, nothing provider-private.
type BlockState struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

// ToolOutputState is one ordered chunk of a tool's public incremental
// output.
type ToolOutputState struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// MessageState is one assistant message's reduced state: immutable
// identity, completion/interruption flags, the full llm.Message once
// committed, and its streamed Blocks.
type MessageState struct {
	ID          string       `json:"id"`
	TurnID      string       `json:"turn_id"`
	AttemptID   string       `json:"attempt_id"`
	InputID     string       `json:"input_id"`
	Complete    bool         `json:"complete"`
	Interrupted bool         `json:"interrupted"`
	Message     llm.Message  `json:"message"`
	Blocks      []BlockState `json:"blocks"`
}

// OperationState is one tool operation's reduced state: its identity,
// disposition, and — once known — its authoritative result reference.
// ToolCallID is the original canonical provider call; ToolCallIDs includes
// that original and every later actual retry attempt; RetryOfToolCallID
// names the call this one recovers, when this is a retry. Operation
// identity is unique within SourceSessionID; an operation imported by fork
// is Inert and cannot be redispatched in the new session.
type OperationState struct {
	SourceSessionID            string            `json:"source_session_id"`
	OperationID                string            `json:"operation_id"`
	ToolCallID                 string            `json:"tool_call_id"`
	TurnID                     string            `json:"turn_id"`
	Disposition                string            `json:"disposition"`
	EvidenceRef                string            `json:"evidence_ref"`
	ResultEventID              string            `json:"result_event_id"`
	Inert                      bool              `json:"inert"`
	Name                       string            `json:"name"`
	RequestSHA256              string            `json:"request_sha256"`
	ToolCallIDs                []string          `json:"tool_call_ids"`
	RetryOfToolCallID          string            `json:"retry_of_tool_call_id"`
	MayRedispatchSameOperation bool              `json:"may_redispatch_same_operation"`
	ResultRecord               []byte            `json:"result_record"`
	ResultExecutionEpoch       uint64            `json:"result_execution_epoch"`
	Output                     []ToolOutputState `json:"output"`
}

// CompactionState records one summarization pass over a source range of the
// session, so a later fork or restore knows which raw messages a summary
// replaces.
type CompactionState struct {
	ThroughSeq       uint64 `json:"through_seq"`
	SourceSHA256     string `json:"source_sha256"`
	SummaryMessageID string `json:"summary_message_id"`
}

// ForkProvenance identifies the source checkpoint a fork-imported session
// seed was prepared from.
type ForkProvenance struct {
	SessionID    string `json:"session_id"`
	ThroughSeq   uint64 `json:"through_seq"`
	CheckpointID string `json:"checkpoint_id"`
	StateSHA256  string `json:"state_sha256"`
	HistoryRef   string `json:"history_ref"`
}

// CheckpointState is the complete pure projection of a durable session at a
// checkpoint. SourceBinding records the last admitted execution owner whose
// effects this state represents: a normal turn sets it to the current
// binding before its first checkpoint; pure recovery or a checkpoint of
// reconciled results preserves the crashed owner's binding until the next
// explicit turn. Messages retains source chronology; ModelMessages is the
// derived, provider-valid (and potentially compacted) projection sent to a
// model — compaction never rewrites Messages.
type CheckpointState struct {
	SchemaVersion             int               `json:"schema_version"`
	SourceBinding             Binding           `json:"source_binding"`
	MuxRevision               string            `json:"mux_revision"`
	SessionID                 string            `json:"session_id"`
	TurnID                    string            `json:"turn_id"`
	LastCommittedSeq          uint64            `json:"last_committed_seq"`
	LastCommittedEventID      string            `json:"last_committed_event_id"`
	ActiveTurnSource          string            `json:"active_turn_source"`
	TerminalKind              string            `json:"terminal_kind"`
	AcceptedInputs            []InputState      `json:"accepted_inputs"`
	AppliedEvents             []EventDigest     `json:"applied_events"`
	ModelMessages             []llm.Message     `json:"model_messages"`
	Iteration                 int               `json:"iteration"`
	ConsecutiveToolIterations int               `json:"consecutive_tool_iterations"`
	JustCompacted             bool              `json:"just_compacted"`
	Usage                     UsageState        `json:"usage"`
	Messages                  []MessageState    `json:"messages"`
	Operations                []OperationState  `json:"operations"`
	Compactions               []CompactionState `json:"compactions"`
	Provenance                *ForkProvenance   `json:"provenance,omitempty"`
}

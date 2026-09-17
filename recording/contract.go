// ABOUTME: Core durable-recording value types: the trusted binding, the
// ABOUTME: Record/Snapshot wire shapes, the Recorder boundary, and typed errors.
//
// Package recording defines the shared value types for Mux's durable
// recording boundary: the host-trusted execution Binding, the Record a host
// recorder persists, the Snapshot pair committed alongside checkpoints, the
// Recorder interface a host implements, and the typed Error/ErrorKind
// vocabulary every recording function reports through. It depends only on
// llm and the standard library; it never imports agent or orchestrator, and
// carries no storage, admission, or Mux-loop implementation of its own.
package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// Binding is trusted host-supplied execution identity. It is never derived
// from model input.
type Binding struct {
	SessionID         string
	RuntimeInstanceID string
	ExecutionEpoch    uint64
}

// Record has no committed sequence: storage assigns one only inside its own
// transaction. Payload is the mux-json/1 canonical encoding of the typed
// payload named by Kind; see EncodePayload, EncodeRecord and ValidateRecord.
type Record struct {
	SchemaVersion     int             `json:"schema_version"`
	EventID           string          `json:"event_id"`
	SessionID         string          `json:"session_id"`
	RuntimeInstanceID string          `json:"runtime_instance_id"`
	ExecutionEpoch    uint64          `json:"execution_epoch"`
	Kind              string          `json:"kind"`
	OccurredAt        time.Time       `json:"occurred_at"`
	TurnID            string          `json:"turn_id"`
	MessageID         string          `json:"message_id,omitempty"`
	ToolCallID        string          `json:"tool_call_id,omitempty"`
	OperationID       string          `json:"operation_id,omitempty"`
	Payload           json.RawMessage `json:"payload"`
	BlobRef           string          `json:"blob_ref,omitempty"`
}

// Clone returns a Record whose Payload is independent of the receiver's, so
// a recorder queueing this value cannot see it mutate out from under it.
func (r Record) Clone() Record {
	r.Payload = bytes.Clone(r.Payload)
	return r
}

// Ticket means an immutable record was accepted, not that it was committed
// or made externally visible. Barrier turns a Ticket into a CommitReceipt.
type Ticket struct {
	SessionID string
	EventID   string
}

// CommitReceipt identifies a durable event by its assigned sequence, not a
// volatile queue position.
type CommitReceipt struct {
	SchemaVersion int       `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	EventID       string    `json:"event_id"`
	Seq           uint64    `json:"seq"`
	RecordSHA256  string    `json:"record_sha256"`
	CommittedAt   time.Time `json:"committed_at"`
	CheckpointID  string    `json:"checkpoint_id,omitempty"`
}

// Snapshot is produced at a Mux-internal safe point, without any callback
// into loop state. PrivateState is opaque to the host: it must be stored
// (and may be encrypted) but never parsed or included in a public log.
type Snapshot struct {
	CodecVersion   string
	SessionID      string
	ThroughEventID string
	PublicState    json.RawMessage
	PrivateState   []byte
}

// Clone returns a Snapshot whose buffers are independent of the receiver's,
// so nested serialized state cannot change while storage commits it.
func (s Snapshot) Clone() Snapshot {
	s.PublicState = bytes.Clone(s.PublicState)
	s.PrivateState = bytes.Clone(s.PrivateState)
	return s
}

// Recorder is run-bound: implementations own storage policy and apply their
// own backpressure. Append takes ownership of a deep copy before returning.
// Failed is a sticky failure signal that never clears. Barrier forces a
// flush of previously appended records. CommitCheckpoint atomically accepts
// a new Record/Snapshot pair, advancing durable state exactly once.
type Recorder interface {
	Append(context.Context, Record) (Ticket, error)
	Barrier(context.Context, Ticket) (CommitReceipt, error)
	CommitCheckpoint(context.Context, Record, Snapshot) (CommitReceipt, error)
	Failed() <-chan struct{}
	Err() error
}

// OperationDisposition is evidence supplied by the host about one historical
// operation, never inferred by replay. ResultEventID is required when
// Disposition is "result_known": it names the committed tool.result record
// that carries the reconciled outcome.
type OperationDisposition struct {
	OperationID                string `json:"operation_id"`
	Disposition                string `json:"disposition"`
	EvidenceRef                string `json:"evidence_ref"`
	MayRedispatchSameOperation bool   `json:"may_redispatch_same_operation"`
	ResultEventID              string `json:"result_event_id,omitempty"`
}

// RecoveryPlan does not execute anything; consuming it must stay
// side-effect-free.
type RecoveryPlan struct {
	SchemaVersion         int                    `json:"schema_version"`
	SessionID             string                 `json:"session_id"`
	NewExecutionEpoch     uint64                 `json:"new_execution_epoch"`
	LastCommittedSeq      uint64                 `json:"last_committed_seq"`
	ContinueAutomatically bool                   `json:"continue_automatically"`
	Operations            []OperationDisposition `json:"operations"`
}

// Clone returns a RecoveryPlan whose Operations is independent of the
// receiver's, preserving nil versus non-nil-empty. OperationDisposition
// holds only strings and a bool, so copying each element is already a full
// deep copy; slices.Clone does exactly that.
func (p RecoveryPlan) Clone() RecoveryPlan {
	p.Operations = slices.Clone(p.Operations)
	return p
}

// ErrorKind classifies an Error without requiring callers to parse its
// message. Tests and callers match on Kind via errors.As, never on message
// substrings.
type ErrorKind string

const (
	InvalidConfig     ErrorKind = "invalid_config"
	InvalidRecord     ErrorKind = "invalid_record"
	Unavailable       ErrorKind = "unavailable"
	StaleBinding      ErrorKind = "stale_binding"
	IdentityConflict  ErrorKind = "identity_conflict"
	OutcomeUncertain  ErrorKind = "outcome_uncertain"
	CorruptState      ErrorKind = "corrupt_state"
	UnsupportedCodec  ErrorKind = "unsupported_codec"
	IncompleteRestore ErrorKind = "incomplete_restore"
)

// Error is the one typed error every exported recording function reports
// through. Op names the failing operation (e.g. "EncodeRecord"); Kind
// classifies the failure; Cause, when set, is the underlying error. Every
// recording call site constructs Cause from short structural detail it
// wrote itself (a field name, a byte offset, an expected-versus-actual
// type) — never a record payload, snapshot state, or a wrapped
// provider/host error's own text. Callers needing the reason use errors.As
// and inspect Kind, not Error's message.
type Error struct {
	Kind  ErrorKind
	Op    string
	Cause error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("recording: %s: %s: %v", e.Op, e.Kind, e.Cause)
	}
	return fmt.Sprintf("recording: %s: %s", e.Op, e.Kind)
}

// Unwrap exposes Cause to errors.Is/errors.As.
func (e *Error) Unwrap() error { return e.Cause }

// Admission is the host's authorization for one provider call or tool
// dispatch, checked before the call proceeds.
type Admission struct {
	Binding     Binding
	Kind        string // "provider" or "tool"
	TurnID      string
	OperationID string
	ToolCallID  string
	Intent      CommitReceipt // zero for provider; committed intent for tool
}

// OperationRequest asks the host to allocate a fresh operation identity for
// a tool dispatch.
type OperationRequest struct {
	Binding       Binding
	TurnID        string
	ToolCallID    string
	Name          string
	RequestSHA256 string
}

// Config wires a durable run to its recorder and host callbacks. A zero
// CleanupTimeout disables cleanup writes; a negative one is invalid.
type Config struct {
	Binding        Binding
	Recorder       Recorder
	Admit          func(context.Context, Admission) error
	OperationID    func(context.Context, OperationRequest) (string, error)
	CleanupTimeout time.Duration
}

// Validate reports InvalidConfig for a Config that cannot be used: an empty
// session or runtime instance ID, a zero execution epoch, a nil (including
// typed-nil) Recorder, a nil Admit or OperationID callback, or a negative
// CleanupTimeout.
func (c Config) Validate() error {
	const op = "Config.Validate"
	switch {
	case c.Binding.SessionID == "":
		return &Error{Kind: InvalidConfig, Op: op, Cause: fmt.Errorf("binding session id is empty")}
	case c.Binding.RuntimeInstanceID == "":
		return &Error{Kind: InvalidConfig, Op: op, Cause: fmt.Errorf("binding runtime instance id is empty")}
	case c.Binding.ExecutionEpoch == 0:
		return &Error{Kind: InvalidConfig, Op: op, Cause: fmt.Errorf("binding execution epoch is zero")}
	case isNilInterfaceValue(c.Recorder):
		return &Error{Kind: InvalidConfig, Op: op, Cause: fmt.Errorf("recorder is nil")}
	case c.Admit == nil:
		return &Error{Kind: InvalidConfig, Op: op, Cause: fmt.Errorf("admit callback is nil")}
	case c.OperationID == nil:
		return &Error{Kind: InvalidConfig, Op: op, Cause: fmt.Errorf("operation id callback is nil")}
	case c.CleanupTimeout < 0:
		return &Error{Kind: InvalidConfig, Op: op, Cause: fmt.Errorf("cleanup timeout is negative")}
	}
	return nil
}

// isNilInterfaceValue reports whether v is nil either directly or as a
// typed-nil value (e.g. a nil *someRecorder assigned to a Recorder field)
// boxed inside a non-nil interface.
func isNilInterfaceValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

// TurnRequest starts or continues a durable turn. InputID is a
// host-persisted identity: an accepted user input, or a recovery request
// when Source is "recovery".
type TurnRequest struct {
	InputID string
	Text    string
	Source  string // "user" or "recovery"
}

// CommittedEnvelope is one entry in a session's committed tail, as read back
// by a reducer. Data is the exact stored bytes: EncodeRecord's output for
// Producer "mux", or the host's own encoding for Producer "host".
type CommittedEnvelope struct {
	Seq        uint64
	Producer   string // "mux" or "host"
	SessionID  string
	EventID    string
	Data       []byte
	DataSHA256 string
}

// Clone returns a CommittedEnvelope whose Data is independent of the
// receiver's, so a caller holding a committed tail cannot see an entry
// mutate out from under it.
func (e CommittedEnvelope) Clone() CommittedEnvelope {
	e.Data = bytes.Clone(e.Data)
	return e
}

// cloneCommittedEnvelopes clones every entry's Data so the result shares no
// backing array with envs, directly or through an element. A nil envs
// returns nil; a non-nil, empty envs returns a non-nil, empty slice.
func cloneCommittedEnvelopes(envs []CommittedEnvelope) []CommittedEnvelope {
	if envs == nil {
		return nil
	}
	cloned := make([]CommittedEnvelope, len(envs))
	for i, e := range envs {
		cloned[i] = e.Clone()
	}
	return cloned
}

// CommittedCheckpoint is a previously committed checkpoint read back from
// storage: the Snapshot taken at CheckpointID, plus ThroughRecord, the exact
// committed envelope the snapshot was taken through.
type CommittedCheckpoint struct {
	SchemaVersion    int
	CheckpointID     string
	MuxRevision      string
	JournalWatermark uint64
	StateSHA256      string
	Snapshot         Snapshot
	ThroughRecord    CommittedEnvelope
}

// Clone returns a CommittedCheckpoint fully independent of the receiver:
// Snapshot and ThroughRecord are each cloned, and every other field is a
// plain value with nothing to alias.
func (c CommittedCheckpoint) Clone() CommittedCheckpoint {
	c.Snapshot = c.Snapshot.Clone()
	c.ThroughRecord = c.ThroughRecord.Clone()
	return c
}

// RestoreInput is Restore's complete input: a checkpoint to resume from, the
// committed tail after it, and the caller's trusted context to resume into
// (Binding), the host record kinds it accepts (HostKinds), and the recovery
// plan governing in-flight operations. HostKinds distinguishes nil (no
// allowlist supplied) from a non-nil, empty slice (an allowlist that
// accepts no host kinds).
type RestoreInput struct {
	Checkpoint       CommittedCheckpoint
	Tail             []CommittedEnvelope
	LastCommittedSeq uint64
	HostKinds        []string
	Recovery         RecoveryPlan
	Binding          Binding
}

// Clone returns a RestoreInput fully independent of the receiver: Checkpoint,
// Tail, Recovery and HostKinds are each cloned, preserving nil versus
// non-nil-empty on Tail and HostKinds. Binding and LastCommittedSeq are
// plain values with nothing to alias.
func (r RestoreInput) Clone() RestoreInput {
	r.Checkpoint = r.Checkpoint.Clone()
	r.Tail = cloneCommittedEnvelopes(r.Tail)
	r.HostKinds = slices.Clone(r.HostKinds)
	r.Recovery = r.Recovery.Clone()
	return r
}

// RestoredState is Restore's complete output: the reduced CheckpointState,
// the raw committed tail it was derived from (RawTail), whether replay can
// continue automatically, and the recovery/source-checkpoint/binding/
// host-kind context carried over from the RestoreInput it was produced
// from.
type RestoredState struct {
	State            CheckpointState
	LastCommittedSeq uint64
	RawTail          []CommittedEnvelope
	CanContinue      bool
	Recovery         RecoveryPlan
	SourceCheckpoint CommittedCheckpoint
	Binding          Binding
	HostKinds        []string
}

// Clone returns a RestoredState whose RawTail, SourceCheckpoint, Recovery
// and HostKinds are independent of the receiver's, preserving nil versus
// non-nil-empty on RawTail and HostKinds. State is deliberately not deep
// cloned: CheckpointState's own deep clone is CloneCheckpointState (kata
// 1rky, over the llm.Message data it carries), and this package must not
// grow a second implementation of it. A caller must not mutate a clone's
// State expecting the receiver's State to be unaffected.
func (s RestoredState) Clone() RestoredState {
	s.RawTail = cloneCommittedEnvelopes(s.RawTail)
	s.SourceCheckpoint = s.SourceCheckpoint.Clone()
	s.HostKinds = slices.Clone(s.HostKinds)
	s.Recovery = s.Recovery.Clone()
	return s
}

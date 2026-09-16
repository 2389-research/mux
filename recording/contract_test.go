// ABOUTME: Tests for Record/Snapshot cloning, Config.Validate, and the
// ABOUTME: typed Error/isNilInterfaceValue helpers in contract.go.
package recording

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestRecordClone_NilPayloadStaysNil(t *testing.T) {
	r := Record{Payload: nil}
	c := r.Clone()
	if c.Payload != nil {
		t.Fatalf("nil Payload promoted to non-nil: %#v", c.Payload)
	}
}

func TestRecordClone_PayloadIsIndependentCopy(t *testing.T) {
	original := json.RawMessage(`{"a":1}`)
	r := Record{Payload: original}
	c := r.Clone()
	c.Payload[2] = 'X'
	if string(original) != `{"a":1}` {
		t.Fatalf("mutating the clone's Payload changed the original: %s", original)
	}
}

func TestSnapshotClone_NilBuffersStayNil(t *testing.T) {
	s := Snapshot{PublicState: nil, PrivateState: nil}
	c := s.Clone()
	if c.PublicState != nil || c.PrivateState != nil {
		t.Fatalf("nil Snapshot buffers promoted to non-nil: %#v", c)
	}
}

func TestSnapshotClone_BuffersAreIndependentCopies(t *testing.T) {
	pub := json.RawMessage(`{"a":1}`)
	priv := []byte("secret")
	s := Snapshot{PublicState: pub, PrivateState: priv}
	c := s.Clone()
	c.PublicState[2] = 'X'
	c.PrivateState[0] = 'X'
	if string(pub) != `{"a":1}` {
		t.Fatalf("mutating clone's PublicState changed the original: %s", pub)
	}
	if string(priv) != "secret" {
		t.Fatalf("mutating clone's PrivateState changed the original: %s", priv)
	}
}

func TestCommittedEnvelopeClone_NilDataStaysNil(t *testing.T) {
	e := CommittedEnvelope{Data: nil}
	c := e.Clone()
	if c.Data != nil {
		t.Fatalf("nil Data promoted to non-nil: %#v", c.Data)
	}
}

func TestCommittedEnvelopeClone_DataIsIndependentCopy(t *testing.T) {
	data := []byte("payload")
	e := CommittedEnvelope{Data: data}
	c := e.Clone()
	c.Data[0] = 'X'
	if string(data) != "payload" {
		t.Fatalf("mutating the clone's Data changed the original: %s", data)
	}
}

func TestCommittedCheckpointClone_NestedBuffersAreIndependentCopies(t *testing.T) {
	pub := json.RawMessage(`{"a":1}`)
	priv := []byte("secret")
	through := []byte("through-bytes")
	cp := CommittedCheckpoint{
		Snapshot:      Snapshot{PublicState: pub, PrivateState: priv},
		ThroughRecord: CommittedEnvelope{Data: through},
	}
	c := cp.Clone()
	c.Snapshot.PublicState[2] = 'X'
	c.Snapshot.PrivateState[0] = 'X'
	c.ThroughRecord.Data[0] = 'X'
	if string(pub) != `{"a":1}` || string(priv) != "secret" || string(through) != "through-bytes" {
		t.Fatalf("mutating the clone's nested buffers changed the original: pub=%s priv=%s through=%s", pub, priv, through)
	}
}

func TestRestoreInputClone_NilHostKindsStaysNil(t *testing.T) {
	r := RestoreInput{HostKinds: nil}
	c := r.Clone()
	if c.HostKinds != nil {
		t.Fatalf("nil HostKinds promoted to non-nil: %#v", c.HostKinds)
	}
}

// An empty, non-nil HostKinds means "accept no host kinds" -- a real
// allowlist that rejects everything. Collapsing it into nil ("no allowlist
// configured") would silently change what Restore accepts.
func TestRestoreInputClone_EmptyHostKindsStaysEmptyNotNil(t *testing.T) {
	r := RestoreInput{HostKinds: []string{}}
	c := r.Clone()
	if c.HostKinds == nil {
		t.Fatal("empty, non-nil HostKinds collapsed to nil")
	}
	if len(c.HostKinds) != 0 {
		t.Fatalf("expected empty HostKinds, got %#v", c.HostKinds)
	}
}

func TestRestoreInputClone_HostKindsIsIndependentCopy(t *testing.T) {
	kinds := []string{"app.note"}
	r := RestoreInput{HostKinds: kinds}
	c := r.Clone()
	c.HostKinds[0] = "changed"
	if kinds[0] != "app.note" {
		t.Fatalf("mutating the clone's HostKinds changed the original: %v", kinds)
	}
}

func TestRestoreInputClone_NilTailStaysNil(t *testing.T) {
	r := RestoreInput{Tail: nil}
	c := r.Clone()
	if c.Tail != nil {
		t.Fatalf("nil Tail promoted to non-nil: %#v", c.Tail)
	}
}

func TestRestoreInputClone_EmptyTailStaysEmptyNotNil(t *testing.T) {
	r := RestoreInput{Tail: []CommittedEnvelope{}}
	c := r.Clone()
	if c.Tail == nil {
		t.Fatal("empty, non-nil Tail collapsed to nil")
	}
	if len(c.Tail) != 0 {
		t.Fatalf("expected empty Tail, got %#v", c.Tail)
	}
}

func TestRestoreInputClone_TailElementDataIsIndependentCopy(t *testing.T) {
	data := []byte("host-row")
	r := RestoreInput{Tail: []CommittedEnvelope{{Data: data}}}
	c := r.Clone()
	c.Tail[0].Data[0] = 'X'
	if string(data) != "host-row" {
		t.Fatalf("mutating the clone's Tail[0].Data changed the original: %s", data)
	}
}

func TestRestoreInputClone_CheckpointBuffersAreIndependentCopies(t *testing.T) {
	priv := []byte("secret")
	r := RestoreInput{Checkpoint: CommittedCheckpoint{Snapshot: Snapshot{PrivateState: priv}}}
	c := r.Clone()
	c.Checkpoint.Snapshot.PrivateState[0] = 'X'
	if string(priv) != "secret" {
		t.Fatalf("mutating the clone's Checkpoint buffers changed the original: %s", priv)
	}
}

func TestRestoredStateClone_NilHostKindsStaysNil(t *testing.T) {
	s := RestoredState{HostKinds: nil}
	c := s.Clone()
	if c.HostKinds != nil {
		t.Fatalf("nil HostKinds promoted to non-nil: %#v", c.HostKinds)
	}
}

func TestRestoredStateClone_EmptyHostKindsStaysEmptyNotNil(t *testing.T) {
	s := RestoredState{HostKinds: []string{}}
	c := s.Clone()
	if c.HostKinds == nil {
		t.Fatal("empty, non-nil HostKinds collapsed to nil")
	}
	if len(c.HostKinds) != 0 {
		t.Fatalf("expected empty HostKinds, got %#v", c.HostKinds)
	}
}

func TestRestoredStateClone_HostKindsIsIndependentCopy(t *testing.T) {
	kinds := []string{"app.note"}
	s := RestoredState{HostKinds: kinds}
	c := s.Clone()
	c.HostKinds[0] = "changed"
	if kinds[0] != "app.note" {
		t.Fatalf("mutating the clone's HostKinds changed the original: %v", kinds)
	}
}

func TestRestoredStateClone_NilRawTailStaysNil(t *testing.T) {
	s := RestoredState{RawTail: nil}
	c := s.Clone()
	if c.RawTail != nil {
		t.Fatalf("nil RawTail promoted to non-nil: %#v", c.RawTail)
	}
}

func TestRestoredStateClone_EmptyRawTailStaysEmptyNotNil(t *testing.T) {
	s := RestoredState{RawTail: []CommittedEnvelope{}}
	c := s.Clone()
	if c.RawTail == nil {
		t.Fatal("empty, non-nil RawTail collapsed to nil")
	}
	if len(c.RawTail) != 0 {
		t.Fatalf("expected empty RawTail, got %#v", c.RawTail)
	}
}

func TestRestoredStateClone_RawTailElementDataIsIndependentCopy(t *testing.T) {
	data := []byte("host-row")
	s := RestoredState{RawTail: []CommittedEnvelope{{Data: data}}}
	c := s.Clone()
	c.RawTail[0].Data[0] = 'X'
	if string(data) != "host-row" {
		t.Fatalf("mutating the clone's RawTail[0].Data changed the original: %s", data)
	}
}

func TestRestoredStateClone_SourceCheckpointBuffersAreIndependentCopies(t *testing.T) {
	through := []byte("through-bytes")
	s := RestoredState{SourceCheckpoint: CommittedCheckpoint{ThroughRecord: CommittedEnvelope{Data: through}}}
	c := s.Clone()
	c.SourceCheckpoint.ThroughRecord.Data[0] = 'X'
	if string(through) != "through-bytes" {
		t.Fatalf("mutating the clone's SourceCheckpoint buffers changed the original: %s", through)
	}
}

// TestRestoreTypes_ConstructWithAllNamedFields mirrors mux#9cg6's
// checkpointInput fixture field-for-field, so these declarations cannot be
// an incompatible local alias of what Restore's own implementation already
// expects to construct.
func TestRestoreTypes_ConstructWithAllNamedFields(t *testing.T) {
	env := CommittedEnvelope{
		Seq: 7, Producer: "mux", SessionID: "s1", EventID: "cp-event",
		Data: []byte("record-bytes"), DataSHA256: "deadbeef",
	}
	checkpoint := CommittedCheckpoint{
		SchemaVersion: 1, CheckpointID: "cp1", MuxRevision: "4c64257",
		JournalWatermark: 7, StateSHA256: "abc123",
		Snapshot:      Snapshot{CodecVersion: SnapshotCodec, SessionID: "s1"},
		ThroughRecord: env,
	}
	input := RestoreInput{
		Checkpoint:       checkpoint,
		Tail:             []CommittedEnvelope{env},
		LastCommittedSeq: 7,
		HostKinds:        []string{"app.note"},
		Recovery:         RecoveryPlan{SchemaVersion: 1, SessionID: "s1", NewExecutionEpoch: 2, LastCommittedSeq: 7},
		Binding:          Binding{SessionID: "s1", RuntimeInstanceID: "r2", ExecutionEpoch: 2},
	}
	restored := RestoredState{
		State:            CheckpointState{SchemaVersion: 1, SessionID: "s1"},
		LastCommittedSeq: 7,
		RawTail:          []CommittedEnvelope{env},
		CanContinue:      true,
		Recovery:         input.Recovery,
		SourceCheckpoint: checkpoint,
		Binding:          input.Binding,
		HostKinds:        input.HostKinds,
	}
	if input.LastCommittedSeq != restored.LastCommittedSeq {
		t.Fatalf("fixture wiring mismatch: %d != %d", input.LastCommittedSeq, restored.LastCommittedSeq)
	}
}

func validConfig() Config {
	return Config{
		Binding:  Binding{SessionID: "s", RuntimeInstanceID: "r", ExecutionEpoch: 1},
		Recorder: fakeRecorder{},
		Admit:    func(context.Context, Admission) error { return nil },
		OperationID: func(context.Context, OperationRequest) (string, error) {
			return "op", nil
		},
		CleanupTimeout: time.Second,
	}
}

type fakeRecorder struct{}

func (fakeRecorder) Append(context.Context, Record) (Ticket, error) { return Ticket{}, nil }
func (fakeRecorder) Barrier(context.Context, Ticket) (CommitReceipt, error) {
	return CommitReceipt{}, nil
}
func (fakeRecorder) CommitCheckpoint(context.Context, Record, Snapshot) (CommitReceipt, error) {
	return CommitReceipt{}, nil
}
func (fakeRecorder) Failed() <-chan struct{} { return nil }
func (fakeRecorder) Err() error              { return nil }

func TestConfigValidate_AcceptsWellFormedConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestConfigValidate_RejectsEmptySessionID(t *testing.T) {
	c := validConfig()
	c.Binding.SessionID = ""
	assertKind(t, c.Validate(), InvalidConfig)
}

func TestConfigValidate_RejectsEmptyRuntimeInstanceID(t *testing.T) {
	c := validConfig()
	c.Binding.RuntimeInstanceID = ""
	assertKind(t, c.Validate(), InvalidConfig)
}

func TestConfigValidate_RejectsZeroExecutionEpoch(t *testing.T) {
	c := validConfig()
	c.Binding.ExecutionEpoch = 0
	assertKind(t, c.Validate(), InvalidConfig)
}

func TestConfigValidate_RejectsNilRecorder(t *testing.T) {
	c := validConfig()
	c.Recorder = nil
	assertKind(t, c.Validate(), InvalidConfig)
}

// nilFakeRecorder is a typed nil pointer to a Recorder implementation: the
// interface value holding it is itself non-nil, so a plain `== nil` check
// would miss it. isNilInterfaceValue exists precisely to catch this.
type nilFakeRecorder struct{ fakeRecorder }

func TestConfigValidate_RejectsTypedNilRecorder(t *testing.T) {
	var p *nilFakeRecorder
	c := validConfig()
	c.Recorder = p
	assertKind(t, c.Validate(), InvalidConfig)
}

func TestConfigValidate_RejectsNilAdmit(t *testing.T) {
	c := validConfig()
	c.Admit = nil
	assertKind(t, c.Validate(), InvalidConfig)
}

func TestConfigValidate_RejectsNilOperationID(t *testing.T) {
	c := validConfig()
	c.OperationID = nil
	assertKind(t, c.Validate(), InvalidConfig)
}

func TestConfigValidate_RejectsNegativeCleanupTimeout(t *testing.T) {
	c := validConfig()
	c.CleanupTimeout = -time.Second
	assertKind(t, c.Validate(), InvalidConfig)
}

func TestConfigValidate_AllowsZeroCleanupTimeout(t *testing.T) {
	c := validConfig()
	c.CleanupTimeout = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("zero CleanupTimeout should disable cleanup, not fail validation: %v", err)
	}
}

func TestIsNilInterfaceValue_PlainNil(t *testing.T) {
	if !isNilInterfaceValue(nil) {
		t.Fatal("plain nil not detected")
	}
}

func TestIsNilInterfaceValue_TypedNilPointer(t *testing.T) {
	var p *nilFakeRecorder
	if !isNilInterfaceValue(p) {
		t.Fatal("typed-nil pointer not detected")
	}
}

func TestIsNilInterfaceValue_NonNilValue(t *testing.T) {
	if isNilInterfaceValue(fakeRecorder{}) {
		t.Fatal("non-nil value reported as nil")
	}
}

func TestIsNilInterfaceValue_NonPointerKindNeverNil(t *testing.T) {
	// An int, string, etc. can never be nil regardless of its value; the
	// default branch must say so rather than panicking on IsNil().
	if isNilInterfaceValue(42) {
		t.Fatal("plain int reported as nil")
	}
}

func TestError_ErrorFormatsCauseWhenSet(t *testing.T) {
	e := &Error{Kind: InvalidRecord, Op: "EncodeRecord", Cause: errors.New("boom")}
	got := e.Error()
	if got != "recording: EncodeRecord: invalid_record: boom" {
		t.Fatalf("unexpected message: %s", got)
	}
}

func TestError_ErrorOmitsCauseWhenUnset(t *testing.T) {
	e := &Error{Kind: InvalidRecord, Op: "EncodeRecord"}
	got := e.Error()
	if got != "recording: EncodeRecord: invalid_record" {
		t.Fatalf("unexpected message: %s", got)
	}
}

func TestError_UnwrapExposesCauseToErrorsIs(t *testing.T) {
	sentinel := errors.New("sentinel")
	e := &Error{Kind: Unavailable, Op: "Append", Cause: sentinel}
	if !errors.Is(e, sentinel) {
		t.Fatal("errors.Is did not see through Unwrap to Cause")
	}
}

func TestError_ErrorsAsMatchesOnKind(t *testing.T) {
	err := error(&Error{Kind: StaleBinding, Op: "ValidateRecord"})
	var re *Error
	if !errors.As(err, &re) {
		t.Fatal("errors.As failed to match *Error")
	}
	if re.Kind != StaleBinding {
		t.Fatalf("Kind: got %s want %s", re.Kind, StaleBinding)
	}
}

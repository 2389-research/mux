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

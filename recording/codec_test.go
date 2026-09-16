// ABOUTME: Tests for the mux-json/1 codec: EncodePayload/EncodeRecord golden
// ABOUTME: byte fixtures, DecodeRecord/ValidateRecord, and RecordSHA256.
package recording

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestEncodePayload_ContractGoldenCases reproduces the three assertions the
// ac20 issue body gives verbatim: reordered keys converge and a >2^53
// integer survives without float rounding, invalid Go UTF-8 is rejected,
// and a duplicate raw JSON key is rejected.
func TestEncodePayload_ContractGoldenCases(t *testing.T) {
	a, err := EncodePayload(json.RawMessage(`{"b":2,"a":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != `{"a":9007199254740993,"b":2}` {
		t.Fatalf("unexpected canonical payload: %s", a)
	}
	if _, err := EncodePayload(map[string]any{"text": string([]byte{0xff})}); err == nil {
		t.Fatal("invalid Go UTF-8 accepted")
	}
	if _, err := EncodePayload(json.RawMessage(`{"a":1,"a":2}`)); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

// The following byte and hash literals were produced by running the
// verified implementation once (see the generator this task used, kept out
// of the module) and hand-verified before being pinned here: every fixture
// below was checked for no trailing newline (LAST_BYTE == '}'), and the
// Fixture2 bytes and SHA-256 were independently re-hashed with the system
// `shasum` binary, not just Go's own crypto/sha256, to rule out a
// self-consistent-but-wrong implementation.

func TestGolden_ToolIntentPayload_TypedValue_KeysSortedLargeIntPreserved(t *testing.T) {
	p := ToolIntentPayload{
		Name: "search_web",
		Arguments: map[string]any{
			"b":     2,
			"a":     json.Number("9007199254740993"),
			"query": "cats <and> dogs & birds",
		},
		RequestSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}
	got, err := EncodePayload(p)
	if err != nil {
		t.Fatalf("EncodePayload: %v", err)
	}
	const want = `{"arguments":{"a":9007199254740993,"b":2,"query":"cats \u003cand\u003e dogs \u0026 birds"},"name":"search_web","request_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}`
	if string(got) != want {
		t.Fatalf("canonical bytes mismatch:\ngot:  %s\nwant: %s", got, want)
	}
	if got[len(got)-1] == '\n' {
		t.Fatal("trailing newline present")
	}
	if err := ValidatePayload("tool.intent", got); err != nil {
		t.Fatalf("ValidatePayload(%q): %v", "tool.intent", err)
	}
}

func TestGolden_Record_FullRoundTrip_UTCAndSHA256(t *testing.T) {
	payload, err := EncodePayload(ToolIntentPayload{
		Name: "search_web",
		Arguments: map[string]any{
			"b":     2,
			"a":     json.Number("9007199254740993"),
			"query": "cats <and> dogs & birds",
		},
		RequestSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	})
	if err != nil {
		t.Fatalf("EncodePayload: %v", err)
	}
	loc := time.FixedZone("-0700", -7*60*60)
	rec := Record{
		SchemaVersion:     1,
		EventID:           "evt-0001",
		SessionID:         "sess-0001",
		RuntimeInstanceID: "rti-0001",
		ExecutionEpoch:    1,
		Kind:              "tool.intent",
		OccurredAt:        time.Date(2026, 9, 16, 10, 30, 0, 0, loc),
		TurnID:            "turn-0001",
		ToolCallID:        "call-0001",
		OperationID:       "op-0001",
		Payload:           payload,
	}
	if err := ValidatePayload(rec.Kind, rec.Payload); err != nil {
		t.Fatalf("ValidatePayload(%q): %v", rec.Kind, err)
	}

	got, err := EncodeRecord(rec)
	if err != nil {
		t.Fatalf("EncodeRecord: %v", err)
	}
	const want = `{"event_id":"evt-0001","execution_epoch":1,"kind":"tool.intent","occurred_at":"2026-09-16T17:30:00Z","operation_id":"op-0001","payload":{"arguments":{"a":9007199254740993,"b":2,"query":"cats \u003cand\u003e dogs \u0026 birds"},"name":"search_web","request_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},"runtime_instance_id":"rti-0001","schema_version":1,"session_id":"sess-0001","tool_call_id":"call-0001","turn_id":"turn-0001"}`
	if string(got) != want {
		t.Fatalf("canonical record bytes mismatch:\ngot:  %s\nwant: %s", got, want)
	}
	if got[len(got)-1] == '\n' {
		t.Fatal("trailing newline present")
	}

	const wantSHA = "abfd5bb13b67819105aa8c2f3ebaf1e2ad1faceb9b94dd9ab1eab2bb411e02fd"
	gotSHA, err := RecordSHA256(rec)
	if err != nil {
		t.Fatalf("RecordSHA256: %v", err)
	}
	if gotSHA != wantSHA {
		t.Fatalf("RecordSHA256: got %s want %s", gotSHA, wantSHA)
	}

	// Decode this exact fixture back and confirm OccurredAt round-trips as
	// the equivalent UTC instant, and every other field survives.
	decoded, err := DecodeRecord(got)
	if err != nil {
		t.Fatalf("DecodeRecord: %v", err)
	}
	if !decoded.OccurredAt.Equal(rec.OccurredAt) {
		t.Fatalf("OccurredAt: got %v want %v", decoded.OccurredAt, rec.OccurredAt)
	}
	if decoded.OccurredAt.Location() != time.UTC {
		t.Fatalf("OccurredAt location: got %v want UTC", decoded.OccurredAt.Location())
	}
	if decoded.EventID != rec.EventID || decoded.Kind != rec.Kind || decoded.SessionID != rec.SessionID {
		t.Fatalf("decoded identity fields do not match: %+v", decoded)
	}
}

func TestGolden_ArrayOrderPreserved(t *testing.T) {
	got, err := EncodePayload(json.RawMessage(`{"z":[3,1,2],"a":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":"x","z":[3,1,2]}` {
		t.Fatalf("array order not preserved: %s", got)
	}
}

func TestGolden_IntegerAndFloatStayDistinct(t *testing.T) {
	asInt, err := EncodePayload(json.RawMessage(`{"n":1}`))
	if err != nil {
		t.Fatal(err)
	}
	asFloat, err := EncodePayload(json.RawMessage(`{"n":1.0}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(asInt) != `{"n":1}` {
		t.Fatalf("integer literal mangled: %s", asInt)
	}
	if string(asFloat) != `{"n":1.0}` {
		t.Fatalf("float literal mangled: %s", asFloat)
	}
	if string(asInt) == string(asFloat) {
		t.Fatal("1 and 1.0 collapsed to the same canonical bytes")
	}
}

func TestGolden_WhitespaceRemovedHTMLEscapedStringContentUntouched(t *testing.T) {
	got, err := EncodePayload(json.RawMessage("{\n  \"html\" : \"<a>&</a>\",\n  \"note\": \"two  spaces\"\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"html":"\u003ca\u003e\u0026\u003c/a\u003e","note":"two  spaces"}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestGolden_EscapedSurrogatePairMatchesLiteralUTF8(t *testing.T) {
	literal, err := EncodePayload(json.RawMessage(`{"emoji":"😀"}`))
	if err != nil {
		t.Fatal(err)
	}
	escaped, err := EncodePayload(json.RawMessage(`{"emoji":"` + `\uD83D\uDE00` + `"}`))
	if err != nil {
		t.Fatalf("valid surrogate pair rejected: %v", err)
	}
	if string(literal) != string(escaped) {
		t.Fatalf("escaped and literal forms diverged: %s vs %s", escaped, literal)
	}
	if string(literal) != `{"emoji":"😀"}` {
		t.Fatalf("unexpected canonical bytes: %s", literal)
	}
}

func TestEncodePayload_RejectsUnpairedHighSurrogate(t *testing.T) {
	_, err := EncodePayload(json.RawMessage(`{"bad":"` + `\uD800` + `"}`))
	if err == nil {
		t.Fatal("unpaired high surrogate accepted")
	}
	if !strings.Contains(err.Error(), "surrogate") {
		t.Fatalf("error does not mention surrogate: %v", err)
	}
}

func TestEncodePayload_RejectsUnpairedLowSurrogate(t *testing.T) {
	_, err := EncodePayload(json.RawMessage(`{"bad":"` + `\uDC00` + `"}`))
	if err == nil {
		t.Fatal("unpaired low surrogate accepted")
	}
	if !strings.Contains(err.Error(), "surrogate") {
		t.Fatalf("error does not mention surrogate: %v", err)
	}
}

func TestEncodePayload_RejectsDuplicateKeyUnderDifferentEscaping(t *testing.T) {
	_, err := EncodePayload(json.RawMessage(`{"a":1,"` + `\u0061` + `":2}`))
	if err == nil {
		t.Fatal("duplicate key under different escaping accepted")
	}
}

func TestEncodePayload_RejectsTrailingContent(t *testing.T) {
	_, err := EncodePayload(json.RawMessage(`{"a":1} garbage`))
	if err == nil {
		t.Fatal("trailing content after top-level value accepted")
	}
}

func TestEncodePayload_RejectsNilValue(t *testing.T) {
	if _, err := EncodePayload(nil); err == nil {
		t.Fatal("nil payload value accepted")
	}
}

func TestEncodePayload_RejectsEmptyRawMessage(t *testing.T) {
	if _, err := EncodePayload(json.RawMessage(``)); err == nil {
		t.Fatal("empty raw payload accepted")
	}
}

func TestEncodePayload_RejectsCyclicMap(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	_, err := EncodePayload(cyclic)
	if err == nil {
		t.Fatal("cyclic map accepted")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("error does not mention cyclic: %v", err)
	}
}

func TestEncodePayload_RejectsCyclicSlice(t *testing.T) {
	cyclic := make([]any, 1)
	cyclic[0] = cyclic
	_, err := EncodePayload(cyclic)
	if err == nil {
		t.Fatal("cyclic slice accepted")
	}
}

// A cyclic value's address is still on the *current path* when it recurs, so
// withCycleGuard must reject it. A value merely referenced twice from
// sibling positions -- never nested inside itself -- must not trip the same
// guard: the guard has to release each address once its own subtree
// finishes, not leave it marked for the rest of the walk.
func TestEncodePayload_AcceptsSharedNonCyclicMapValue(t *testing.T) {
	shared := map[string]any{"x": 1}
	outer := map[string]any{"a": shared, "b": shared}
	if _, err := EncodePayload(outer); err != nil {
		t.Fatalf("map value shared between sibling keys (not a cycle) rejected: %v", err)
	}
}

func TestEncodePayload_RejectsInvalidUTF8MapKey(t *testing.T) {
	_, err := EncodePayload(map[string]any{string([]byte{0xff}): "v"})
	if err == nil {
		t.Fatal("invalid UTF-8 map key accepted")
	}
}

// unsupportedMarshaler implements json.Marshaler but is not time.Time or
// json.RawMessage: the codec must reject it rather than silently invoke
// arbitrary user-defined encoding logic.
type unsupportedMarshaler struct{}

func (unsupportedMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"x"`), nil }

func TestEncodePayload_RejectsUnsupportedMarshaler(t *testing.T) {
	_, err := EncodePayload(map[string]any{"v": unsupportedMarshaler{}})
	if err == nil {
		t.Fatal("unsupported custom marshaler accepted")
	}
	if !strings.Contains(err.Error(), "marshaler") {
		t.Fatalf("error does not mention marshaler: %v", err)
	}
}

func TestEncodePayload_AllowsTimeTime(t *testing.T) {
	got, err := EncodePayload(map[string]any{"at": time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)})
	if err != nil {
		t.Fatalf("time.Time rejected: %v", err)
	}
	if string(got) != `{"at":"2026-01-02T03:04:05Z"}` {
		t.Fatalf("unexpected time.Time encoding: %s", got)
	}
}

func TestEncodePayload_AllowsNestedRawMessage(t *testing.T) {
	got, err := EncodePayload(map[string]any{"nested": json.RawMessage(`{"b":2,"a":1}`)})
	if err != nil {
		t.Fatalf("nested json.RawMessage rejected: %v", err)
	}
	if string(got) != `{"nested":{"a":1,"b":2}}` {
		t.Fatalf("nested raw message not canonicalized: %s", got)
	}
}

func TestEncodePayload_RejectsInvalidUTF8InsideNestedRawMessage(t *testing.T) {
	bad := map[string]any{"nested": json.RawMessage(`{"text":"` + string([]byte{0xff}) + `"}`)}
	if _, err := EncodePayload(bad); err == nil {
		t.Fatal("invalid UTF-8 inside nested json.RawMessage accepted")
	}
}

func TestDecodeRecord_RejectsUnknownOuterField(t *testing.T) {
	data := []byte(`{"schema_version":1,"event_id":"e","session_id":"s","runtime_instance_id":"r","execution_epoch":1,"kind":"turn.started","occurred_at":"2026-01-01T00:00:00Z","turn_id":"t","payload":{"input_id":"i","source":"user","text":"hi"},"unexpected_field":true}`)
	if _, err := DecodeRecord(data); err == nil {
		t.Fatal("unknown outer field accepted")
	}
}

func TestDecodeRecord_RejectsDuplicateOuterKey(t *testing.T) {
	data := []byte(`{"schema_version":1,"schema_version":1,"event_id":"e","session_id":"s","runtime_instance_id":"r","execution_epoch":1,"kind":"turn.started","occurred_at":"2026-01-01T00:00:00Z","turn_id":"t","payload":{"input_id":"i","source":"user","text":"hi"}}`)
	if _, err := DecodeRecord(data); err == nil {
		t.Fatal("duplicate outer key accepted")
	}
}

func TestDecodeRecord_RejectsInvalidUTF8(t *testing.T) {
	data := []byte(`{"schema_version":1,"event_id":"e","session_id":"s","runtime_instance_id":"r","execution_epoch":1,"kind":"turn.started","occurred_at":"2026-01-01T00:00:00Z","turn_id":"` + "\xff" + `","payload":{}}`)
	if _, err := DecodeRecord(data); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestRecordSHA256_SamePayloadSameHash(t *testing.T) {
	rec := validTurnStartedRecord(t)
	h1, err := RecordSHA256(rec)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := RecordSHA256(rec)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %s vs %s", h1, h2)
	}
}

func TestRecordSHA256_DifferentPayloadDifferentHash(t *testing.T) {
	rec := validTurnStartedRecord(t)
	h1, err := RecordSHA256(rec)
	if err != nil {
		t.Fatal(err)
	}

	payload2, err := EncodePayload(TurnStartedPayload{InputID: "i2", Source: "user", Text: "different"})
	if err != nil {
		t.Fatal(err)
	}
	rec2 := rec
	rec2.Payload = payload2
	h2, err := RecordSHA256(rec2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("distinct payloads produced the same hash")
	}
}

func TestValidateRecord_AcceptsMatchingBinding(t *testing.T) {
	rec := validTurnStartedRecord(t)
	b := Binding{SessionID: rec.SessionID, RuntimeInstanceID: rec.RuntimeInstanceID, ExecutionEpoch: rec.ExecutionEpoch}
	if err := ValidateRecord(rec, b); err != nil {
		t.Fatalf("ValidateRecord: %v", err)
	}
}

func TestValidateRecord_StaleBindingOnSessionMismatch(t *testing.T) {
	rec := validTurnStartedRecord(t)
	b := Binding{SessionID: "other-session", RuntimeInstanceID: rec.RuntimeInstanceID, ExecutionEpoch: rec.ExecutionEpoch}
	assertKind(t, ValidateRecord(rec, b), StaleBinding)
}

func TestValidateRecord_StaleBindingOnRuntimeInstanceMismatch(t *testing.T) {
	rec := validTurnStartedRecord(t)
	b := Binding{SessionID: rec.SessionID, RuntimeInstanceID: "other-runtime", ExecutionEpoch: rec.ExecutionEpoch}
	assertKind(t, ValidateRecord(rec, b), StaleBinding)
}

func TestValidateRecord_StaleBindingOnExecutionEpochMismatch(t *testing.T) {
	rec := validTurnStartedRecord(t)
	b := Binding{SessionID: rec.SessionID, RuntimeInstanceID: rec.RuntimeInstanceID, ExecutionEpoch: rec.ExecutionEpoch + 1}
	assertKind(t, ValidateRecord(rec, b), StaleBinding)
}

func TestValidateRecord_InvalidRecordOnMissingTurnID(t *testing.T) {
	rec := validTurnStartedRecord(t)
	rec.TurnID = ""
	b := Binding{SessionID: rec.SessionID, RuntimeInstanceID: rec.RuntimeInstanceID, ExecutionEpoch: rec.ExecutionEpoch}
	assertKind(t, ValidateRecord(rec, b), InvalidRecord)
}

func TestValidateRecord_InvalidRecordOnUnknownKind(t *testing.T) {
	rec := validTurnStartedRecord(t)
	rec.Kind = "no.such.kind"
	b := Binding{SessionID: rec.SessionID, RuntimeInstanceID: rec.RuntimeInstanceID, ExecutionEpoch: rec.ExecutionEpoch}
	assertKind(t, ValidateRecord(rec, b), InvalidRecord)
}

func TestValidateRecord_InvalidRecordOnBadBlobRef(t *testing.T) {
	rec := validTurnStartedRecord(t)
	rec.BlobRef = "not-a-blob-ref"
	b := Binding{SessionID: rec.SessionID, RuntimeInstanceID: rec.RuntimeInstanceID, ExecutionEpoch: rec.ExecutionEpoch}
	assertKind(t, ValidateRecord(rec, b), InvalidRecord)
}

func TestValidateRecord_ToolKindRequiresToolCallIDAndOperationID(t *testing.T) {
	payload, err := EncodePayload(ToolIntentPayload{
		Name:          "search",
		Arguments:     map[string]any{},
		RequestSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{
		SchemaVersion: 1, EventID: "e", SessionID: "s", RuntimeInstanceID: "r",
		ExecutionEpoch: 1, Kind: "tool.intent", OccurredAt: time.Now().UTC(),
		TurnID: "t", Payload: payload,
		// ToolCallID and OperationID deliberately omitted.
	}
	b := Binding{SessionID: "s", RuntimeInstanceID: "r", ExecutionEpoch: 1}
	assertKind(t, ValidateRecord(rec, b), InvalidRecord)
}

func TestValidateRecord_MessageKindRequiresMessageID(t *testing.T) {
	payload, err := EncodePayload(MessageAbortedPayload{Reason: "interrupted"})
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{
		SchemaVersion: 1, EventID: "e", SessionID: "s", RuntimeInstanceID: "r",
		ExecutionEpoch: 1, Kind: "message.aborted", OccurredAt: time.Now().UTC(),
		TurnID: "t", Payload: payload,
		// MessageID deliberately omitted.
	}
	b := Binding{SessionID: "s", RuntimeInstanceID: "r", ExecutionEpoch: 1}
	assertKind(t, ValidateRecord(rec, b), InvalidRecord)
}

func TestValidateRecord_PropagatesPayloadValidationFailure(t *testing.T) {
	rec := validTurnStartedRecord(t)
	rec.Payload = json.RawMessage(`{"input_id":"i","source":"not-a-valid-source","text":"hi"}`)
	b := Binding{SessionID: rec.SessionID, RuntimeInstanceID: rec.RuntimeInstanceID, ExecutionEpoch: rec.ExecutionEpoch}
	assertKind(t, ValidateRecord(rec, b), InvalidRecord)
}

// validTurnStartedRecord builds a minimal Record that satisfies
// ValidateRecord's framing and payload checks, for tests that only care
// about one other dimension of validation.
func validTurnStartedRecord(t *testing.T) Record {
	t.Helper()
	payload, err := EncodePayload(TurnStartedPayload{InputID: "input-1", Source: "user", Text: "hello"})
	if err != nil {
		t.Fatalf("EncodePayload: %v", err)
	}
	return Record{
		SchemaVersion:     1,
		EventID:           "evt-1",
		SessionID:         "sess-1",
		RuntimeInstanceID: "rti-1",
		ExecutionEpoch:    1,
		Kind:              "turn.started",
		OccurredAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		TurnID:            "turn-1",
		Payload:           payload,
	}
}

// assertKind fails the test unless err is a *Error with the given Kind.
func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error of kind %s, got nil", want)
	}
	var re *Error
	if !errors.As(err, &re) {
		t.Fatalf("expected a *recording.Error, got %T: %v", err, err)
	}
	if re.Kind != want {
		t.Fatalf("Kind: got %s want %s", re.Kind, want)
	}
}

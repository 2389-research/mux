// ABOUTME: A focused contract test for CheckpointState's wire shape: no
// ABOUTME: bool/numeric field is ever omitted, and every tag is snake_case.
package recording

import (
	"encoding/json"
	"testing"
)

// TestCheckpointState_ZeroValueFieldsNotOmitted guards the property
// state.go's own doc comment declares deliberate: "every boolean/numeric
// field is encoded without omitempty, since a snapshot must distinguish
// 'false'/'0' from 'absent'." A future edit that adds `,omitempty` to one of
// these fields out of habit would silently make a real zero value
// indistinguishable from a missing one; this test fails the moment that
// happens.
func TestCheckpointState_ZeroValueFieldsNotOmitted(t *testing.T) {
	raw, err := json.Marshal(CheckpointState{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}

	for _, key := range []string{
		"schema_version", "last_committed_seq", "iteration",
		"consecutive_tool_iterations", "just_compacted",
	} {
		if _, ok := wire[key]; !ok {
			t.Errorf("zero-value field %q was omitted from the wire form: %s", key, raw)
		}
	}

	// Provenance is the one documented exception: a *ForkProvenance left nil
	// on an ordinary (non-fork-imported) checkpoint must be omitted, not
	// encoded as a null.
	if _, ok := wire["provenance"]; ok {
		t.Errorf(`"provenance" must be omitted when nil, not encoded as null: %s`, raw)
	}
}

// TestCheckpointState_TagsAreSnakeCase confirms the actual wire keys, not
// just the source's declared tags, so a typo'd tag fails here rather than
// silently shipping a differently-spelled field.
func TestCheckpointState_TagsAreSnakeCase(t *testing.T) {
	state := CheckpointState{
		SourceBinding: Binding{SessionID: "s", RuntimeInstanceID: "r", ExecutionEpoch: 1},
		SessionID:     "sess-1",
		TurnID:        "turn-1",
		Usage:         UsageState{InputTokens: 1},
		Messages:      []MessageState{{ID: "msg-1", TurnID: "turn-1"}},
		Operations:    []OperationState{{OperationID: "op-1"}},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}

	for _, key := range []string{
		"schema_version", "source_binding", "mux_revision", "session_id",
		"turn_id", "last_committed_seq", "last_committed_event_id",
		"active_turn_source", "terminal_kind", "accepted_inputs",
		"applied_events", "model_messages", "iteration",
		"consecutive_tool_iterations", "just_compacted", "usage",
		"messages", "operations", "compactions",
	} {
		if _, ok := wire[key]; !ok {
			t.Errorf("expected snake_case key %q, got wire form: %s", key, raw)
		}
	}
}

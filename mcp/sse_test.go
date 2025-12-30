package mcp

import (
	"strings"
	"testing"
)

func TestParseSSEEvents(t *testing.T) {
	input := `event: message
data: {"jsonrpc":"2.0","id":1,"result":{"tools":[]}}

event: message
data: {"jsonrpc":"2.0","method":"notifications/tools/changed"}

`
	reader := strings.NewReader(input)
	events, err := parseSSEEvents(reader)
	if err != nil {
		t.Fatalf("parseSSEEvents failed: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Event != "message" {
		t.Errorf("event 0: expected 'message', got %q", events[0].Event)
	}
	if events[0].Data != `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}` {
		t.Errorf("event 0: unexpected data: %s", events[0].Data)
	}
}

func TestParseSSEEventMultilineData(t *testing.T) {
	input := `event: message
data: {"jsonrpc":"2.0",
data: "id":1}

`
	reader := strings.NewReader(input)
	events, err := parseSSEEvents(reader)
	if err != nil {
		t.Fatalf("parseSSEEvents failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// Multi-line data should be concatenated with newlines
	expected := `{"jsonrpc":"2.0",
"id":1}`
	if events[0].Data != expected {
		t.Errorf("expected %q, got %q", expected, events[0].Data)
	}
}

func TestSSEEventEmpty(t *testing.T) {
	input := ``
	reader := strings.NewReader(input)
	events, err := parseSSEEvents(reader)
	if err != nil {
		t.Fatalf("parseSSEEvents failed: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

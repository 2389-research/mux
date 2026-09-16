// ABOUTME: Tests for typed stream delta/block metadata and tool-input parsing.
// ABOUTME: Covers parseStreamToolInput's accept/reject rules for streamed JSON.
package llm

import (
	"errors"
	"testing"
)

func TestParseStreamToolInput(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", "{", `{"x":1} {"y":2}`} {
		_, err := parseStreamToolInput("anthropic", "anthropic:3", raw)
		var protocol *StreamProtocolError
		if !errors.As(err, &protocol) {
			t.Fatalf("%q: expected protocol error, got %v", raw, err)
		}
	}
	got, err := parseStreamToolInput("anthropic", "anthropic:3", `{"city":"東京"}`)
	if err != nil || got["city"] != "東京" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

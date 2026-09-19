// ABOUTME: Pins that a large tool-input integer survives transcript
// ABOUTME: serialization in both the JSON and JSONL codecs without rounding.
package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/2389-research/mux/llm"
)

// bigID is one past float64's exact-integer range: a plain float64 decode
// rounds it down to ...000.
const bigID = "10000000000000001"

func TestTranscript_ToolInputLargeIntegerSurvivesRoundTrip(t *testing.T) {
	tr := NewTranscript("test-agent")
	tr.Append(llm.Message{Role: llm.RoleAssistant, Blocks: []llm.ContentBlock{
		{Type: llm.ContentTypeToolUse, ID: "tool_big", Name: "lookup",
			Input: map[string]any{"id": json.Number(bigID)}},
	}})

	codecs := []struct {
		name string
		save func(*Transcript, io.Writer) error
		load func(io.Reader) (*Transcript, error)
	}{
		{"JSON", (*Transcript).SaveJSON, LoadJSON},
		{"JSONL", (*Transcript).SaveJSONL, LoadJSONL},
	}

	for _, codec := range codecs {
		t.Run(codec.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := codec.save(tr, &buf); err != nil {
				t.Fatalf("save: %v", err)
			}
			loaded, err := codec.load(&buf)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			got := loaded.ToMessages()
			if len(got) != 1 || len(got[0].Blocks) != 1 {
				t.Fatalf("unexpected restored shape: %#v", got)
			}
			input := got[0].Blocks[0].Input
			value, ok := input["id"]
			if !ok {
				t.Fatalf("input has no id: %#v", input)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("marshal id: %v", err)
			}
			if string(encoded) != bigID {
				t.Fatalf("id = %s (%T), want %s", encoded, value, bigID)
			}
		})
	}
}

// ABOUTME: Tests for transcript persistence - serialization, deserialization, save/load.
// ABOUTME: Validates JSONL and JSON format handling and message round-tripping.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

var ctx = context.Background()

func TestNewTranscript(t *testing.T) {
	tr := NewTranscript("test-agent")

	if tr.AgentID != "test-agent" {
		t.Errorf("AgentID = %q, want %q", tr.AgentID, "test-agent")
	}
	if tr.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if len(tr.Entries) != 0 {
		t.Errorf("Entries len = %d, want 0", len(tr.Entries))
	}
}

func TestFromMessages(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}}},
		{Role: llm.RoleAssistant, Blocks: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hi"}}},
	}

	tr := FromMessages("test-agent", messages)

	if tr.AgentID != "test-agent" {
		t.Errorf("AgentID = %q, want %q", tr.AgentID, "test-agent")
	}
	if len(tr.Entries) != 2 {
		t.Errorf("Entries len = %d, want 2", len(tr.Entries))
	}
	if tr.Entries[0].Role != "user" {
		t.Errorf("Entry[0].Role = %q, want %q", tr.Entries[0].Role, "user")
	}
	if tr.Entries[1].Role != "assistant" {
		t.Errorf("Entry[1].Role = %q, want %q", tr.Entries[1].Role, "assistant")
	}
}

func TestTranscriptToMessages(t *testing.T) {
	tr := NewTranscript("test-agent")
	tr.Entries = []TranscriptEntry{
		{Timestamp: time.Now(), Role: "user", Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}}},
		{Timestamp: time.Now(), Role: "assistant", Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hi"}}},
	}

	messages := tr.ToMessages()

	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if messages[0].Role != llm.RoleUser {
		t.Errorf("messages[0].Role = %v, want user", messages[0].Role)
	}
	if messages[1].Role != llm.RoleAssistant {
		t.Errorf("messages[1].Role = %v, want assistant", messages[1].Role)
	}
	if messages[0].Blocks[0].Text != "hello" {
		t.Errorf("messages[0] text = %q, want %q", messages[0].Blocks[0].Text, "hello")
	}
}

func TestTranscriptAppend(t *testing.T) {
	tr := NewTranscript("test-agent")
	originalUpdatedAt := tr.UpdatedAt

	time.Sleep(1 * time.Millisecond) // Ensure time difference

	msg := llm.Message{Role: llm.RoleUser, Blocks: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "test"}}}
	tr.Append(msg)

	if tr.Len() != 1 {
		t.Errorf("Len() = %d, want 1", tr.Len())
	}
	if !tr.UpdatedAt.After(originalUpdatedAt) {
		t.Error("UpdatedAt should be updated after Append")
	}
}

// TestTranscriptTextRoundTrip is the issue's own reproduction (mux#bq4e):
// llm.NewUserMessage and NewAssistantMessage store their text in
// Message.Content, not Message.Blocks, and ordinary Run/Continue prompts go
// through exactly that constructor. A message mixing Content and Blocks
// must round-trip both independently — neither dropped nor folded into the
// other as a duplicate text block.
func TestTranscriptTextRoundTrip(t *testing.T) {
	want := []llm.Message{
		llm.NewUserMessage("critical user instruction"),
		llm.NewAssistantMessage("plain assistant reply"),
		{
			Role:    llm.RoleUser,
			Content: "content prefix",
			Blocks: []llm.ContentBlock{
				{Type: llm.ContentTypeText, Text: "block suffix"},
				{Type: llm.ContentTypeToolResult, ToolUseID: "call_1", Name: "read", Text: "result"},
			},
		},
	}

	tr := FromMessages("test-agent", want[:2])
	tr.Append(want[2])

	if got := tr.ToMessages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ToMessages (no serialization) =\n%#v\nwant\n%#v", got, want)
	}

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
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip =\n%#v\nwant\n%#v", got, want)
			}
		})
	}
}

func TestTranscriptSaveLoadJSON(t *testing.T) {
	tr := NewTranscript("test-agent")
	tr.Entries = []TranscriptEntry{
		{Timestamp: time.Now(), Role: "user", Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}}},
		{Timestamp: time.Now(), Role: "assistant", Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hi"}}},
	}

	// Save to buffer
	var buf bytes.Buffer
	err := tr.SaveJSON(&buf)
	if err != nil {
		t.Fatalf("SaveJSON error: %v", err)
	}

	// Load from buffer
	loaded, err := LoadJSON(&buf)
	if err != nil {
		t.Fatalf("LoadJSON error: %v", err)
	}

	if loaded.AgentID != tr.AgentID {
		t.Errorf("AgentID = %q, want %q", loaded.AgentID, tr.AgentID)
	}
	if len(loaded.Entries) != len(tr.Entries) {
		t.Errorf("Entries len = %d, want %d", len(loaded.Entries), len(tr.Entries))
	}
}

func TestTranscriptSaveLoadJSONL(t *testing.T) {
	tr := NewTranscript("test-agent")
	tr.Entries = []TranscriptEntry{
		{Timestamp: time.Now(), Role: "user", Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}}},
		{Timestamp: time.Now(), Role: "assistant", Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hi"}}},
	}

	// Save to buffer
	var buf bytes.Buffer
	err := tr.SaveJSONL(&buf)
	if err != nil {
		t.Fatalf("SaveJSONL error: %v", err)
	}

	// Load from buffer
	loaded, err := LoadJSONL(&buf)
	if err != nil {
		t.Fatalf("LoadJSONL error: %v", err)
	}

	if loaded.AgentID != tr.AgentID {
		t.Errorf("AgentID = %q, want %q", loaded.AgentID, tr.AgentID)
	}
	if len(loaded.Entries) != len(tr.Entries) {
		t.Errorf("Entries len = %d, want %d", len(loaded.Entries), len(tr.Entries))
	}
	if loaded.Entries[0].Role != "user" {
		t.Errorf("Entry[0].Role = %q, want user", loaded.Entries[0].Role)
	}
}

func TestTranscriptSaveLoadFile(t *testing.T) {
	tr := NewTranscript("test-agent")
	tr.Entries = []TranscriptEntry{
		{Timestamp: time.Now(), Role: "user", Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}}},
	}

	// Create temp file
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.json")

	// Save
	err := tr.SaveToFile(path)
	if err != nil {
		t.Fatalf("SaveToFile error: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("file was not created")
	}

	// Load
	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile error: %v", err)
	}

	if loaded.AgentID != tr.AgentID {
		t.Errorf("AgentID = %q, want %q", loaded.AgentID, tr.AgentID)
	}
}

func TestTranscriptSaveLoadFileJSONL(t *testing.T) {
	tr := NewTranscript("test-agent")
	tr.Entries = []TranscriptEntry{
		{Timestamp: time.Now(), Role: "user", Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}}},
	}

	// Create temp file
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")

	// Save
	err := tr.SaveToFileJSONL(path)
	if err != nil {
		t.Fatalf("SaveToFileJSONL error: %v", err)
	}

	// Load
	loaded, err := LoadFromFileJSONL(path)
	if err != nil {
		t.Fatalf("LoadFromFileJSONL error: %v", err)
	}

	if loaded.AgentID != tr.AgentID {
		t.Errorf("AgentID = %q, want %q", loaded.AgentID, tr.AgentID)
	}
}

// TestAgentSaveRestoreTranscript exercises the actual acceptance path named
// by mux#bq4e: a real Agent's Run() output (the plain-text Message.Content
// path every ordinary prompt uses) must survive SaveTranscript -> serialize
// -> deserialize -> RestoreTranscript into a fresh Agent, in both the JSON
// and JSONL formats. A length check alone is not enough: it is exactly how
// this bug shipped unnoticed, since a message that loses its Content but
// keeps its slot still passes a count comparison.
func TestAgentSaveRestoreTranscript(t *testing.T) {
	registry := tool.NewRegistry()
	client := asyncSpec{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "response"}},
		},
	}.client()

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	// Run to create some history: the user turn goes through
	// llm.NewUserMessage, so it carries Content, not Blocks.
	if err := agent.Run(ctx, "hello"); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	want := agent.Messages()

	// Save transcript
	tr := agent.SaveTranscript()
	if tr.AgentID != agent.ID() {
		t.Errorf("transcript AgentID = %q, want %q", tr.AgentID, agent.ID())
	}

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

			// Restore into a fresh agent - the real acceptance path.
			agent2 := New(Config{
				Name:      "test-agent-2",
				Registry:  registry,
				LLMClient: client,
			})
			if err := agent2.RestoreTranscript(loaded); err != nil {
				t.Fatalf("RestoreTranscript error: %v", err)
			}

			got := agent2.Messages()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("restored messages =\n%#v\nwant\n%#v", got, want)
			}
		})
	}
}

func TestLoadJSONL_EmptyLines(t *testing.T) {
	input := `{"type":"transcript_header","agent_id":"test"}

{"type":"message","role":"user","content":[{"type":"text","text":"hi"}]}

`
	loaded, err := LoadJSONL(bytes.NewBufferString(input))
	if err != nil {
		t.Fatalf("LoadJSONL error: %v", err)
	}

	if loaded.AgentID != "test" {
		t.Errorf("AgentID = %q, want %q", loaded.AgentID, "test")
	}
	if len(loaded.Entries) != 1 {
		t.Errorf("Entries len = %d, want 1", len(loaded.Entries))
	}
}

func TestLoadJSONL_UnknownType(t *testing.T) {
	input := `{"type":"transcript_header","agent_id":"test"}
{"type":"unknown_future_type","data":"something"}
{"type":"message","role":"user","content":[{"type":"text","text":"hi"}]}
`
	loaded, err := LoadJSONL(bytes.NewBufferString(input))
	if err != nil {
		t.Fatalf("LoadJSONL error: %v", err)
	}

	// Should skip unknown type and still load message
	if len(loaded.Entries) != 1 {
		t.Errorf("Entries len = %d, want 1", len(loaded.Entries))
	}
}

func TestTranscriptWithToolCalls(t *testing.T) {
	tr := NewTranscript("test-agent")
	tr.Entries = []TranscriptEntry{
		{
			Timestamp: time.Now(),
			Role:      "assistant",
			Content: []llm.ContentBlock{
				{Type: llm.ContentTypeText, Text: "Let me help"},
				{Type: llm.ContentTypeToolUse, ID: "tool_1", Name: "read_file", Input: map[string]any{"path": "/tmp/test"}},
			},
		},
		{
			Timestamp: time.Now(),
			Role:      "user",
			Content: []llm.ContentBlock{
				{Type: llm.ContentTypeToolResult, ToolUseID: "tool_1", Text: "file contents"},
			},
		},
	}

	// Round-trip through JSONL
	var buf bytes.Buffer
	err := tr.SaveJSONL(&buf)
	if err != nil {
		t.Fatalf("SaveJSONL error: %v", err)
	}

	loaded, err := LoadJSONL(&buf)
	if err != nil {
		t.Fatalf("LoadJSONL error: %v", err)
	}

	if len(loaded.Entries) != 2 {
		t.Fatalf("Entries len = %d, want 2", len(loaded.Entries))
	}

	// Verify tool call preserved
	if loaded.Entries[0].Content[1].Type != llm.ContentTypeToolUse {
		t.Errorf("Entry[0].Content[1].Type = %v, want tool_use", loaded.Entries[0].Content[1].Type)
	}
	if loaded.Entries[0].Content[1].Name != "read_file" {
		t.Errorf("Tool name = %q, want %q", loaded.Entries[0].Content[1].Name, "read_file")
	}

	// Verify tool result preserved
	if loaded.Entries[1].Content[0].Type != llm.ContentTypeToolResult {
		t.Errorf("Entry[1].Content[0].Type = %v, want tool_result", loaded.Entries[1].Content[0].Type)
	}
}

func TestTranscriptSaveFailurePreservesExistingFile(t *testing.T) {
	cases := []struct {
		name string
		save func(*Transcript, string) error
		load func(string) (*Transcript, error)
	}{
		{"JSON", (*Transcript).SaveToFile, LoadFromFile},
		{"JSONL", (*Transcript).SaveToFileJSONL, LoadFromFileJSONL},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTranscript("test-agent")
			tr.Entries = []TranscriptEntry{
				{Timestamp: time.Now(), Role: "user", Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}}},
			}

			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "transcript")

			if err := tc.save(tr, path); err != nil {
				t.Fatalf("initial save error: %v", err)
			}

			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read saved file: %v", err)
			}
			if len(before) == 0 {
				t.Fatal("initial saved file is empty")
			}

			// Put an unsupported value in the already-saved entry's
			// ContentBlock.Input so the encoder fails on the very entry
			// already on disk. For JSONL this guarantees the broken
			// partial write (header only) differs from "before"; a
			// header-only match masked the bug when the bad value was
			// appended as a later entry instead.
			tr.Entries[0].Content[0].Input = map[string]any{"unsupported": make(chan int)}

			if err := tc.save(tr, path); err == nil {
				t.Fatal("save with unsupported value should have failed")
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read file after failed save: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("previous file changed after failed save: before=%d bytes, after=%d bytes", len(before), len(after))
			}

			loaded, err := tc.load(path)
			if err != nil {
				t.Fatalf("load after failed save: %v", err)
			}
			if loaded.AgentID != "test-agent" {
				t.Errorf("AgentID = %q, want %q", loaded.AgentID, "test-agent")
			}
			if len(loaded.Entries) != 1 {
				t.Errorf("loaded Entries len = %d, want 1 (the pre-failure state)", len(loaded.Entries))
			}

			// The failed save must not leave a temp file behind in the directory.
			entries, err := os.ReadDir(tmpDir)
			if err != nil {
				t.Fatalf("read dir: %v", err)
			}
			if len(entries) != 1 {
				names := make([]string, len(entries))
				for i, e := range entries {
					names[i] = e.Name()
				}
				t.Errorf("dir has %d entries after failed save, want 1: %v", len(entries), names)
			}
		})
	}
}

// TestTranscriptJSONLRoundTripsLargeEntry covers a transcript entry whose single
// JSONL line exceeds bufio.Scanner's 64 KiB default. Provider replay envelopes and
// extended-thinking blocks both routinely produce entries that large, and the whole
// point of this file is resume: a transcript that saves but will not load back is
// the failure that matters.
func TestTranscriptJSONLRoundTripsLargeEntry(t *testing.T) {
	big := strings.Repeat("x", 100*1024)
	tr := NewTranscript("large-entry")
	tr.Append(llm.Message{
		Role:   llm.RoleAssistant,
		Blocks: []llm.ContentBlock{{Type: llm.ContentTypeThinking, Text: big}},
	})

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := tr.SaveToFileJSONL(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := LoadFromFileJSONL(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Len() != 1 {
		t.Fatalf("entries: got %d want 1", got.Len())
	}
	if text := got.Entries[0].Content[0].Text; text != big {
		t.Errorf("thinking text: got %d bytes want %d", len(text), len(big))
	}
}

// TestTranscriptEntryWireFormatIsFrozen is the recorded decision for
// mux#2zdv: agent.TranscriptEntry keeps its hand mirror of llm.Message as a
// deliberate, frozen on-disk wire format rather than embedding llm.Message
// directly. Embedding it would change the shape every external transcript
// reader sees, and that change needs the format-version field owned by
// mux#yp6a plus a coordinated breaking release; the widened Text field
// (mux#bq4e) already makes the mirror lossless for today's llm.Message, so
// no shape change is required to complete this item.
//
// This test makes the freeze actionable: it pins the exact serialized key
// set of an entry, so a field rename, addition, or removal fails here and
// forces the author to either re-record this decision deliberately or take
// the versioned shape via mux#yp6a. The entry carries the same keys in both
// codecs; the JSONL message record adds only its record "type".
func TestTranscriptEntryWireFormatIsFrozen(t *testing.T) {
	tr := NewTranscript("wire-format")
	tr.Entries = []TranscriptEntry{{
		Timestamp: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
		Role:      "assistant",
		Content:   []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "body"}},
		Text:      "body",
	}}

	// The JSON object form: each entry has exactly this key set.
	var buf bytes.Buffer
	if err := tr.SaveJSON(&buf); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}
	var decoded struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(decoded.Entries) != 1 {
		t.Fatalf("entries: got %d want 1", len(decoded.Entries))
	}
	assertFrozenKeys(t, "JSON entry", decoded.Entries[0],
		"timestamp", "role", "content", "text")

	// The JSONL record form: the same entry keys plus the record "type".
	buf.Reset()
	if err := tr.SaveJSONL(&buf); err != nil {
		t.Fatalf("SaveJSONL: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("JSONL lines: got %d want 2", len(lines))
	}
	var messageRecord map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[1]), &messageRecord); err != nil {
		t.Fatalf("decode JSONL message: %v", err)
	}
	assertFrozenKeys(t, "JSONL message record", messageRecord,
		"type", "timestamp", "role", "content", "text")
}

// assertFrozenKeys compares a decoded object's key set against want,
// ignoring order, and names the mux#2zdv decision in the failure so the
// next author knows the freeze is deliberate.
func assertFrozenKeys(t *testing.T, what string, obj map[string]json.RawMessage, want ...string) {
	t.Helper()
	got := make([]string, 0, len(obj))
	for k := range obj {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s keys = %v, want %v; the transcript wire format is frozen (mux#2zdv) - re-record this decision deliberately or take the versioned shape via mux#yp6a", what, got, want)
	}
}

// TestTranscriptEntryMirrorsEveryMessageField is the other half of the
// mux#2zdv guard: the mirror must map every JSON field llm.Message
// declares. It reads the struct's tags by reflection rather than marshaling
// a zero value, because an omitted `omitempty` field would hide behind the
// encoding and let a new field slip past. If llm.Message gains a field,
// this fails rather than letting transcripts silently drop it at the
// FromMessages/Append boundary - the exact failure mode that produced
// mux#bq4e. Resolving the failure is a decision: widen the mirror and
// re-record the freeze above, or move to the versioned shape (mux#yp6a).
func TestTranscriptEntryMirrorsEveryMessageField(t *testing.T) {
	// The mirror's mapping from llm.Message JSON fields to entry fields. An
	// unmapped llm.Message field is a silent drop, which this test rejects.
	mapped := map[string]bool{
		"role":    true, // TranscriptEntry.Role
		"content": true, // TranscriptEntry.Text
		"blocks":  true, // TranscriptEntry.Content
	}
	messageType := reflect.TypeOf(llm.Message{})
	for i := 0; i < messageType.NumField(); i++ {
		name := jsonFieldName(messageType.Field(i))
		if name == "" { // untagged or explicitly ignored
			continue
		}
		if !mapped[name] {
			t.Fatalf("llm.Message field %q has no TranscriptEntry mapping; the hand mirror would silently drop it (mux#2zdv)", name)
		}
	}
}

// jsonFieldName returns the JSON key a struct field serializes under, or ""
// when the field is unexported or tagged `json:"-"`.
func jsonFieldName(field reflect.StructField) string {
	if field.PkgPath != "" { // unexported
		return ""
	}
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	if name == "-" {
		return ""
	}
	if name == "" {
		return field.Name
	}
	return name
}

// ABOUTME: Tests for transcript persistence - serialization, deserialization, save/load.
// ABOUTME: Validates JSONL and JSON format handling and message round-tripping.
package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

func TestAgentSaveRestoreTranscript(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockLLMClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "response"}},
		},
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	// Run to create some history
	agent.Run(ctx, "hello")

	// Save transcript
	tr := agent.SaveTranscript()
	if tr.AgentID != agent.ID() {
		t.Errorf("transcript AgentID = %q, want %q", tr.AgentID, agent.ID())
	}

	// Create new agent and restore
	agent2 := New(Config{
		Name:      "test-agent-2",
		Registry:  registry,
		LLMClient: client,
	})

	err := agent2.RestoreTranscript(tr)
	if err != nil {
		t.Fatalf("RestoreTranscript error: %v", err)
	}

	// Verify messages restored
	messages := agent2.Messages()
	if len(messages) != len(agent.Messages()) {
		t.Errorf("restored messages len = %d, want %d", len(messages), len(agent.Messages()))
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

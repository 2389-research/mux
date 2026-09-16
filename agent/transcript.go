// ABOUTME: Implements transcript persistence for agent resume capability.
// ABOUTME: Provides JSONL serialization/deserialization of conversation history.
package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/2389-research/mux/llm"
)

// MaxTranscriptLineBytes is the per-line ceiling when reading JSONL transcripts.
// One line carries one entry's entire content, and provider replay envelopes and
// extended-thinking blocks routinely run past bufio.Scanner's 64 KiB default. That
// default makes an otherwise intact transcript impossible to resume from, so this
// matches the 16 MiB ceiling the MCP stdio transport uses for the same reason.
const MaxTranscriptLineBytes = 16 * 1024 * 1024

// TranscriptEntry represents a single entry in the transcript.
type TranscriptEntry struct {
	Timestamp time.Time          `json:"timestamp"`
	Role      string             `json:"role"`
	Content   []llm.ContentBlock `json:"content"`
}

// Transcript holds conversation history that can be saved and restored.
type Transcript struct {
	AgentID   string            `json:"agent_id"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Entries   []TranscriptEntry `json:"entries"`
}

// NewTranscript creates a new transcript for an agent.
func NewTranscript(agentID string) *Transcript {
	now := time.Now()
	return &Transcript{
		AgentID:   agentID,
		CreatedAt: now,
		UpdatedAt: now,
		Entries:   make([]TranscriptEntry, 0),
	}
}

// FromMessages creates a transcript from existing messages.
func FromMessages(agentID string, messages []llm.Message) *Transcript {
	t := NewTranscript(agentID)
	for _, msg := range messages {
		t.Entries = append(t.Entries, TranscriptEntry{
			Timestamp: time.Now(),
			Role:      string(msg.Role),
			Content:   msg.Blocks,
		})
	}
	return t
}

// ToMessages converts the transcript back to messages.
func (t *Transcript) ToMessages() []llm.Message {
	messages := make([]llm.Message, len(t.Entries))
	for i, entry := range t.Entries {
		messages[i] = llm.Message{
			Role:   llm.Role(entry.Role),
			Blocks: entry.Content,
		}
	}
	return messages
}

// Append adds a message to the transcript.
func (t *Transcript) Append(msg llm.Message) {
	t.Entries = append(t.Entries, TranscriptEntry{
		Timestamp: time.Now(),
		Role:      string(msg.Role),
		Content:   msg.Blocks,
	})
	t.UpdatedAt = time.Now()
}

// Len returns the number of entries in the transcript.
func (t *Transcript) Len() int {
	return len(t.Entries)
}

// SaveJSON writes the transcript as a single JSON object.
func (t *Transcript) SaveJSON(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(t)
}

// LoadJSON reads a transcript from a JSON object.
func LoadJSON(r io.Reader) (*Transcript, error) {
	var t Transcript
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&t); err != nil {
		return nil, fmt.Errorf("decode transcript: %w", err)
	}
	return &t, nil
}

// SaveJSONL writes the transcript in JSONL format (one entry per line).
// This format is better for streaming/appending.
func (t *Transcript) SaveJSONL(w io.Writer) error {
	encoder := json.NewEncoder(w)

	// Write header line
	header := struct {
		Type      string    `json:"type"`
		AgentID   string    `json:"agent_id"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}{
		Type:      "transcript_header",
		AgentID:   t.AgentID,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
	if err := encoder.Encode(header); err != nil {
		return fmt.Errorf("encode header: %w", err)
	}

	// Write each entry
	for _, entry := range t.Entries {
		line := struct {
			Type      string             `json:"type"`
			Timestamp time.Time          `json:"timestamp"`
			Role      string             `json:"role"`
			Content   []llm.ContentBlock `json:"content"`
		}{
			Type:      "message",
			Timestamp: entry.Timestamp,
			Role:      entry.Role,
			Content:   entry.Content,
		}
		if err := encoder.Encode(line); err != nil {
			return fmt.Errorf("encode entry: %w", err)
		}
	}

	return nil
}

// LoadJSONL reads a transcript from JSONL format.
func LoadJSONL(r io.Reader) (*Transcript, error) {
	t := &Transcript{
		Entries: make([]TranscriptEntry, 0),
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxTranscriptLineBytes)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Peek at the type field
		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &typeCheck); err != nil {
			return nil, fmt.Errorf("line %d: parse type: %w", lineNum, err)
		}

		switch typeCheck.Type {
		case "transcript_header":
			var header struct {
				AgentID   string    `json:"agent_id"`
				CreatedAt time.Time `json:"created_at"`
				UpdatedAt time.Time `json:"updated_at"`
			}
			if err := json.Unmarshal(line, &header); err != nil {
				return nil, fmt.Errorf("line %d: parse header: %w", lineNum, err)
			}
			t.AgentID = header.AgentID
			t.CreatedAt = header.CreatedAt
			t.UpdatedAt = header.UpdatedAt

		case "message":
			var entry struct {
				Timestamp time.Time          `json:"timestamp"`
				Role      string             `json:"role"`
				Content   []llm.ContentBlock `json:"content"`
			}
			if err := json.Unmarshal(line, &entry); err != nil {
				return nil, fmt.Errorf("line %d: parse message: %w", lineNum, err)
			}
			t.Entries = append(t.Entries, TranscriptEntry{
				Timestamp: entry.Timestamp,
				Role:      entry.Role,
				Content:   entry.Content,
			})

		default:
			// Unknown type - skip for forward compatibility
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read lines: %w", err)
	}

	return t, nil
}

// writeFileAtomic encodes to a temporary file created exclusively in path's
// directory, then renames it over path only once encoding, syncing, and
// closing that temporary file all succeed. A serialization or write
// failure never touches path, so any previously saved content there
// remains byte-for-byte intact and loadable.
func writeFileAtomic(path string, encode func(io.Writer) error) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpPath) // best-effort: don't leave an orphan temp file behind
		}
	}()

	if err = encode(f); err != nil {
		f.Close()
		return fmt.Errorf("encode: %w", err)
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	// Capture a close error so a failed flush on networked or buffered
	// filesystems surfaces instead of silently losing the trailing write.
	if err = f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// SaveToFile writes the transcript to a file (JSON format). The write is
// atomic: content is encoded to a temporary file in the destination
// directory first, and path is only replaced once that succeeds, so a
// serialization or write failure leaves any existing file at path intact.
// The file is written at mode 0600 (owner read/write only).
func (t *Transcript) SaveToFile(path string) error {
	if err := writeFileAtomic(path, t.SaveJSON); err != nil {
		return fmt.Errorf("save transcript: %w", err)
	}
	return nil
}

// LoadFromFile reads a transcript from a file (JSON format).
func LoadFromFile(path string) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	return LoadJSON(f)
}

// SaveToFileJSONL writes the transcript to a file (JSONL format). The write
// is atomic: content is encoded to a temporary file in the destination
// directory first, and path is only replaced once that succeeds, so a
// serialization or write failure leaves any existing file at path intact.
// The file is written at mode 0600 (owner read/write only).
func (t *Transcript) SaveToFileJSONL(path string) error {
	if err := writeFileAtomic(path, t.SaveJSONL); err != nil {
		return fmt.Errorf("save JSONL transcript: %w", err)
	}
	return nil
}

// LoadFromFileJSONL reads a transcript from a file (JSONL format).
func LoadFromFileJSONL(path string) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	return LoadJSONL(f)
}

// SaveTranscript saves the agent's current conversation history to a transcript.
func (a *Agent) SaveTranscript() *Transcript {
	messages := a.Messages()
	return FromMessages(a.ID(), messages)
}

// RestoreTranscript restores conversation history from a transcript.
// Returns an error if the transcript is for a different agent.
func (a *Agent) RestoreTranscript(t *Transcript) error {
	// Note: We don't enforce agent ID matching to allow flexible resume
	// Users can check t.AgentID themselves if needed
	a.SetMessages(t.ToMessages())
	return nil
}

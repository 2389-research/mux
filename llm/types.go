// ABOUTME: Defines the core types for LLM communication - messages, content
// ABOUTME: blocks, tool definitions, and tool use/results.
package llm

import "fmt"

// Role represents the role of a message sender.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ContentType identifies the type of content in a block.
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
	ContentTypeThinking   ContentType = "thinking"
)

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
)

// DefaultMaxTokens is the max output tokens used when a request does not
// specify MaxTokens. 16 384 is a safe floor across all major providers
// (Anthropic 64-128K, OpenAI 32K, Gemini 8-65K, Ollama varies).
const DefaultMaxTokens = 16384

// SourceKind identifies how media bytes are supplied.
type SourceKind string

const (
	SourceKindURL   SourceKind = "url"
	SourceKindBytes SourceKind = "bytes"
	SourceKindFile  SourceKind = "file"
)

// MediaSource describes where media bytes come from.
// For SourceKindFile, Bytes is populated eagerly at construction time;
// Path remains set for informational use (filenames on provider APIs, UI display).
type MediaSource struct {
	Kind  SourceKind `json:"kind"`
	URL   string     `json:"url,omitempty"`
	Bytes []byte     `json:"bytes,omitempty"`
	Path  string     `json:"path,omitempty"`
}

// Message represents a conversation message.
type Message struct {
	Role    Role           `json:"role"`
	Content string         `json:"content,omitempty"`
	Blocks  []ContentBlock `json:"blocks,omitempty"`
}

// NewUserMessage creates a user message with text content.
func NewUserMessage(text string) Message {
	return Message{Role: RoleUser, Content: text}
}

// NewAssistantMessage creates an assistant message with text content.
func NewAssistantMessage(text string) Message {
	return Message{Role: RoleAssistant, Content: text}
}

// ContentBlock represents a piece of content in a message.
type ContentBlock struct {
	Type ContentType `json:"type"`

	// For text content
	Text string `json:"text,omitempty"`

	// For tool use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// For tool result
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// For thinking content
	Thinking string `json:"thinking,omitempty"`

	// For media content (image, pdf, audio)
	Source    *MediaSource `json:"source,omitempty"`
	MediaType string       `json:"media_type,omitempty"`
}

// ToolDefinition describes a tool for the LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ThinkingConfig controls extended thinking / reasoning for providers that support it.
type ThinkingConfig struct {
	Enabled bool
	Budget  int // token budget; interpretation varies by provider
}

// Request is the input for CreateMessage.
type Request struct {
	Model       string           `json:"model,omitempty"`
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	System      string           `json:"system,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	Thinking    *ThinkingConfig  `json:"thinking,omitempty"`
}

// Response is the output from CreateMessage.
type Response struct {
	ID         string         `json:"id"`
	Content    []ContentBlock `json:"content"`
	StopReason StopReason     `json:"stop_reason"`
	Model      string         `json:"model"`
	Usage      Usage          `json:"usage"`
}

// Usage tracks token consumption.
type Usage struct {
	InputTokens    int `json:"input_tokens"`
	OutputTokens   int `json:"output_tokens"`
	ThinkingTokens int `json:"thinking_tokens,omitempty"`
}

// HasToolUse returns true if the response contains tool use blocks.
func (r *Response) HasToolUse() bool {
	for _, block := range r.Content {
		if block.Type == ContentTypeToolUse {
			return true
		}
	}
	return false
}

// ToolUses extracts all tool use blocks from the response.
func (r *Response) ToolUses() []ContentBlock {
	var uses []ContentBlock
	for _, block := range r.Content {
		if block.Type == ContentTypeToolUse {
			uses = append(uses, block)
		}
	}
	return uses
}

// TextContent extracts concatenated text from the response.
func (r *Response) TextContent() string {
	var text string
	for _, block := range r.Content {
		if block.Type == ContentTypeText {
			text += block.Text
		}
	}
	return text
}

// Capabilities describes which media types a provider supports as input.
type Capabilities struct {
	Image bool `json:"image"`
	PDF   bool `json:"pdf"`
	Audio bool `json:"audio"`
	Video bool `json:"video"`
}

// FullCapabilities returns a Capabilities with every media type enabled.
// Useful for test mocks that don't care about capability-gated behavior.
func FullCapabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: true, Video: true}
}

// ErrUnsupportedMedia indicates a provider cannot handle the requested media type at all.
type ErrUnsupportedMedia struct {
	Provider string
	Media    string
}

func (e *ErrUnsupportedMedia) Error() string {
	return fmt.Sprintf("%s does not support media type %q", e.Provider, e.Media)
}

// ErrUnsupportedSource indicates a provider supports the media type but not this source form.
type ErrUnsupportedSource struct {
	Provider string
	Media    string
	Kind     string
}

func (e *ErrUnsupportedSource) Error() string {
	return fmt.Sprintf("%s does not support source kind %q for media type %q", e.Provider, e.Kind, e.Media)
}

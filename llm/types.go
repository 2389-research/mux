// ABOUTME: Defines the core types for LLM communication - messages, content
// ABOUTME: blocks, tool definitions, and tool use/results.
package llm

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
)

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
)

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
}

// ToolDefinition describes a tool for the LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Request is the input for CreateMessage.
type Request struct {
	Model       string           `json:"model,omitempty"`
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	System      string           `json:"system,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
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
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
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

// ABOUTME: Defines the core types for LLM communication - messages, content
// ABOUTME: blocks, tool definitions, and tool use/results.
package llm

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

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
	ContentTypeImage      ContentType = "image"
	ContentTypePDF        ContentType = "pdf"
	ContentTypeAudio      ContentType = "audio"
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

// NewUserMessageWithBlocks constructs a user message from one or more content
// blocks. Use this when assembling multimodal messages (text + image, PDF, etc.).
func NewUserMessageWithBlocks(blocks ...ContentBlock) Message {
	return Message{Role: RoleUser, Blocks: blocks}
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

// NewImageFromURL constructs an image content block backed by a remote URL.
// MediaType is left empty; the remote server's Content-Type is authoritative.
func NewImageFromURL(url string) ContentBlock {
	return ContentBlock{
		Type:   ContentTypeImage,
		Source: &MediaSource{Kind: SourceKindURL, URL: url},
	}
}

// NewImageFromBytes constructs an image content block from inline bytes.
// mediaType must be an image/* MIME type; data must be non-empty.
func NewImageFromBytes(mediaType string, data []byte) (ContentBlock, error) {
	if err := validateMediaFamily("image", mediaType); err != nil {
		return ContentBlock{}, err
	}
	if len(data) == 0 {
		return ContentBlock{}, fmt.Errorf("NewImageFromBytes: data is empty")
	}
	return ContentBlock{
		Type:      ContentTypeImage,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindBytes, Bytes: data},
	}, nil
}

// NewImageFromFile reads an image file, infers its media type from the
// extension, and returns a ready-to-send content block.
func NewImageFromFile(path string) (ContentBlock, error) {
	data, mediaType, err := readMediaFile(path, "image")
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type:      ContentTypeImage,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindFile, Bytes: data, Path: path},
	}, nil
}

// NewPDFFromURL constructs a PDF content block backed by a remote URL.
// MediaType is set to "application/pdf" for consistency with the bytes/file
// forms; provider translators may still rely on the remote server's Content-Type.
func NewPDFFromURL(url string) ContentBlock {
	return ContentBlock{
		Type:      ContentTypePDF,
		MediaType: "application/pdf",
		Source:    &MediaSource{Kind: SourceKindURL, URL: url},
	}
}

// NewPDFFromBytes constructs a PDF content block from inline bytes.
// The media type is fixed to "application/pdf" (the only PDF MIME type),
// so no mediaType parameter is needed. data must be non-empty.
func NewPDFFromBytes(data []byte) (ContentBlock, error) {
	if len(data) == 0 {
		return ContentBlock{}, fmt.Errorf("NewPDFFromBytes: data is empty")
	}
	return ContentBlock{
		Type:      ContentTypePDF,
		MediaType: "application/pdf",
		Source:    &MediaSource{Kind: SourceKindBytes, Bytes: data},
	}, nil
}

// NewPDFFromFile reads a PDF file (extension must map to application/pdf) and
// returns a ready-to-send content block.
func NewPDFFromFile(path string) (ContentBlock, error) {
	data, mediaType, err := readMediaFile(path, "pdf")
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type:      ContentTypePDF,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindFile, Bytes: data, Path: path},
	}, nil
}

// NewAudioFromURL constructs an audio content block backed by a remote URL.
// MediaType is left empty; the remote server's Content-Type is authoritative.
func NewAudioFromURL(url string) ContentBlock {
	return ContentBlock{
		Type:   ContentTypeAudio,
		Source: &MediaSource{Kind: SourceKindURL, URL: url},
	}
}

// NewAudioFromBytes constructs an audio content block from inline bytes.
// mediaType must be an audio/* MIME type; data must be non-empty.
func NewAudioFromBytes(mediaType string, data []byte) (ContentBlock, error) {
	if err := validateMediaFamily("audio", mediaType); err != nil {
		return ContentBlock{}, err
	}
	if len(data) == 0 {
		return ContentBlock{}, fmt.Errorf("NewAudioFromBytes: data is empty")
	}
	return ContentBlock{
		Type:      ContentTypeAudio,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindBytes, Bytes: data},
	}, nil
}

// NewAudioFromFile reads an audio file (extension must map to an audio/* MIME
// type), infers its media type from the extension, and returns a
// ready-to-send content block.
func NewAudioFromFile(path string) (ContentBlock, error) {
	data, mediaType, err := readMediaFile(path, "audio")
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type:      ContentTypeAudio,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindFile, Bytes: data, Path: path},
	}, nil
}

// validateMediaFamily confirms mediaType starts with the expected family prefix
// (e.g. "image/", "audio/") or matches exactly for fixed types.
func validateMediaFamily(family, mediaType string) error {
	if family == "pdf" {
		if mediaType != "application/pdf" {
			return fmt.Errorf("media type %q is not application/pdf", mediaType)
		}
		return nil
	}
	prefix := family + "/"
	if !strings.HasPrefix(mediaType, prefix) {
		return fmt.Errorf("media type %q is not in family %s*", mediaType, prefix)
	}
	return nil
}

// readMediaFile reads a file, infers its media type from the extension, and
// validates the media type against the expected family.
func readMediaFile(path, family string) (data []byte, mediaType string, err error) {
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	ext := filepath.Ext(path)
	mediaType = mime.TypeByExtension(ext)
	if i := strings.Index(mediaType, ";"); i != -1 {
		mediaType = mediaType[:i]
	}
	if mediaType == "" {
		return nil, "", fmt.Errorf("readMediaFile %q: could not infer media type from extension %q", path, ext)
	}
	if err := validateMediaFamily(family, mediaType); err != nil {
		return nil, "", fmt.Errorf("readMediaFile %q: %w", path, err)
	}
	return data, mediaType, nil
}

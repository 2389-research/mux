// ABOUTME: Anthropic API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming message creation.
package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// AnthropicClient implements Client for the Anthropic API.
type AnthropicClient struct {
	client anthropic.Client
	model  string
}

// NewAnthropicClient creates a new Anthropic API client.
func NewAnthropicClient(apiKey, model string) *AnthropicClient {
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &AnthropicClient{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
	}
}

// NewAnthropicClientWithBaseURL creates an Anthropic API client with a custom base URL.
// Useful for proxies, API gateways, or compatible endpoints.
func NewAnthropicClientWithBaseURL(apiKey, model, baseURL string) *AnthropicClient {
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &AnthropicClient{
		client: anthropic.NewClient(opts...),
		model:  model,
	}
}

// anthropicSignedTypes are the content block types convertResponse stamps with
// a replay envelope and convertRequest can restore. They hold opaque bytes mux
// cannot rebuild: a thinking signature, or redacted thinking's encrypted data.
//
// Text and tool_use are deliberately absent, for three reasons: neither holds
// opaque bytes to preserve (a tool_use block is {type, id, name, input}); a
// caller's edits to assistant text or tool input have to reach the wire, and an
// envelope would silently discard them; and a history with no thinking in it
// has to survive a model switch, which an envelope would fail pre-flight.
//
// Their normalized fields are not byte-exact, though. ToolUse.Input is a
// map[string]any, so every JSON number decodes as a float64 and an integer past
// 2^53 rebuilds rounded: a wire input of 10000000000000001 replays as
// 10000000000000000. That predates replay envelopes and is unchanged by them.
// It is worth knowing because Anthropic invalidates the signature on every
// later thinking block when an earlier tool_use input changes.
//
// Anything else in a replay payload is rejected rather than sent as an empty
// block.
var anthropicSignedTypes = map[string]bool{
	"thinking":          true,
	"redacted_thinking": true,
}

// anthropicBlockReplay returns the envelope for a block whose bytes must reach
// the API unmodified, and nil for one mux can rebuild. A block the SDK did not
// decode from the wire (hand-built in a test, or a stream snapshot) has no raw
// JSON to preserve, so it is left unstamped rather than stamped empty.
func anthropicBlockReplay(block anthropic.ContentBlockUnion, requestModel string) *ProviderReplay {
	if !anthropicSignedTypes[block.Type] {
		return nil
	}
	raw := block.RawJSON()
	if raw == "" {
		return nil
	}
	return &ProviderReplay{
		Provider: "anthropic",
		Model:    requestModel,
		Data:     json.RawMessage(raw),
	}
}

// convertRequest converts our Request to Anthropic's MessageNewParams.
//
// A block carrying a Replay envelope is restored from its raw provider JSON
// and the normalized fields are ignored: Anthropic rejects thinking blocks
// whose text or signature changed, so the bytes the API sent are the only
// thing safe to send back.
func convertRequest(req *Request) (anthropic.MessageNewParams, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
	}

	// Enable extended thinking if configured
	if req.Thinking != nil && req.Thinking.Enabled {
		if int64(req.MaxTokens) < int64(req.Thinking.Budget) {
			params.MaxTokens = int64(req.Thinking.Budget)
		}
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{
				BudgetTokens: int64(req.Thinking.Budget),
			},
		}
	}

	// Convert messages
	messages := make([]anthropic.MessageParam, 0, len(req.Messages))
	for _, msg := range req.Messages {
		var content []anthropic.ContentBlockParamUnion
		if msg.Content != "" {
			content = append(content, anthropic.NewTextBlock(msg.Content))
		}
		for _, block := range msg.Blocks {
			if block.Replay != nil {
				restored, err := restoreAnthropicBlock(block.Replay)
				if err != nil {
					return anthropic.MessageNewParams{}, err
				}
				content = append(content, restored)
				continue
			}
			switch block.Type {
			case ContentTypeText:
				content = append(content, anthropic.NewTextBlock(block.Text))
			case ContentTypeToolUse:
				// Serialize assistant's tool_use blocks for conversation history
				content = append(content, anthropic.NewToolUseBlock(block.ID, block.Input, block.Name))
			case ContentTypeToolResult:
				content = append(content, anthropic.NewToolResultBlock(block.ToolUseID, block.Text, block.IsError))
			case ContentTypeImage:
				content = append(content, convertAnthropicImage(block))
			case ContentTypePDF:
				content = append(content, convertAnthropicPDF(block))
			}
		}
		messages = append(messages, anthropic.MessageParam{
			Role:    anthropic.MessageParamRole(msg.Role),
			Content: content,
		})
	}
	params.Messages = messages

	// Set system prompt
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	// Convert tools
	if len(req.Tools) > 0 {
		tools := make([]anthropic.ToolUnionParam, 0, len(req.Tools))
		for _, tool := range req.Tools {
			inputSchema := anthropic.ToolInputSchemaParam{}

			// Extract properties from the schema
			if props, ok := tool.InputSchema["properties"]; ok {
				inputSchema.Properties = props
			}

			// Extract required fields from the schema
			if req, ok := tool.InputSchema["required"]; ok {
				if reqSlice, ok := req.([]string); ok {
					inputSchema.Required = reqSlice
				} else if reqSlice, ok := req.([]any); ok {
					// Handle []any (common from JSON unmarshal)
					required := make([]string, 0, len(reqSlice))
					for _, r := range reqSlice {
						if s, ok := r.(string); ok {
							required = append(required, s)
						} else {
							fmt.Fprintf(os.Stderr, "Warning: failed to convert required field element to string for tool %s: got %T\n", tool.Name, r)
						}
					}
					inputSchema.Required = required
				} else {
					fmt.Fprintf(os.Stderr, "Warning: failed to convert required field to []string or []any for tool %s: got %T\n", tool.Name, req)
				}
			}

			toolParam := anthropic.ToolParam{
				Name:        tool.Name,
				Description: param.NewOpt(tool.Description),
				InputSchema: inputSchema,
			}
			tools = append(tools, anthropic.ToolUnionParam{OfTool: &toolParam})
		}
		params.Tools = tools
	}

	return params, nil
}

// restoreAnthropicBlock rebuilds the SDK content block param from a replay
// envelope's raw JSON, so signed thinking and redacted thinking reach the API
// exactly as they left it.
func restoreAnthropicBlock(replay *ProviderReplay) (anthropic.ContentBlockParamUnion, error) {
	var item anthropic.ContentBlockUnion
	if err := json.Unmarshal(replay.Data, &item); err != nil {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("anthropic: decoding replay block: %w", err)
	}
	if !anthropicSignedTypes[item.Type] {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("anthropic: replay block has unsupported type %q", item.Type)
	}
	return item.ToParam(), nil
}

// convertResponse converts Anthropic's Message to our Response.
//
// A thinking or redacted_thinking block carries a replay envelope holding the
// block's unmodified JSON, pinned to requestModel (the effective requested
// model, not the returned snapshot name). That is what lets a thinking block
// and its signature survive a tool turn. Text and tool_use are left normalized
// — see anthropicSignedTypes. redacted_thinking has no readable text, so it
// becomes a replay-only block: its encrypted data stays in the envelope and
// never reaches display text.
func convertResponse(msg *anthropic.Message, requestModel string) *Response {
	resp := &Response{
		ID:         msg.ID,
		Model:      string(msg.Model),
		StopReason: mapAnthropicStopReason(msg.StopReason),
		Usage: Usage{
			InputTokens:  int(msg.Usage.InputTokens),
			OutputTokens: int(msg.Usage.OutputTokens),
		},
	}

	for _, block := range msg.Content {
		replay := anthropicBlockReplay(block, requestModel)
		switch block.Type {
		case "text":
			resp.Content = append(resp.Content, ContentBlock{
				Type: ContentTypeText,
				Text: block.Text,
			})
		case "thinking":
			resp.Content = append(resp.Content, ContentBlock{
				Type:     ContentTypeThinking,
				Thinking: block.Thinking,
				Replay:   replay,
			})
		case "redacted_thinking":
			// Redacted thinking has nothing to display, so it is worth
			// carrying only while its encrypted bytes can be replayed.
			if replay == nil {
				continue
			}
			resp.Content = append(resp.Content, ContentBlock{
				Type:   ContentTypeReplay,
				Replay: replay,
			})
		case "tool_use":
			// Unmarshal the raw JSON input to map[string]any
			var input map[string]any
			if block.Input != nil {
				if err := json.Unmarshal(block.Input, &input); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to parse tool input for %s: %v\n", block.Name, err)
					input = make(map[string]any)
				}
			}
			resp.Content = append(resp.Content, ContentBlock{
				Type:  ContentTypeToolUse,
				ID:    block.ID,
				Name:  block.Name,
				Input: input,
			})
		}
	}

	return resp
}

// CreateMessage sends a message and returns the complete response.
func (a *AnthropicClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	if req.Model == "" {
		req.Model = a.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}
	if err := validateRequest("anthropic", a.Capabilities(), req); err != nil {
		return nil, err
	}
	if err := validateReplay("anthropic", req.Model, req.Messages); err != nil {
		return nil, err
	}

	params, err := convertRequest(req)
	if err != nil {
		return nil, err
	}
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	return convertResponse(msg, req.Model), nil
}

type anthropicStreamBlock struct {
	block    ContentBlock
	inputRaw string
	// rawType is the provider's own block type ("thinking",
	// "redacted_thinking", ...), kept because several of them normalize onto
	// the same mux ContentType.
	rawType string
	// signature accumulates signature_delta fragments for a thinking block.
	// It is never appended to displayed text.
	signature string
	// redactedData is the encrypted payload of a redacted_thinking block.
	redactedData string
}

type anthropicStreamAccumulator struct {
	response *Response
	blocks   map[int]*anthropicStreamBlock
	// model is the effective requested model, stamped on replay envelopes.
	model string
}

func newAnthropicStreamAccumulator(model string) *anthropicStreamAccumulator {
	return &anthropicStreamAccumulator{blocks: make(map[int]*anthropicStreamBlock), model: model}
}

func (a *anthropicStreamAccumulator) start(msg *anthropic.Message) *Response {
	a.response = convertResponse(msg, a.model)
	a.response.Content = nil
	return a.response
}

func (a *anthropicStreamAccumulator) startBlock(index int, rawType string, id string, name string, text string, thinking string, signature string, redactedData string) {
	blockType := ContentType(rawType)
	if rawType == "redacted_thinking" {
		// No readable text: it exists only to be replayed.
		blockType = ContentTypeReplay
	}
	block := ContentBlock{
		Type:     blockType,
		ID:       id,
		Name:     name,
		Text:     text,
		Thinking: thinking,
	}
	a.blocks[index] = &anthropicStreamBlock{
		block:        block,
		rawType:      rawType,
		signature:    signature,
		redactedData: redactedData,
	}
}

func (a *anthropicStreamAccumulator) appendDelta(index int, deltaType string, text string) {
	block, ok := a.blocks[index]
	if !ok {
		return
	}
	switch deltaType {
	case "text_delta":
		block.block.Text += text
	case "thinking_delta":
		block.block.Thinking += text
	case "signature_delta":
		block.signature += text
	case "input_json_delta":
		block.inputRaw += text
	}
}

// finalizeThinkingReplay attaches the replay envelope for a completed thinking
// or redacted_thinking block, rebuilt from the exact accumulated deltas. A
// thinking block that never received its signature (a stream cut short) gets
// no envelope: replaying an unsigned block would be rejected, and mux does not
// fabricate signatures.
func (a *anthropicStreamAccumulator) finalizeThinkingReplay(block *anthropicStreamBlock) error {
	var raw []byte
	var err error
	switch block.rawType {
	case "thinking":
		if block.signature == "" {
			return nil
		}
		raw, err = json.Marshal(struct {
			Type      string `json:"type"`
			Thinking  string `json:"thinking"`
			Signature string `json:"signature"`
		}{"thinking", block.block.Thinking, block.signature})
	case "redacted_thinking":
		if block.redactedData == "" {
			return nil
		}
		raw, err = json.Marshal(struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}{"redacted_thinking", block.redactedData})
	default:
		return nil
	}
	if err != nil {
		return fmt.Errorf("anthropic: encoding streamed %s block: %w", block.rawType, err)
	}
	block.block.Replay = &ProviderReplay{
		Provider: "anthropic",
		Model:    a.model,
		Data:     raw,
	}
	return nil
}

func (a *anthropicStreamAccumulator) stopBlock(index int) error {
	block, ok := a.blocks[index]
	if !ok {
		return nil
	}
	if block.block.Type != ContentTypeToolUse {
		return a.finalizeThinkingReplay(block)
	}
	if block.inputRaw == "" {
		block.block.Input = make(map[string]any)
		return nil
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(block.inputRaw), &input); err != nil {
		// Truncated input (a max_tokens stop mid tool input leaves inputRaw
		// as partial JSON): drop the block from the finished Response so the
		// orchestrator never executes a partial call with empty input as if
		// it were complete.
		fmt.Fprintf(os.Stderr, "Warning: failed to parse streamed tool input for %s: %v\n", block.block.Name, err)
		delete(a.blocks, index)
		return nil
	}
	block.block.Input = input
	return nil
}

func (a *anthropicStreamAccumulator) mergeDelta(stopReason StopReason, usage Usage) *Response {
	if a.response == nil {
		a.response = &Response{}
	}
	if stopReason != "" {
		a.response.StopReason = stopReason
	}
	if usage.OutputTokens > 0 {
		a.response.Usage.OutputTokens = usage.OutputTokens
	}
	return &Response{StopReason: stopReason, Usage: usage}
}

func (a *anthropicStreamAccumulator) finish() *Response {
	if a.response == nil {
		a.response = &Response{}
	}
	indexes := make([]int, 0, len(a.blocks))
	for index := range a.blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	a.response.Content = make([]ContentBlock, 0, len(indexes))
	for _, index := range indexes {
		a.response.Content = append(a.response.Content, a.blocks[index].block)
	}
	return a.response
}

func cloneResponse(response *Response) *Response {
	if response == nil {
		return nil
	}
	clone := *response
	if len(response.Content) > 0 {
		clone.Content = make([]ContentBlock, len(response.Content))
		for i, block := range response.Content {
			clone.Content[i] = block
			if block.Input != nil {
				clone.Content[i].Input = make(map[string]any, len(block.Input))
				for key, value := range block.Input {
					clone.Content[i].Input[key] = value
				}
			}
			if block.Replay != nil {
				replay := *block.Replay
				replay.Data = bytes.Clone(block.Replay.Data)
				clone.Content[i].Replay = &replay
			}
		}
	}
	return &clone
}

// CreateMessageStream sends a message and returns a channel of streaming events.
func (a *AnthropicClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	if req.Model == "" {
		req.Model = a.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}
	if err := validateRequest("anthropic", a.Capabilities(), req); err != nil {
		return nil, err
	}
	if err := validateReplay("anthropic", req.Model, req.Messages); err != nil {
		return nil, err
	}

	params, err := convertRequest(req)
	if err != nil {
		return nil, err
	}
	stream := a.client.Messages.NewStreaming(ctx, params)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer stream.Close()
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in CreateMessageStream: %v\n", r)
				sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("panic in stream processing: %v", r),
				})
			}
			close(eventChan)
		}()

		acc := newAnthropicStreamAccumulator(req.Model)
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "message_start":
				response := acc.start(&event.Message)
				if !sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:     EventMessageStart,
					Response: cloneResponse(response),
				}) {
					return
				}
			case "content_block_start":
				se := StreamEvent{
					Type:  EventContentStart,
					Index: int(event.Index),
				}
				// Populate Block so consumers can distinguish text from tool_use
				if event.ContentBlock.Type != "" {
					acc.startBlock(int(event.Index), event.ContentBlock.Type, event.ContentBlock.ID, event.ContentBlock.Name, event.ContentBlock.Text, event.ContentBlock.Thinking, event.ContentBlock.Signature, event.ContentBlock.Data)
					se.Block = &ContentBlock{
						Type: acc.blocks[int(event.Index)].block.Type,
						ID:   event.ContentBlock.ID,
						Name: event.ContentBlock.Name,
					}
				}
				if !sendStreamEvent(ctx, eventChan, se) {
					return
				}
			case "content_block_delta":
				var text string
				switch event.Delta.Type {
				case "text_delta":
					text = event.Delta.Text
				case "input_json_delta":
					text = event.Delta.PartialJSON
				case "thinking_delta":
					text = event.Delta.Thinking
				}
				// The signature is opaque provider state, not display text: it
				// goes to the accumulator only, never into StreamEvent.Text.
				if event.Delta.Type == "signature_delta" {
					acc.appendDelta(int(event.Index), event.Delta.Type, event.Delta.Signature)
				} else {
					acc.appendDelta(int(event.Index), event.Delta.Type, text)
				}
				if !sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:  EventContentDelta,
					Index: int(event.Index),
					Text:  text,
				}) {
					return
				}
			case "content_block_stop":
				if err := acc.stopBlock(int(event.Index)); err != nil {
					sendStreamEvent(ctx, eventChan, StreamEvent{Type: EventError, Error: err})
					return
				}
				if !sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:  EventContentStop,
					Index: int(event.Index),
				}) {
					return
				}
			case "message_delta":
				se := StreamEvent{
					Type: EventMessageDelta,
				}
				// Carry stop_reason and usage from the final message_delta
				if event.Delta.StopReason != "" || event.Usage.OutputTokens > 0 {
					se.Response = acc.mergeDelta(mapAnthropicStopReason(event.Delta.StopReason), Usage{OutputTokens: int(event.Usage.OutputTokens)})
				}
				if !sendStreamEvent(ctx, eventChan, se) {
					return
				}
			case "message_stop":
				if !sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:     EventMessageStop,
					Response: acc.finish(),
				}) {
					return
				}
			}
		}

		if err := stream.Err(); err != nil {
			sendStreamEvent(ctx, eventChan, StreamEvent{
				Type:  EventError,
				Error: err,
			})
		}
	}()

	return eventChan, nil
}

// Capabilities reports which media types Anthropic supports in our Messages API.
func (a *AnthropicClient) Capabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: false, Video: false}
}

// convertAnthropicImage translates an image ContentBlock into the SDK's image block param,
// selecting base64-inline or URL source based on the block's MediaSource kind.
func convertAnthropicImage(block ContentBlock) anthropic.ContentBlockParamUnion {
	if block.Source == nil {
		return anthropic.NewTextBlock("")
	}
	switch block.Source.Kind {
	case SourceKindURL:
		return anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: block.Source.URL})
	default: // Bytes or File (File's Bytes populated eagerly)
		encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
		return anthropic.NewImageBlockBase64(block.MediaType, encoded)
	}
}

// convertAnthropicPDF translates a PDF ContentBlock into the SDK's document block param,
// selecting base64-inline or URL source based on the block's MediaSource kind.
func convertAnthropicPDF(block ContentBlock) anthropic.ContentBlockParamUnion {
	if block.Source == nil {
		return anthropic.NewTextBlock("")
	}
	switch block.Source.Kind {
	case SourceKindURL:
		return anthropic.NewDocumentBlock(anthropic.URLPDFSourceParam{URL: block.Source.URL})
	default:
		encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
		return anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: encoded})
	}
}

// Compile-time interface assertion.
var _ Client = (*AnthropicClient)(nil)

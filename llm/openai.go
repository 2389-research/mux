// ABOUTME: OpenAI API client implementing the llm.Client interface.
// ABOUTME: Handles Responses API message creation and streaming with tool calling.
package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// OpenAIClient implements Client for the OpenAI API.
type OpenAIClient struct {
	client openai.Client
	model  string
}

// NewOpenAIClient creates a new OpenAI API client.
// Default model is gpt-5.2 (Thinking variant for agentic work).
func NewOpenAIClient(apiKey, model string) *OpenAIClient {
	if model == "" {
		model = "gpt-5.2"
	}
	return &OpenAIClient{
		client: openai.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
	}
}

// NewOpenAIClientWithBaseURL creates an OpenAI API client with a custom base URL.
// Useful for Azure OpenAI, proxies, or OpenAI-compatible endpoints.
func NewOpenAIClientWithBaseURL(apiKey, model, baseURL string) *OpenAIClient {
	if model == "" {
		model = "gpt-5.2"
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &OpenAIClient{
		client: openai.NewClient(opts...),
		model:  model,
	}
}

// convertOpenAIRequest converts our Request to OpenAI's ChatCompletionNewParams.
func convertOpenAIRequest(req *Request) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model: req.Model,
	}

	if req.MaxTokens > 0 {
		// Use MaxCompletionTokens for newer models (gpt-5.x, o1, etc.)
		params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
	}

	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}

	// Map thinking config to reasoning effort
	if req.Thinking != nil && req.Thinking.Enabled {
		switch {
		case req.Thinking.Budget <= 4096:
			params.ReasoningEffort = openai.ReasoningEffortLow
		case req.Thinking.Budget <= 16384:
			params.ReasoningEffort = openai.ReasoningEffortMedium
		default:
			params.ReasoningEffort = openai.ReasoningEffortHigh
		}
	}

	// Build messages
	messages := []openai.ChatCompletionMessageParamUnion{}

	// System prompt becomes a system message
	if req.System != "" {
		messages = append(messages, openai.SystemMessage(req.System))
	}

	// Convert conversation messages
	for _, msg := range req.Messages {
		switch msg.Role {
		case RoleUser:
			messages = append(messages, convertUserMessages(msg)...)
		case RoleAssistant:
			messages = append(messages, convertAssistantMessage(msg))
		}
	}
	params.Messages = messages

	// Convert tools. v3 of the SDK switched tools from a single
	// ChatCompletionToolParam to a tagged union (ChatCompletionToolUnionParam)
	// with OfFunction / OfCustom variants. We only emit function tools.
	if len(req.Tools) > 0 {
		tools := make([]openai.ChatCompletionToolUnionParam, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, openai.ChatCompletionToolUnionParam{
				OfFunction: &openai.ChatCompletionFunctionToolParam{
					Function: openai.FunctionDefinitionParam{
						Name:        tool.Name,
						Description: openai.String(tool.Description),
						Parameters:  openai.FunctionParameters(tool.InputSchema),
					},
				},
			})
		}
		params.Tools = tools
	}

	return params
}

// convertOpenAIResponsesRequest converts our Request to the Responses API shape.
func convertOpenAIResponsesRequest(req *Request) (responses.ResponseNewParams, error) {
	input, err := convertResponsesInput(req.Messages)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	params := responses.ResponseNewParams{
		Model: req.Model,
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
	}
	// Retain opaque reasoning items (encrypted_content) so stateless
	// clients can replay them back on the next request. OpenAI rejects the
	// include with a hard 400 on non-reasoning models, so it is gated on the
	// model family. Store stays at its default: no hosted conversation
	// storage is required.
	if supportsEncryptedReasoning(req.Model) {
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
	}

	if req.System != "" {
		params.Instructions = openai.String(req.System)
	}

	if req.MaxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(req.MaxTokens))
	}

	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}

	if req.Thinking != nil && req.Thinking.Enabled {
		params.Reasoning.Effort = reasoningEffort(req.Thinking.Budget)
	}

	if len(req.Tools) > 0 {
		params.Tools = make([]responses.ToolUnionParam, 0, len(req.Tools))
		for _, tool := range req.Tools {
			params.Tools = append(params.Tools, responses.ToolUnionParam{
				OfFunction: &responses.FunctionToolParam{
					Name:        tool.Name,
					Description: openai.String(tool.Description),
					Parameters:  tool.InputSchema,
					Strict:      openai.Bool(false),
				},
			})
		}
	}

	return params, nil
}

// supportsEncryptedReasoning reports whether the model family supports the
// reasoning.encrypted_content include: reasoning model families o1, o3, o4,
// gpt-5, and codex, matched by documented case-insensitive prefixes
// ("gpt-5.1", "o3-mini", and "codex-mini" match; "gpt-4o", "gpt-4.1", and
// "gpt-3.5-turbo" do not). The check is default-deny: omitting the include
// degrades gracefully (the response simply carries no encrypted_content),
// while sending it to a non-reasoning model is a hard 400.
func supportsEncryptedReasoning(model string) bool {
	m := strings.ToLower(model)
	for _, prefix := range []string{"o1", "o3", "o4", "gpt-5", "codex"} {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return false
}

func reasoningEffort(budget int) openai.ReasoningEffort {
	switch {
	case budget <= 4096:
		return openai.ReasoningEffortLow
	case budget <= 16384:
		return openai.ReasoningEffortMedium
	default:
		return openai.ReasoningEffortHigh
	}
}

func convertResponsesInput(messages []Message) (responses.ResponseInputParam, error) {
	items := make(responses.ResponseInputParam, 0, len(messages))
	for _, msg := range messages {
		role := responses.EasyInputMessageRoleUser
		if msg.Role == RoleAssistant {
			role = responses.EasyInputMessageRoleAssistant
		}

		// When a message contains image or PDF blocks, collapse its text and
		// media into a single multipart message item (the only shape the
		// Responses API accepts for multimodal). Otherwise preserve the
		// pre-multimodal behavior of emitting each text block as its own item.
		hasMedia := false
		hasReplay := false
		for _, block := range msg.Blocks {
			if block.Type == ContentTypeImage || block.Type == ContentTypePDF {
				hasMedia = true
			}
			if block.Replay != nil {
				hasReplay = true
			}
		}

		if hasMedia {
			items = append(items, buildResponsesMultipartMessage(role, msg, hasReplay))
		} else if msg.Content != "" && !hasReplay {
			items = append(items, responseMessage(role, msg.Content))
		}

		for _, block := range msg.Blocks {
			// Replay envelopes carry the provider's raw item and are
			// authoritative: decode and emit the item verbatim (preserving
			// id, phase, and encrypted bytes via the SDK's raw-JSON
			// override) instead of re-deriving it from normalized fields.
			if block.Replay != nil {
				var item responses.ResponseInputItemUnion
				if err := json.Unmarshal(block.Replay.Data, &item); err != nil {
					return nil, fmt.Errorf("decode replay item: %w", err)
				}
				items = append(items, item.ToParam())
				continue
			}
			switch block.Type {
			case ContentTypeText:
				if !hasMedia && block.Text != "" {
					items = append(items, responseMessage(role, block.Text))
				}
			case ContentTypeToolUse:
				argsJSON, _ := json.Marshal(block.Input)
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(string(argsJSON), block.ID, block.Name))
			case ContentTypeToolResult:
				items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(block.ToolUseID, block.Text))
			}
		}
	}
	return items, nil
}

// buildResponsesMultipartMessage emits one EasyInputMessage with list-form
// content (text + image + file parts) for a message that has at least one
// image or PDF block. validateRequest has already gated unsupported media.
// When any block carries a Replay envelope (hasReplay), the raw replay items
// are authoritative, so the normalized msg.Content text part is skipped to
// avoid duplicating the replayed message text on the wire; media parts and
// independent text blocks still emit.
func buildResponsesMultipartMessage(role responses.EasyInputMessageRole, msg Message, hasReplay bool) responses.ResponseInputItemUnionParam {
	var content responses.ResponseInputMessageContentListParam
	if msg.Content != "" && !hasReplay {
		content = append(content, responses.ResponseInputContentUnionParam{
			OfInputText: &responses.ResponseInputTextParam{Text: msg.Content},
		})
	}
	for _, block := range msg.Blocks {
		switch block.Type {
		case ContentTypeText:
			if block.Text != "" {
				content = append(content, responses.ResponseInputContentUnionParam{
					OfInputText: &responses.ResponseInputTextParam{Text: block.Text},
				})
			}
		case ContentTypeImage:
			content = append(content, convertResponsesImage(block))
		case ContentTypePDF:
			content = append(content, convertResponsesPDF(block))
		}
	}
	return responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role: role,
			Content: responses.EasyInputMessageContentUnionParam{
				OfInputItemContentList: content,
			},
		},
	}
}

// convertResponsesImage builds an input_image part for the Responses API.
// URL-form sources pass the URL through; inline bytes are encoded as a data URL.
func convertResponsesImage(block ContentBlock) responses.ResponseInputContentUnionParam {
	img := responses.ResponseInputImageParam{Detail: responses.ResponseInputImageDetailAuto}
	if block.Source != nil {
		switch block.Source.Kind {
		case SourceKindURL:
			img.ImageURL = openai.String(block.Source.URL)
		case SourceKindBytes, SourceKindFile:
			encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
			img.ImageURL = openai.String("data:" + block.MediaType + ";base64," + encoded)
		}
	}
	return responses.ResponseInputContentUnionParam{OfInputImage: &img}
}

// convertResponsesPDF builds an input_file part for the Responses API.
// URL-form sources pass through as FileURL (the Responses API accepts
// remote PDFs natively, unlike Chat Completions); inline bytes are base64
// encoded into FileData with a filename derived from the original path.
func convertResponsesPDF(block ContentBlock) responses.ResponseInputContentUnionParam {
	f := responses.ResponseInputFileParam{}
	if block.Source != nil {
		switch block.Source.Kind {
		case SourceKindURL:
			f.FileURL = openai.String(block.Source.URL)
		case SourceKindBytes, SourceKindFile:
			encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
			filename := "file.pdf"
			if block.Source.Path != "" {
				filename = filepath.Base(block.Source.Path)
			}
			f.FileData = openai.String(encoded)
			f.Filename = openai.String(filename)
		}
	}
	return responses.ResponseInputContentUnionParam{OfInputFile: &f}
}

func responseMessage(role responses.EasyInputMessageRole, text string) responses.ResponseInputItemUnionParam {
	return responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role: role,
			Content: responses.EasyInputMessageContentUnionParam{
				OfString: openai.String(text),
			},
		},
	}
}

// convertUserMessage converts a mux user message to OpenAI format.
func convertUserMessage(msg Message) openai.ChatCompletionMessageParamUnion {
	// Tool result routes to a tool message (OpenAI's required shape).
	for _, block := range msg.Blocks {
		if block.Type == ContentTypeToolResult {
			return openai.ToolMessage(block.Text, block.ToolUseID)
		}
	}

	var parts []openai.ChatCompletionContentPartUnionParam
	if msg.Content != "" {
		parts = append(parts, openai.TextContentPart(msg.Content))
	}
	for _, block := range msg.Blocks {
		switch block.Type {
		case ContentTypeText:
			parts = append(parts, openai.TextContentPart(block.Text))
		case ContentTypeImage:
			parts = append(parts, convertOpenAIImage(block))
		case ContentTypePDF:
			parts = append(parts, convertOpenAIPDF(block))
		case ContentTypeAudio:
			parts = append(parts, convertOpenAIAudio(block))
		}
	}

	if len(parts) == 0 {
		return openai.UserMessage("")
	}
	// Keep plain text in string form so we don't force array form unnecessarily.
	if len(parts) == 1 && len(msg.Blocks) == 0 {
		return openai.UserMessage(msg.Content)
	}
	return openai.UserMessage(parts)
}

// convertOpenAIPDF translates a PDF block to an OpenAI file content part.
// URL form and nil Source are rejected pre-flight by validateOpenAISources /
// validateRequest, but we guard here defensively.
func convertOpenAIPDF(block ContentBlock) openai.ChatCompletionContentPartUnionParam {
	if block.Source == nil {
		return openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{})
	}
	encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
	filename := "file.pdf"
	if block.Source.Path != "" {
		filename = filepath.Base(block.Source.Path)
	}
	return openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
		FileData: openai.String(encoded),
		Filename: openai.String(filename),
	})
}

// convertOpenAIAudio translates an audio block to an OpenAI input_audio part.
// URL form and nil Source are rejected pre-flight by validateOpenAISources /
// validateRequest, but we guard here defensively.
func convertOpenAIAudio(block ContentBlock) openai.ChatCompletionContentPartUnionParam {
	if block.Source == nil {
		return openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{})
	}
	format, _ := openaiAudioFormat(block.MediaType) // validateOpenAISources already checked.
	encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
	return openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
		Data:   encoded,
		Format: format,
	})
}

// openaiAudioFormat maps a MIME type to OpenAI's input_audio format.
// audio/mpeg → "mp3"; audio/wav and audio/x-wav → "wav". Unknown MIME types
// return ok=false so callers can reject the request with a clear local error
// rather than relying on an upstream 400.
func openaiAudioFormat(mediaType string) (format string, ok bool) {
	switch mediaType {
	case "audio/mpeg", "audio/mp3":
		return "mp3", true
	case "audio/wav", "audio/x-wav":
		return "wav", true
	default:
		return "", false
	}
}

// validateOpenAISources checks every user-message block for source-form
// compatibility. Returns *ErrUnsupportedSource for audio via URL, audio
// MIME types OpenAI's input_audio doesn't accept, and (when
// allowURLPDF is false) PDF via URL. Used by OpenAI-compatible
// providers (OpenAI, OpenRouter); the provider parameter is attributed
// in returned errors.
//
// allowURLPDF should be true only when the request will be sent via the
// Responses API, which exposes ResponseInputFileParam.FileURL — Chat
// Completions has no equivalent and cannot accept URL-form PDFs.
func validateOpenAISources(provider string, allowURLPDF bool, req *Request) error {
	for _, msg := range req.Messages {
		if msg.Role != RoleUser {
			continue
		}
		for _, block := range msg.Blocks {
			if block.Source == nil {
				continue
			}
			if block.Source.Kind == SourceKindURL {
				switch block.Type {
				case ContentTypePDF:
					if !allowURLPDF {
						return &ErrUnsupportedSource{Provider: provider, Media: "pdf", Kind: "url"}
					}
				case ContentTypeAudio:
					return &ErrUnsupportedSource{Provider: provider, Media: "audio", Kind: "url"}
				}
			}
			if block.Type == ContentTypeAudio {
				if _, ok := openaiAudioFormat(block.MediaType); !ok {
					return &ErrUnsupportedSource{Provider: provider, Media: "audio", Kind: block.MediaType}
				}
			}
		}
	}
	return nil
}

// convertOpenAIImage builds an OpenAI image content part from a mux image
// block, encoding inline bytes as a data URL.
func convertOpenAIImage(block ContentBlock) openai.ChatCompletionContentPartUnionParam {
	if block.Source == nil {
		return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{})
	}
	var url string
	switch block.Source.Kind {
	case SourceKindURL:
		url = block.Source.URL
	default:
		encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
		url = "data:" + block.MediaType + ";base64," + encoded
	}
	return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: url})
}

// convertUserMessages converts a mux user message to one or more OpenAI messages.
// When a user message contains multiple tool results (packed by the orchestrator),
// each tool result becomes a separate ToolMessage. Any text content is emitted as
// a UserMessage before the tool messages.
func convertUserMessages(msg Message) []openai.ChatCompletionMessageParamUnion {
	var toolMessages []openai.ChatCompletionMessageParamUnion
	var hasText bool

	for _, block := range msg.Blocks {
		if block.Type == ContentTypeToolResult {
			toolMessages = append(toolMessages, openai.ToolMessage(block.Text, block.ToolUseID))
		}
	}

	if len(toolMessages) == 0 {
		// No tool results — fall back to single user message behavior
		return []openai.ChatCompletionMessageParamUnion{convertUserMessage(msg)}
	}

	// Check if there is also text content alongside tool results
	if msg.Content != "" {
		hasText = true
	} else {
		for _, block := range msg.Blocks {
			if block.Type == ContentTypeText {
				hasText = true
				break
			}
		}
	}

	var result []openai.ChatCompletionMessageParamUnion
	if hasText {
		text := msg.Content
		if text == "" {
			for _, block := range msg.Blocks {
				if block.Type == ContentTypeText {
					text = block.Text
					break
				}
			}
		}
		result = append(result, openai.UserMessage(text))
	}
	result = append(result, toolMessages...)
	return result
}

// convertAssistantMessage converts a mux assistant message to OpenAI format.
func convertAssistantMessage(msg Message) openai.ChatCompletionMessageParamUnion {
	// v3 switched tool calls on assistant messages from
	// ChatCompletionMessageToolCallParam to a tagged union
	// (ChatCompletionMessageToolCallUnionParam) with OfFunction / OfCustom
	// variants. We only emit function tool calls.
	var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
	var textContent string

	if msg.Content != "" {
		textContent = msg.Content
	}

	for _, block := range msg.Blocks {
		switch block.Type {
		case ContentTypeText:
			textContent = block.Text
		case ContentTypeToolUse:
			argsJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: block.ID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      block.Name,
						Arguments: string(argsJSON),
					},
				},
			})
		}
	}

	if len(toolCalls) > 0 {
		msg := openai.ChatCompletionAssistantMessageParam{
			Role:      "assistant",
			ToolCalls: toolCalls,
		}
		if textContent != "" {
			msg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
				OfString: openai.String(textContent),
			}
		}
		return openai.ChatCompletionMessageParamUnion{OfAssistant: &msg}
	}

	return openai.AssistantMessage(textContent)
}

// convertOpenAIResponse converts OpenAI's ChatCompletion to our Response.
func convertOpenAIResponse(resp *openai.ChatCompletion) *Response {
	result := &Response{
		ID:    resp.ID,
		Model: resp.Model,
		Usage: Usage{
			InputTokens:    int(resp.Usage.PromptTokens),
			OutputTokens:   int(resp.Usage.CompletionTokens),
			ThinkingTokens: int(resp.Usage.CompletionTokensDetails.ReasoningTokens),
		},
	}

	if len(resp.Choices) == 0 {
		// An empty choice list carries no finish reason; report a definite
		// StopReasonOther instead of leaving the field empty.
		result.StopReason = StopReasonOther
		return result
	}

	choice := resp.Choices[0]

	result.StopReason = mapChatStopReason(choice.FinishReason)

	// Text content
	if choice.Message.Content != "" {
		result.Content = append(result.Content, ContentBlock{
			Type: ContentTypeText,
			Text: choice.Message.Content,
		})
	}

	// Tool calls
	for _, tc := range choice.Message.ToolCalls {
		var input map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse tool call arguments for %s: %v\n", tc.Function.Name, err)
			input = make(map[string]any)
		}

		result.Content = append(result.Content, ContentBlock{
			Type:  ContentTypeToolUse,
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	return result
}

// convertOpenAIResponsesResponse converts a Responses API result to our
// Response. Every output item yields exactly one mux block, in order, and
// carries a Replay envelope with the item's raw JSON pinned to the effective
// requested model (requestModel), so reasoning items, message phase, and
// item IDs survive to the next request byte-for-byte. Reasoning items are
// replay-only; message output_text joins into one text block (empty text is
// still emitted so the envelope has a block to ride on); function_call keeps
// its normalized parse keyed by call_id, with the item ID staying raw-only.
func convertOpenAIResponsesResponse(resp *responses.Response, requestModel string) *Response {
	result := &Response{
		ID:    resp.ID,
		Model: resp.Model,
		Usage: Usage{
			InputTokens:    int(resp.Usage.InputTokens),
			OutputTokens:   int(resp.Usage.OutputTokens),
			ThinkingTokens: int(resp.Usage.OutputTokensDetails.ReasoningTokens),
		},
	}

	hasTools := false
	hasRefusal := false
	for _, item := range resp.Output {
		replay := &ProviderReplay{
			Provider: "openai",
			Model:    requestModel,
			Data:     json.RawMessage(item.RawJSON()),
		}
		switch item.Type {
		case "message":
			var text strings.Builder
			for _, content := range item.Content {
				if content.Type == "output_text" {
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(content.Text)
				}
				if content.Type == "refusal" {
					hasRefusal = true
					if content.Refusal != "" {
						result.Content = append(result.Content, ContentBlock{
							Type: ContentTypeText,
							Text: content.Refusal,
						})
					}
				}
			}
			result.Content = append(result.Content, ContentBlock{
				Type:   ContentTypeText,
				Text:   text.String(),
				Replay: replay,
			})
		case "reasoning":
			result.Content = append(result.Content, ContentBlock{
				Type:   ContentTypeReplay,
				Replay: replay,
			})
		case "function_call":
			// v3 made item.Arguments a tagged union (OfString / OfResponseToolSearchCallArguments).
			// For function_call items the arguments are a JSON-encoded string in OfString.
			var input map[string]any
			if err := json.Unmarshal([]byte(item.Arguments.OfString), &input); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to parse tool call arguments for %s: %v\n", item.Name, err)
				input = make(map[string]any)
			}
			result.Content = append(result.Content, ContentBlock{
				Type:   ContentTypeToolUse,
				ID:     item.CallID,
				Name:   item.Name,
				Input:  input,
				Replay: replay,
			})
			hasTools = true
		}
	}

	result.StopReason = mapResponsesStopReason(string(resp.Status), resp.IncompleteDetails.Reason, hasTools)
	if resp.Status == responses.ResponseStatusCompleted && hasRefusal {
		result.StopReason = StopReasonRefusal
	}

	return result
}

// openAIResponseError reports an error for any Responses API result whose
// status is not "completed", including max_output_tokens truncation. It is the
// single status policy for both call shapes: CreateMessage checks it after the
// SDK call returns and before conversion, and CreateMessageStream applies it
// to terminal status events — response.failed, response.incomplete, and
// response.completed events whose payload carries a non-completed status
// (defense against wire inconsistency) — so partial output (e.g. a parsable
// function_call) can never surface as a successful turn on either path.
// Conversion of incomplete responses via convertOpenAIResponsesResponse still
// exposes StopReasonMaxTokens for direct unit conversion.
func openAIResponseError(resp *responses.Response) error {
	switch resp.Status {
	case responses.ResponseStatusCompleted:
		return nil
	case responses.ResponseStatusIncomplete:
		return &ErrProviderResponse{Provider: "openai", Reason: "incomplete: " + resp.IncompleteDetails.Reason}
	case responses.ResponseStatusFailed:
		return &ErrProviderResponse{Provider: "openai", Reason: "failed: " + resp.Error.Message}
	default:
		return &ErrProviderResponse{Provider: "openai", Reason: "unexpected status: " + string(resp.Status)}
	}
}

// CreateMessage sends a message and returns the complete response.
func (o *OpenAIClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	if req.Model == "" {
		req.Model = o.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}

	if err := validateRequest("openai", o.Capabilities(), req); err != nil {
		return nil, err
	}
	// CreateMessage uses the Responses API, which accepts URL-form PDFs
	// natively via ResponseInputFileParam.FileURL.
	if err := validateOpenAISources("openai", true, req); err != nil {
		return nil, err
	}
	if err := validateReplay("openai", req.Model, req.Messages); err != nil {
		return nil, err
	}

	params, err := convertOpenAIResponsesRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := o.client.Responses.New(ctx, params)
	if err != nil {
		return nil, err
	}
	if err := openAIResponseError(resp); err != nil {
		return nil, err
	}

	return convertOpenAIResponsesResponse(resp, req.Model), nil
}

// CreateMessageStream sends a message and returns a channel of streaming events.
func (o *OpenAIClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	if req.Model == "" {
		req.Model = o.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}

	if err := validateRequest("openai", o.Capabilities(), req); err != nil {
		return nil, err
	}
	// CreateMessageStream streams over the Responses API. URL-form PDFs are
	// rejected pre-flight on this path; only inline base64 is sent.
	if err := validateOpenAISources("openai", false, req); err != nil {
		return nil, err
	}
	if err := validateReplay("openai", req.Model, req.Messages); err != nil {
		return nil, err
	}

	params, err := convertOpenAIResponsesRequest(req)
	if err != nil {
		return nil, err
	}
	stream := o.client.Responses.NewStreaming(ctx, params)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in OpenAI CreateMessageStream: %v\n", r)
				eventChan <- StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("panic in stream processing: %v", r),
				}
			}
			close(eventChan)
		}()

		type streamingToolCall struct {
			callID    string
			name      string
			arguments string
		}
		toolCalls := make(map[string]streamingToolCall)
		messageStarted := false

		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "response.created":
				messageStarted = true
				eventChan <- StreamEvent{Type: EventMessageStart}
			case "response.output_text.delta":
				eventChan <- StreamEvent{
					Type: EventContentDelta,
					Text: event.Delta,
				}
			case "response.output_item.added":
				if event.Item.Type == "function_call" {
					toolCalls[event.Item.ID] = streamingToolCall{
						callID:    event.Item.CallID,
						name:      event.Item.Name,
						arguments: event.Item.Arguments.OfString,
					}
				}
			case "response.function_call_arguments.delta":
				toolCall := toolCalls[event.ItemID]
				toolCall.arguments += event.Delta
				toolCalls[event.ItemID] = toolCall
			case "response.function_call_arguments.done":
				toolCall := toolCalls[event.ItemID]
				if event.Arguments != "" {
					toolCall.arguments = event.Arguments
				}
				toolCalls[event.ItemID] = toolCall
				block := openAIStreamingToolCallBlock(event.ItemID, toolCall)
				eventChan <- StreamEvent{
					Type:  EventContentStop,
					Block: block,
				}
			case "response.completed":
				if err := openAIResponseError(&event.Response); err != nil {
					eventChan <- StreamEvent{Type: EventError, Error: err}
					return
				}
				if !messageStarted {
					eventChan <- StreamEvent{Type: EventMessageStart}
				}
				resp := convertOpenAIResponsesResponse(&event.Response, req.Model)
				eventChan <- StreamEvent{
					Type:     EventMessageStop,
					Response: resp,
				}
			case "error":
				eventChan <- StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("openai stream error: %s", event.Message),
				}
				return
			case "response.failed", "response.incomplete":
				err := openAIResponseError(&event.Response)
				if err == nil {
					err = fmt.Errorf("openai: stream reported %s with status %q", event.Type, event.Response.Status)
				}
				eventChan <- StreamEvent{
					Type:  EventError,
					Error: err,
				}
				return
			}
		}

		if err := stream.Err(); err != nil {
			eventChan <- StreamEvent{
				Type:  EventError,
				Error: err,
			}
			return
		}
	}()

	return eventChan, nil
}

func openAIStreamingToolCallBlock(itemID string, toolCall struct {
	callID    string
	name      string
	arguments string
}) *ContentBlock {
	var input map[string]any
	if err := json.Unmarshal([]byte(toolCall.arguments), &input); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to parse tool call arguments for %s: %v\n", toolCall.name, err)
		input = make(map[string]any)
	}
	id := toolCall.callID
	if id == "" {
		id = itemID
	}
	return &ContentBlock{
		Type:  ContentTypeToolUse,
		ID:    id,
		Name:  toolCall.name,
		Input: input,
	}
}

// Capabilities reports which media types OpenAI supports through the Responses
// API paths used by both CreateMessage and CreateMessageStream. Image and PDF
// are supported.
// Audio is reported as false because OpenAI's Responses API does not accept
// audio input at the API level — only Chat Completions does. Narrowing here
// keeps the non-streaming path from silently dropping audio. Restoration is
// gated on OpenAI extending the Responses API itself, not on an SDK update.
func (o *OpenAIClient) Capabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: false, Video: false}
}

// Compile-time interface assertion.
var _ Client = (*OpenAIClient)(nil)

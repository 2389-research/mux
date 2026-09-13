// ABOUTME: Gemini API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming content generation with tool calling.
package llm

import (
	"context"
	"fmt"
	"math"
	"os"

	"google.golang.org/genai"
)

// GeminiClient implements Client for the Gemini API.
type GeminiClient struct {
	client *genai.Client
	model  string
}

// NewGeminiClient creates a new Gemini API client.
// Default model is gemini-2.0-flash.
func NewGeminiClient(ctx context.Context, apiKey, model string) (*GeminiClient, error) {
	if model == "" {
		model = "gemini-2.0-flash"
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &GeminiClient{
		client: client,
		model:  model,
	}, nil
}

// NewGeminiClientWithBaseURL creates a Gemini API client with a custom base URL.
// Useful for proxies or compatible endpoints.
func NewGeminiClientWithBaseURL(ctx context.Context, apiKey, model, baseURL string) (*GeminiClient, error) {
	if model == "" {
		model = "gemini-2.0-flash"
	}

	config := &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	}
	if baseURL != "" {
		config.HTTPOptions = genai.HTTPOptions{
			BaseURL: baseURL,
		}
	}

	client, err := genai.NewClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &GeminiClient{
		client: client,
		model:  model,
	}, nil
}

// convertGeminiRequest converts our Request to Gemini's content and config.
func convertGeminiRequest(req *Request) ([]*genai.Content, *genai.GenerateContentConfig) {
	config := &genai.GenerateContentConfig{}

	if req.MaxTokens > 0 && req.MaxTokens <= math.MaxInt32 {
		config.MaxOutputTokens = int32(req.MaxTokens) //nolint:gosec // bounds checked above
	}

	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		config.Temperature = &temp
	}

	if req.Thinking != nil && req.Thinking.Enabled && req.Thinking.Budget > 0 && req.Thinking.Budget <= math.MaxInt32 {
		budget := int32(req.Thinking.Budget) //nolint:gosec // bounds checked above
		config.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget:  &budget,
			IncludeThoughts: true,
		}
	}

	// System instruction
	if req.System != "" {
		config.SystemInstruction = genai.NewContentFromText(req.System, genai.RoleUser)
	}

	// Convert tools
	if len(req.Tools) > 0 {
		tools := make([]*genai.Tool, 0, 1)
		funcDecls := make([]*genai.FunctionDeclaration, 0, len(req.Tools))
		for _, tool := range req.Tools {
			funcDecl := &genai.FunctionDeclaration{
				Name:        tool.Name,
				Description: tool.Description,
			}
			// Use ParametersJsonSchema to pass the raw schema
			if tool.InputSchema != nil {
				funcDecl.ParametersJsonSchema = tool.InputSchema
			}
			funcDecls = append(funcDecls, funcDecl)
		}
		tools = append(tools, &genai.Tool{FunctionDeclarations: funcDecls})
		config.Tools = tools
	}

	// Convert messages
	contents := make([]*genai.Content, 0, len(req.Messages))
	for _, msg := range req.Messages {
		content := convertMessage(msg)
		if content != nil {
			contents = append(contents, content)
		}
	}

	return contents, config
}

// convertMessage converts a mux Message to Gemini Content.
func convertMessage(msg Message) *genai.Content {
	role := genai.RoleUser
	if msg.Role == RoleAssistant {
		role = genai.RoleModel
	}

	var parts []*genai.Part

	// Handle simple text content
	if msg.Content != "" {
		parts = append(parts, &genai.Part{Text: msg.Content})
	}

	// Handle blocks
	for _, block := range msg.Blocks {
		switch block.Type {
		case ContentTypeText:
			parts = append(parts, &genai.Part{Text: block.Text})
		case ContentTypeToolUse:
			// Assistant's tool call - represented as FunctionCall
			parts = append(parts, genai.NewPartFromFunctionCall(block.Name, block.Input))
		case ContentTypeToolResult:
			// User's tool result - represented as FunctionResponse
			response := map[string]any{"output": block.Text}
			if block.IsError {
				response = map[string]any{"error": block.Text}
			}
			parts = append(parts, genai.NewPartFromFunctionResponse(block.Name, response))
		case ContentTypeImage, ContentTypePDF, ContentTypeAudio, ContentTypeVideo:
			if part := convertGeminiMedia(block); part != nil {
				parts = append(parts, part)
			}
		}
	}

	if len(parts) == 0 {
		return nil
	}

	return &genai.Content{
		Role:  role,
		Parts: parts,
	}
}

// geminiUsage converts Gemini's cumulative usage metadata to our Usage.
// Gemini reports totals per response, so callers replace prior usage with
// this value rather than summing across chunks.
func geminiUsage(meta *genai.GenerateContentResponseUsageMetadata) Usage {
	return Usage{
		InputTokens:    int(meta.PromptTokenCount),
		OutputTokens:   int(meta.CandidatesTokenCount),
		ThinkingTokens: int(meta.ThoughtsTokenCount),
	}
}

// convertGeminiResponse converts Gemini's GenerateContentResponse to our Response.
func convertGeminiResponse(resp *genai.GenerateContentResponse, model string) *Response {
	result := &Response{
		Model: model,
	}

	if resp.ResponseID != "" {
		result.ID = resp.ResponseID
	}

	// Usage metadata
	if resp.UsageMetadata != nil {
		result.Usage = geminiUsage(resp.UsageMetadata)
	}

	reason := ""
	if len(resp.Candidates) > 0 {
		reason = string(resp.Candidates[0].FinishReason)
	}
	result.StopReason = mapGeminiStopReason(reason, len(resp.FunctionCalls()) > 0)

	if len(resp.Candidates) == 0 {
		return result
	}

	candidate := resp.Candidates[0]

	// Extract content from candidate
	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if block, ok := geminiPartToBlock(part); ok {
				result.Content = append(result.Content, block)
			}
		}
	}

	return result
}

// geminiPartToBlock maps one Gemini content part to a mux ContentBlock.
// It returns false for parts with no mux representation (e.g. inline data),
// letting both the non-streaming and streaming paths share one mapping.
// Only the first candidate is processed; do not aggregate alternate
// candidates.
func geminiPartToBlock(part *genai.Part) (ContentBlock, bool) {
	if part == nil {
		return ContentBlock{}, false
	}
	if part.Thought {
		return ContentBlock{Type: ContentTypeThinking, Thinking: part.Text}, true
	}
	if part.FunctionCall != nil {
		return ContentBlock{
			Type:  ContentTypeToolUse,
			ID:    part.FunctionCall.ID,
			Name:  part.FunctionCall.Name,
			Input: part.FunctionCall.Args,
		}, true
	}
	if part.Text != "" {
		return ContentBlock{Type: ContentTypeText, Text: part.Text}, true
	}
	return ContentBlock{}, false
}

// geminiStreamAccumulator builds the final streamed Response from every
// GenerateContentStream chunk (mux#ac2b). Gemini chunks are incremental, so
// keeping only the last chunk loses text and tool calls emitted earlier, and
// lets a usage-only trailer erase accumulated content.
//
// Rules:
//   - Content blocks are appended in the order the parts were received;
//     adjacent text blocks are never merged so part boundaries (and any
//     future per-part signatures, mux#62ba) are preserved.
//   - Usage metadata is cumulative and replaces prior usage only when a
//     chunk actually carries UsageMetadata.
//   - The finish reason is recorded only when a chunk explicitly states one;
//     a chunk without a finish reason must never override a prior explicit
//     one (zero-value FinishReason means "not stopped yet").
//   - Function-call parts are appended as received; do not deduplicate by
//     function name, since parallel calls repeat names with distinct IDs.
type geminiStreamAccumulator struct {
	response     Response
	finishReason genai.FinishReason
}

func newGeminiStreamAccumulator(model string) *geminiStreamAccumulator {
	return &geminiStreamAccumulator{
		response: Response{
			Model:      model,
			StopReason: StopReasonOther,
		},
	}
}

// add folds one streamed chunk into the accumulator. Only the first
// candidate is considered.
func (a *geminiStreamAccumulator) add(chunk *genai.GenerateContentResponse) {
	if chunk == nil {
		return
	}
	if chunk.ResponseID != "" {
		a.response.ID = chunk.ResponseID
	}
	if chunk.UsageMetadata != nil {
		a.response.Usage = geminiUsage(chunk.UsageMetadata)
	}
	if len(chunk.Candidates) == 0 {
		return
	}
	candidate := chunk.Candidates[0]
	if candidate == nil {
		return
	}
	if candidate.FinishReason != "" {
		a.finishReason = candidate.FinishReason
	}
	if candidate.Content == nil {
		return
	}
	for _, part := range candidate.Content.Parts {
		if block, ok := geminiPartToBlock(part); ok {
			a.response.Content = append(a.response.Content, block)
		}
	}
}

// snapshot returns the accumulated Response. The stop reason derives from
// the last explicitly stated finish reason plus whether any tool call was
// accumulated; abnormal and token-limit reasons keep priority over the
// tool-use inference, matching convertGeminiResponse.
func (a *geminiStreamAccumulator) snapshot() Response {
	response := a.response
	hasTools := false
	for _, block := range response.Content {
		if block.Type == ContentTypeToolUse {
			hasTools = true
			break
		}
	}
	response.StopReason = mapGeminiStopReason(string(a.finishReason), hasTools)
	return response
}

// CreateMessage sends a message and returns the complete response.
func (g *GeminiClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = g.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}
	if err := validateRequest("gemini", g.Capabilities(), req); err != nil {
		return nil, err
	}
	if err := validateGeminiSources(req); err != nil {
		return nil, err
	}

	contents, config := convertGeminiRequest(req)
	resp, err := g.client.Models.GenerateContent(ctx, model, contents, config)
	if err != nil {
		return nil, err
	}

	return convertGeminiResponse(resp, model), nil
}

// CreateMessageStream sends a message and returns a channel of streaming events.
func (g *GeminiClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = g.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}
	if err := validateRequest("gemini", g.Capabilities(), req); err != nil {
		return nil, err
	}
	if err := validateGeminiSources(req); err != nil {
		return nil, err
	}

	contents, config := convertGeminiRequest(req)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in Gemini CreateMessageStream: %v\n", r)
				eventChan <- StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("panic in stream processing: %v", r),
				}
			}
			close(eventChan)
		}()

		// Send message start
		eventChan <- StreamEvent{
			Type: EventMessageStart,
		}

		var lastResp *genai.GenerateContentResponse
		acc := newGeminiStreamAccumulator(model)

		// Iterate over the streaming response
		for resp, err := range g.client.Models.GenerateContentStream(ctx, model, contents, config) {
			if err != nil {
				eventChan <- StreamEvent{
					Type:  EventError,
					Error: err,
				}
				return
			}

			lastResp = resp
			acc.add(resp)

			// Process each candidate's content
			if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
				for _, part := range resp.Candidates[0].Content.Parts {
					if part.Text != "" {
						eventChan <- StreamEvent{
							Type: EventContentDelta,
							Text: part.Text,
						}
					}
					if part.FunctionCall != nil {
						eventChan <- StreamEvent{
							Type: EventContentStop,
							Block: &ContentBlock{
								Type:  ContentTypeToolUse,
								ID:    part.FunctionCall.ID,
								Name:  part.FunctionCall.Name,
								Input: part.FunctionCall.Args,
							},
						}
					}
				}
			}
		}

		// Send final message stop with the complete accumulated response
		if lastResp != nil {
			snapshot := acc.snapshot()
			eventChan <- StreamEvent{
				Type:     EventMessageStop,
				Response: &snapshot,
			}
		} else {
			eventChan <- StreamEvent{
				Type: EventMessageStop,
			}
		}
	}()

	return eventChan, nil
}

// Capabilities reports which media types Gemini supports as input.
// All four media kinds are translated to inline_data parts with the block's
// MediaType. URL-form sources are rejected pre-flight by validateGeminiSources
// because Gemini's inline_data takes raw bytes and we don't auto-fetch URLs.
func (g *GeminiClient) Capabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: true, Video: true}
}

// convertGeminiMedia translates a media block into a Gemini inline_data part.
// URL-form sources are rejected pre-flight by validateGeminiSources, so we
// only see Bytes/File here. Returns nil for malformed blocks (also rejected
// pre-flight) as a defensive fallback.
func convertGeminiMedia(block ContentBlock) *genai.Part {
	if block.Source == nil {
		return nil
	}
	return genai.NewPartFromBytes(block.Source.Bytes, block.MediaType)
}

// validateGeminiSources rejects URL-form media because Gemini's inline_data
// takes raw bytes and we don't auto-fetch URLs.
func validateGeminiSources(req *Request) error {
	for _, msg := range req.Messages {
		for _, block := range msg.Blocks {
			if block.Source == nil || block.Source.Kind != SourceKindURL {
				continue
			}
			// Map ContentType to an explicit media-name string so the error
			// stays stable if the ContentType constant values are ever
			// renamed. Matches the pattern in checkBlock / validateOpenAISources.
			var media string
			switch block.Type {
			case ContentTypeImage:
				media = "image"
			case ContentTypePDF:
				media = "pdf"
			case ContentTypeAudio:
				media = "audio"
			case ContentTypeVideo:
				media = "video"
			default:
				continue
			}
			return &ErrUnsupportedSource{Provider: "gemini", Media: media, Kind: "url"}
		}
	}
	return nil
}

// Compile-time interface assertion.
var _ Client = (*GeminiClient)(nil)

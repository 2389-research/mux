// ABOUTME: Gemini API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming content generation with tool calling.
package llm

import (
	"context"
	"encoding/json"
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
func convertGeminiRequest(req *Request) ([]*genai.Content, *genai.GenerateContentConfig, error) {
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
		content, err := convertMessage(msg)
		if err != nil {
			return nil, nil, err
		}
		if content != nil {
			contents = append(contents, content)
		}
	}

	return contents, config, nil
}

// convertMessage converts a mux Message to Gemini Content.
//
// A block carrying a Replay envelope is restored from its raw part JSON and
// its normalized fields are ignored: a rebuilt part would lose the thought
// signature, which Gemini rejects.
func convertMessage(msg Message) (*genai.Content, error) {
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
		if block.Replay != nil {
			restored, err := restoreGeminiPart(block.Replay)
			if err != nil {
				return nil, err
			}
			parts = append(parts, restored)
			continue
		}
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
		return nil, nil
	}

	return &genai.Content{
		Role:  role,
		Parts: parts,
	}, nil
}

// restoreGeminiPart rebuilds a genai.Part from a replay envelope's raw JSON,
// so the thought signature reaches the API as the exact bytes it sent.
func restoreGeminiPart(replay *ProviderReplay) (*genai.Part, error) {
	var part genai.Part
	if err := json.Unmarshal(replay.Data, &part); err != nil {
		return nil, fmt.Errorf("gemini: decoding replay part: %w", err)
	}
	return &part, nil
}

// geminiPartReplay wraps a part carrying a thought signature in a replay
// envelope holding the part's own JSON. Gemini requires the signature back
// unmodified on the first function call of every step of the current turn and
// answers HTTP 400 without it, so the whole part is preserved rather than
// rebuilt from normalized fields. Parts with no signature return nil and keep
// the normalized path, which leaves ordinary text editable.
func geminiPartReplay(part *genai.Part, model string) (*ProviderReplay, error) {
	if len(part.ThoughtSignature) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(part)
	if err != nil {
		return nil, fmt.Errorf("gemini: preserving signed part: %w", err)
	}
	return &ProviderReplay{Provider: "gemini", Model: model, Data: data}, nil
}

// convertGeminiResponse converts Gemini's GenerateContentResponse to our Response.
func convertGeminiResponse(resp *genai.GenerateContentResponse, model string) (*Response, error) {
	result := &Response{
		Model: model,
	}

	if resp.ResponseID != "" {
		result.ID = resp.ResponseID
	}

	// Usage metadata
	if resp.UsageMetadata != nil {
		result.Usage = Usage{
			InputTokens:    int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens:   int(resp.UsageMetadata.CandidatesTokenCount),
			ThinkingTokens: int(resp.UsageMetadata.ThoughtsTokenCount),
		}
	}

	reason := ""
	if len(resp.Candidates) > 0 {
		reason = string(resp.Candidates[0].FinishReason)
	}
	result.StopReason = mapGeminiStopReason(reason, len(resp.FunctionCalls()) > 0)

	if len(resp.Candidates) == 0 {
		return result, nil
	}

	candidate := resp.Candidates[0]

	// Extract content from candidate
	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			replay, err := geminiPartReplay(part, model)
			if err != nil {
				return nil, err
			}
			// A part whose only payload is the signature has nothing to
			// display, so it becomes a replay-only block.
			if replay != nil && part.Text == "" && part.FunctionCall == nil {
				result.Content = append(result.Content, ContentBlock{
					Type:   ContentTypeReplay,
					Replay: replay,
				})
				continue
			}
			// The signature belongs to the part, so exactly one block may
			// own it or replay would send the part twice. When a part holds
			// both text and a call, the call is what Gemini validates.
			textReplay := replay
			if part.FunctionCall != nil {
				textReplay = nil
			}
			if part.Thought {
				result.Content = append(result.Content, ContentBlock{
					Type:     ContentTypeThinking,
					Thinking: part.Text,
					Replay:   textReplay,
				})
			} else if part.Text != "" {
				result.Content = append(result.Content, ContentBlock{
					Type:   ContentTypeText,
					Text:   part.Text,
					Replay: textReplay,
				})
			}
			if part.FunctionCall != nil {
				result.Content = append(result.Content, ContentBlock{
					Type:   ContentTypeToolUse,
					ID:     part.FunctionCall.ID,
					Name:   part.FunctionCall.Name,
					Input:  part.FunctionCall.Args,
					Replay: replay,
				})
			}
		}
	}

	return result, nil
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
	if err := validateReplay("gemini", model, req.Messages); err != nil {
		return nil, err
	}

	contents, config, err := convertGeminiRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Models.GenerateContent(ctx, model, contents, config)
	if err != nil {
		return nil, err
	}

	return convertGeminiResponse(resp, model)
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
	if err := validateReplay("gemini", model, req.Messages); err != nil {
		return nil, err
	}

	contents, config, err := convertGeminiRequest(req)
	if err != nil {
		return nil, err
	}

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
						replay, err := geminiPartReplay(part, model)
						if err != nil {
							eventChan <- StreamEvent{Type: EventError, Error: err}
							return
						}
						eventChan <- StreamEvent{
							Type: EventContentStop,
							Block: &ContentBlock{
								Type:   ContentTypeToolUse,
								ID:     part.FunctionCall.ID,
								Name:   part.FunctionCall.Name,
								Input:  part.FunctionCall.Args,
								Replay: replay,
							},
						}
					}
				}
			}
		}

		// Send final message stop with complete response
		if lastResp != nil {
			final, err := convertGeminiResponse(lastResp, model)
			if err != nil {
				eventChan <- StreamEvent{Type: EventError, Error: err}
				return
			}
			eventChan <- StreamEvent{
				Type:     EventMessageStop,
				Response: final,
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

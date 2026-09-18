// ABOUTME: Gemini API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming content generation with tool calling.
package llm

import (
	"bytes"
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

// validateGeminiReplayPayload checks that a replay payload really is a
// genai.Part. A Part carries no "type" discriminator to key off, so the check
// is structural: the payload must be a non-empty JSON object and every field
// in it must be one genai.Part declares. The field set comes from the SDK's
// own struct tags rather than a list kept by hand here, so it cannot drift
// from the SDK. Without this, "{}" and {"totally":"unrelated"} both decoded
// into an empty Part and shipped to the API as "{}".
//
// The check is top-level only. encoding/json does not carry DisallowUnknownFields
// into a field type that declares its own UnmarshalJSON, so a bogus key nested
// inside one — videoMetadata, say — is not caught. Going deeper would mean
// hand-rolling validation the SDK does not expose, so the guard stops here.
func validateGeminiReplayPayload(field string, data json.RawMessage) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil || len(keys) == 0 {
		return fmt.Errorf("%s: unsupported replay item payload: %.32q", field, data)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var part genai.Part
	if err := decoder.Decode(&part); err != nil {
		return fmt.Errorf("%s: unsupported replay item payload: %.32q", field, data)
	}
	return nil
}

// restoreGeminiPart rebuilds a genai.Part from a replay envelope's raw JSON,
// so the thought signature reaches the API as the exact bytes it sent. The
// payload is re-checked here rather than trusted from preflight, because a
// caller can reach convertMessage directly.
func restoreGeminiPart(replay *ProviderReplay) (*genai.Part, error) {
	if err := validateGeminiReplayPayload("gemini replay part", replay.Data); err != nil {
		return nil, err
	}
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

// geminiBlockedPromptError reports an error when Gemini blocked the prompt
// before generation, per resp.PromptFeedback.BlockReason. A blocked prompt
// produces no candidates, so this is checked ahead of the zero-candidate
// fallback that would otherwise treat it as an ordinary empty turn — a
// caller previously received an empty successful Response with no way to
// tell "nothing to say" from "the provider refused this prompt". It backs
// both call shapes: convertGeminiResponse checks the completed response, and
// CreateMessageStream checks every chunk as it arrives, since PromptFeedback
// is carried on whichever chunk reports the block, not necessarily the last
// one seen.
func geminiBlockedPromptError(resp *genai.GenerateContentResponse) error {
	fb := resp.PromptFeedback
	if fb == nil || fb.BlockReason == "" || fb.BlockReason == genai.BlockedReasonUnspecified {
		return nil
	}
	return &ErrProviderResponse{Provider: "gemini", Reason: "blocked: " + string(fb.BlockReason)}
}

// geminiUsage converts Gemini's usage metadata to our Usage. Gemini reports
// running totals for the whole response, so callers replace prior usage with
// this value rather than summing it across chunks.
func geminiUsage(meta *genai.GenerateContentResponseUsageMetadata) Usage {
	return Usage{
		InputTokens:    int(meta.PromptTokenCount),
		OutputTokens:   int(meta.CandidatesTokenCount),
		ThinkingTokens: int(meta.ThoughtsTokenCount),
	}
}

// geminiPartBlocks maps one Gemini part to the content blocks it contributes.
// Whole responses and streamed chunks both come through here, so a part means
// the same thing however it arrived.
func geminiPartBlocks(part *genai.Part, model string) ([]ContentBlock, error) {
	replay, err := geminiPartReplay(part, model)
	if err != nil {
		return nil, err
	}
	// A part whose only payload is the signature has nothing to
	// display, so it becomes a replay-only block.
	if replay != nil && part.Text == "" && part.FunctionCall == nil {
		return []ContentBlock{{
			Type:   ContentTypeReplay,
			Replay: replay,
		}}, nil
	}
	// The signature belongs to the part, so exactly one block may
	// own it or replay would send the part twice. When a part holds
	// both text and a call, the call is what Gemini validates.
	textReplay := replay
	if part.FunctionCall != nil {
		textReplay = nil
	}
	var blocks []ContentBlock
	if part.Thought {
		blocks = append(blocks, ContentBlock{
			Type:     ContentTypeThinking,
			Thinking: part.Text,
			Replay:   textReplay,
		})
	} else if part.Text != "" {
		blocks = append(blocks, ContentBlock{
			Type:   ContentTypeText,
			Text:   part.Text,
			Replay: textReplay,
		})
	}
	if part.FunctionCall != nil {
		blocks = append(blocks, ContentBlock{
			Type:   ContentTypeToolUse,
			ID:     part.FunctionCall.ID,
			Name:   part.FunctionCall.Name,
			Input:  part.FunctionCall.Args,
			Replay: replay,
		})
	}
	return blocks, nil
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
		result.Usage = geminiUsage(resp.UsageMetadata)
	}

	if err := geminiBlockedPromptError(resp); err != nil {
		return nil, err
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
			blocks, err := geminiPartBlocks(part, model)
			if err != nil {
				return nil, err
			}
			result.Content = append(result.Content, blocks...)
		}
	}

	return result, nil
}

// geminiStreamAccumulator builds the final streamed Response out of every
// chunk GenerateContentStream yields (ac2b). Gemini chunks are incremental, so
// keeping only the last one drops the text and tool calls that came before it,
// and lets a usage-only trailer erase the content entirely.
//
// Rules:
//   - Blocks are appended in the order their parts arrived, and adjacent text
//     blocks are never merged, so part boundaries and the replay envelopes
//     hanging off them survive.
//   - Usage replaces prior usage only when a chunk actually carries it, since
//     Gemini reports running totals rather than per-chunk deltas.
//   - A finish reason is recorded only when a chunk states one: the zero value
//     means "not stopped yet" and must never overwrite an explicit reason.
//   - Function calls are appended as received and never deduplicated by name,
//     because parallel calls repeat a name with distinct IDs.
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

// add folds one streamed chunk into the accumulator, considering only the
// first candidate.
func (a *geminiStreamAccumulator) add(chunk *genai.GenerateContentResponse, model string) error {
	if chunk == nil {
		return nil
	}
	if chunk.ResponseID != "" {
		a.response.ID = chunk.ResponseID
	}
	if chunk.UsageMetadata != nil {
		a.response.Usage = geminiUsage(chunk.UsageMetadata)
	}
	if len(chunk.Candidates) == 0 {
		return nil
	}
	candidate := chunk.Candidates[0]
	if candidate == nil {
		return nil
	}
	if candidate.FinishReason != "" {
		a.finishReason = candidate.FinishReason
	}
	if candidate.Content == nil {
		return nil
	}
	for _, part := range candidate.Content.Parts {
		blocks, err := geminiPartBlocks(part, model)
		if err != nil {
			return err
		}
		a.response.Content = append(a.response.Content, blocks...)
	}
	return nil
}

// snapshot returns the accumulated Response. Its stop reason comes from the
// last finish reason a chunk stated plus whether any tool call accumulated;
// abnormal and token-limit reasons outrank the tool-use inference, matching
// convertGeminiResponse.
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
	messages, err := validateReplay("gemini", model, req.Messages)
	if err != nil {
		return nil, err
	}
	req.Messages = messages

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
	messages, err := validateReplay("gemini", model, req.Messages)
	if err != nil {
		return nil, err
	}
	req.Messages = messages

	contents, config, err := convertGeminiRequest(req)
	if err != nil {
		return nil, err
	}

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in Gemini CreateMessageStream: %v\n", r)
				sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("panic in stream processing: %v", r),
				})
			}
			close(eventChan)
		}()

		// Send message start
		if !sendStreamEvent(ctx, eventChan, StreamEvent{
			Type: EventMessageStart,
		}) {
			return
		}

		var lastResp *genai.GenerateContentResponse
		acc := newGeminiStreamAccumulator(model)

		// Iterate over the streaming response. Returning out of this range
		// loop runs the iterator's own deferred cleanup (closing the HTTP
		// response body), so no explicit stream handle needs closing here.
		for resp, err := range g.client.Models.GenerateContentStream(ctx, model, contents, config) {
			if err != nil {
				sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:  EventError,
					Error: err,
				})
				return
			}

			if err := geminiBlockedPromptError(resp); err != nil {
				sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:  EventError,
					Error: err,
				})
				return
			}

			lastResp = resp
			if err := acc.add(resp, model); err != nil {
				sendStreamEvent(ctx, eventChan, StreamEvent{Type: EventError, Error: err})
				return
			}

			// Process each candidate's content
			if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
				for _, part := range resp.Candidates[0].Content.Parts {
					if part.Text != "" {
						if !sendStreamEvent(ctx, eventChan, StreamEvent{
							Type: EventContentDelta,
							Text: part.Text,
						}) {
							return
						}
					}
					if part.FunctionCall != nil {
						replay, err := geminiPartReplay(part, model)
						if err != nil {
							sendStreamEvent(ctx, eventChan, StreamEvent{Type: EventError, Error: err})
							return
						}
						if !sendStreamEvent(ctx, eventChan, StreamEvent{
							Type: EventContentStop,
							Block: &ContentBlock{
								Type:   ContentTypeToolUse,
								ID:     part.FunctionCall.ID,
								Name:   part.FunctionCall.Name,
								Input:  part.FunctionCall.Args,
								Replay: replay,
							},
						}) {
							return
						}
					}
				}
			}
		}

		// Send final message stop with the complete accumulated response
		if lastResp != nil {
			final := acc.snapshot()
			sendStreamEvent(ctx, eventChan, StreamEvent{
				Type:     EventMessageStop,
				Response: &final,
			})
		} else {
			sendStreamEvent(ctx, eventChan, StreamEvent{
				Type: EventMessageStop,
			})
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

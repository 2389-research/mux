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

// geminiStreamBlockID builds the stream-local block identity for a Gemini
// accumulator ordinal (s61b). Gemini restarts its part index at zero on every
// chunk, so the chunk's own index is not a stable identity; the accumulator
// allocates its own monotonic ordinal per observed block instead.
func geminiStreamBlockID(index int) string {
	return fmt.Sprintf("gemini:%d", index)
}

// copyContentBlock returns an independent pointer copy of a block, so an event
// consumer cannot alias accumulator state through a delivered Block.
func copyContentBlock(block ContentBlock) *ContentBlock {
	copied := cloneContentBlock(block)
	return &copied
}

// Fixed, sanitized StreamProtocolError reasons for Gemini stream violations.
// Each is a static string so an error can never embed raw accumulated content.
const (
	// reasonConflictingCall reports a repeated native call ID whose name or
	// arguments differ from the block already recorded for it. A duplicate
	// with identical name and arguments is a re-observation of the same
	// completed call and is accepted; a changed one cannot be silently
	// merged, because that would invent an argument document the provider
	// never sent.
	reasonConflictingCall = "conflicting duplicate function call"
	// reasonGeminiStreamNoFinish reports a stream that ended without a
	// meaningful finish reason. The accumulated content is incomplete in a
	// way the consumer cannot detect, so it must not be presented as a
	// successful turn.
	reasonGeminiStreamNoFinish = "stream ended without a finish reason"
)

// geminiStreamCompleted reports whether a stream's recorded finish reason
// marks a real end. The zero value means no chunk ever stated a reason, and
// FINISH_REASON_UNSPECIFIED is the SDK's explicit "no reason given": neither
// is completion.
func geminiStreamCompleted(reason genai.FinishReason) bool {
	return reason != "" && reason != genai.FinishReasonUnspecified
}

// geminiArgsEqual reports whether two function-call argument objects are
// identical, so a repeated call ID can be recognized as the same completed
// observation rather than a changed or partially merged one. encoding/json
// sorts map keys, so the comparison is deterministic for identical content.
func geminiArgsEqual(a, b map[string]any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(left, right)
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
// chunk GenerateContentStream yields (ac2b), and (s61b) attaches a stable
// stream-local identity to each block it observes.
//
// Gemini restarts its part index at zero on every chunk, so the chunk's own
// index cannot identify a block. The accumulator allocates its own monotonic
// ordinal per observed block instead, and that ordinal is what
// geminiStreamBlockID renders and what FinalBlocks maps back to Content.
//
// Rules:
//   - Blocks are appended in the order their parts arrived. Distinct parts
//     inside one chunk stay distinct blocks, so part boundaries and the replay
//     envelopes hanging off them survive (ac2b); only a text or thinking part
//     opening a later chunk continues the preceding same-channel block.
//   - Usage replaces prior usage only when a chunk actually carries it, since
//     Gemini reports running totals rather than per-chunk deltas.
//   - A finish reason is recorded only when a chunk states one: the zero value
//     means "not stopped yet" and must never overwrite an explicit reason.
//   - Function calls are keyed by their native ID, never by tool name, because
//     parallel calls repeat a name with distinct IDs. A repeated ID with
//     identical name and arguments is the same completed observation and is
//     taken once; a changed one is a protocol error rather than an invented
//     merge.
type geminiStreamAccumulator struct {
	response     Response
	finishReason genai.FinishReason
	// callIDs maps a native function-call ID to the index in response.Content
	// of the block already recorded for it, so a repeated ID can be
	// recognized without scanning.
	callIDs map[string]int
	// openIndex is the ordinal of the block that may still absorb a
	// same-channel continuation, or -1 when the last observed block is
	// complete. Only the first part of a new chunk may continue it: distinct
	// parts inside one chunk stay distinct blocks (ac2b).
	openIndex int
}

func newGeminiStreamAccumulator(model string) *geminiStreamAccumulator {
	return &geminiStreamAccumulator{
		response: Response{
			Model:      model,
			StopReason: StopReasonOther,
		},
		callIDs:   make(map[string]int),
		openIndex: -1,
	}
}

// add folds one streamed chunk into the accumulator, considering only the
// first candidate, and returns the events to deliver for that chunk's content:
// a content_block_start and typed delta for each text/thinking block, and a
// start/stop pair for each complete tool call. No function call ever yields an
// argument delta: the pinned SDK delivers Args as a whole object, so a
// tool_input fragment would be invented.
func (a *geminiStreamAccumulator) add(chunk *genai.GenerateContentResponse, model string) ([]StreamEvent, error) {
	if chunk == nil {
		return nil, nil
	}
	if chunk.ResponseID != "" {
		a.response.ID = chunk.ResponseID
	}
	if chunk.UsageMetadata != nil {
		a.response.Usage = geminiUsage(chunk.UsageMetadata)
	}
	if len(chunk.Candidates) == 0 {
		return nil, nil
	}
	candidate := chunk.Candidates[0]
	if candidate == nil {
		return nil, nil
	}
	if candidate.FinishReason != "" {
		a.finishReason = candidate.FinishReason
	}
	if candidate.Content == nil {
		return nil, nil
	}

	var events []StreamEvent
	// Only the chunk's first mapped block may continue the previous chunk's
	// open block; every later block in the same chunk begins a new one.
	allowMerge := true
	for _, part := range candidate.Content.Parts {
		blocks, err := geminiPartBlocks(part, model)
		if err != nil {
			return nil, err
		}
		for _, block := range blocks {
			evs, err := a.observe(block, allowMerge)
			if err != nil {
				return nil, err
			}
			allowMerge = false
			events = append(events, evs...)
		}
	}
	return events, nil
}

// geminiMergeable reports whether a block may absorb a same-channel
// continuation from a later chunk. Only unsigned plain text and thinking
// qualify: a tool call is a complete observation, and a part carrying a replay
// envelope must stay its own block so the envelope is never replayed twice.
func geminiMergeable(block ContentBlock) bool {
	return block.Replay == nil && (block.Type == ContentTypeText || block.Type == ContentTypeThinking)
}

// closeOpen emits the stop for the mergeable block still open, if any, and
// clears it. A text or thinking block is stopped when it actually closes — on
// a channel change, a function-call part, or end of stream — not at the end of
// the chunk that opened it, so no delta ever follows its stop.
func (a *geminiStreamAccumulator) closeOpen() []StreamEvent {
	if a.openIndex < 0 {
		return nil
	}
	index := a.openIndex
	a.openIndex = -1
	return []StreamEvent{{
		Type:    EventContentStop,
		Index:   index,
		BlockID: geminiStreamBlockID(index),
		Block:   copyContentBlock(a.response.Content[index]),
	}}
}

// observe records one mapped block, allocating its stream-local ordinal when
// it is new, and returns the events describing it. A same-channel text or
// thinking block continuing an open block appends to it; a tool call closes
// any open block and is delivered as a complete start/stop pair, and a
// repeated native ID carrying the same name and arguments is accepted as the
// same completed block.
func (a *geminiStreamAccumulator) observe(block ContentBlock, allowMerge bool) ([]StreamEvent, error) {
	if block.Type == ContentTypeToolUse && block.ID != "" {
		if existing, ok := a.callIDs[block.ID]; ok {
			prev := a.response.Content[existing]
			if prev.Name == block.Name && geminiArgsEqual(prev.Input, block.Input) {
				// The same completed call re-observed: keep the recorded
				// block and emit no call events, so a duplicate cannot add a
				// second call or a partial argument merge. It still closes any
				// open text/thinking block, because a function-call part ends
				// that channel regardless of whether the call is new.
				return a.closeOpen(), nil
			}
			return nil, &StreamProtocolError{
				Provider: "gemini",
				BlockID:  geminiStreamBlockID(existing),
				Reason:   reasonConflictingCall,
			}
		}
	}

	if allowMerge && a.openIndex >= 0 && geminiMergeable(block) {
		open := &a.response.Content[a.openIndex]
		if open.Type == block.Type {
			// Continuation of the same channel across a chunk boundary: append
			// and emit only the delta, since the block already started.
			if block.Type == ContentTypeThinking {
				open.Thinking += block.Thinking
				return []StreamEvent{{Type: EventContentDelta, Index: a.openIndex, BlockID: geminiStreamBlockID(a.openIndex), DeltaKind: StreamDeltaThinking, Text: block.Thinking}}, nil
			}
			open.Text += block.Text
			return []StreamEvent{{Type: EventContentDelta, Index: a.openIndex, BlockID: geminiStreamBlockID(a.openIndex), DeltaKind: StreamDeltaText, Text: block.Text}}, nil
		}
	}

	// A new block closes whatever was open before it starts.
	events := a.closeOpen()
	ordinal := len(a.response.Content)
	blockID := geminiStreamBlockID(ordinal)
	a.response.Content = append(a.response.Content, block)
	if block.Type == ContentTypeToolUse && block.ID != "" {
		a.callIDs[block.ID] = ordinal
	}

	events = append(events, StreamEvent{Type: EventContentStart, Index: ordinal, BlockID: blockID})
	switch block.Type {
	case ContentTypeText:
		a.openIndex = ordinal
		events = append(events,
			StreamEvent{Type: EventContentDelta, Index: ordinal, BlockID: blockID, DeltaKind: StreamDeltaText, Text: block.Text},
		)
	case ContentTypeThinking:
		a.openIndex = ordinal
		events = append(events,
			StreamEvent{Type: EventContentDelta, Index: ordinal, BlockID: blockID, DeltaKind: StreamDeltaThinking, Text: block.Thinking},
		)
	default:
		// A tool call (or a replay-only part) is a complete observation
		// delivered whole on its stop: no delta, so no consumer can mistake
		// it for incremental argument streaming.
		events = append(events, StreamEvent{Type: EventContentStop, Index: ordinal, BlockID: blockID, Block: copyContentBlock(block)})
	}
	return events, nil
}

// completed reports whether the stream reached a meaningful finish reason. An
// empty reason means no chunk ever stated one, and FINISH_REASON_UNSPECIFIED
// is the SDK's explicit "no reason given"; neither is completion.
func (a *geminiStreamAccumulator) completed() bool {
	return geminiStreamCompleted(a.finishReason)
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

// refs maps every accumulated block to the stream-local identity that
// produced it, in Content order.
func (a *geminiStreamAccumulator) refs() []StreamBlockRef {
	refs := make([]StreamBlockRef, 0, len(a.response.Content))
	for index := range a.response.Content {
		refs = append(refs, StreamBlockRef{BlockID: geminiStreamBlockID(index), ContentIndex: index})
	}
	return refs
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

			// The accumulator maps each part through the same mapper the
			// non-streaming path uses and returns the typed events for the
			// blocks it observed, so a part means the same thing whether it
			// arrived whole or in a chunk (d6rh): a thought part becomes a
			// thinking delta rather than leaking reasoning into answer text,
			// and a function call becomes a complete block with no invented
			// argument delta (s61b).
			events, err := acc.add(resp, model)
			if err != nil {
				sendStreamEvent(ctx, eventChan, StreamEvent{Type: EventError, Error: err})
				return
			}
			for _, event := range events {
				if !sendStreamEvent(ctx, eventChan, event) {
					return
				}
			}
		}

		// A stream that never stated a meaningful finish reason ended early:
		// the accumulated content is incomplete, so it must not be presented
		// as a successful turn. An explicitly empty stream is the same case.
		if !acc.completed() {
			sendStreamEvent(ctx, eventChan, StreamEvent{
				Type:  EventError,
				Error: &StreamProtocolError{Provider: "gemini", Reason: reasonGeminiStreamNoFinish},
			})
			return
		}

		// Close the block still open at end of stream, so every block in the
		// final response was stopped before the terminal event.
		for _, event := range acc.closeOpen() {
			if !sendStreamEvent(ctx, eventChan, event) {
				return
			}
		}

		// Send final message stop with the complete accumulated response and
		// the identity of every block in it.
		final := acc.snapshot()
		sendStreamEvent(ctx, eventChan, StreamEvent{
			Type:        EventMessageStop,
			Response:    &final,
			FinalBlocks: acc.refs(),
		})
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

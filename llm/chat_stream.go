// ABOUTME: Shared accumulator for OpenAI-compatible Chat Completions streams.
// ABOUTME: Preserves interleaved text and native tool-argument fragments for Ollama and OpenRouter.
package llm

import (
	"strconv"
	"strings"

	"github.com/openai/openai-go/v3"
)

// The Chat Completions APIs mux adapts (Ollama's OpenAI-compatible endpoint and
// OpenRouter) stream one choice per response, so the accumulator selects choice
// index 0 and rejects a stream that changes that identity instead of silently
// merging another choice into it.
const chatSelectedChoiceIndex = 0

// Fixed, sanitized StreamProtocolError reasons for Chat Completions stream
// violations. Each is a static string so an error can never embed raw
// accumulated content, which may hold sensitive tool arguments.
const (
	// reasonChatStreamNoFinish reports a stream that ended without an
	// observed finish_reason. The accumulated content is incomplete in a way
	// a consumer cannot detect, so it must not be presented as a complete
	// turn even when the SDK accumulator happens to hold text.
	reasonChatStreamNoFinish = "stream ended without a finish reason"
	// reasonChatStreamChoice reports a chunk whose choice count or choice
	// index does not match the single selected choice.
	reasonChatStreamChoice = "unexpected stream choice"
	// reasonChatStreamAfterFinish reports content or another finish reason
	// observed after the stream already finished. Usage-only chunks are
	// allowed; anything that could add content is not.
	reasonChatStreamAfterFinish = "stream content after finish reason"
	// reasonChatStreamConflictingToolID reports a native tool index whose
	// completed call ID was replaced by a different one. The provider's own
	// IDs are what consumers see, so a silent swap would relabel a call
	// mid-flight.
	reasonChatStreamConflictingToolID = "conflicting tool call id"
	// reasonChatStreamDuplicateToolID reports two distinct native tool
	// indexes claiming the same completed call ID. Consumers key follow-up
	// tool results by that ID, so it must be unique within a response.
	reasonChatStreamDuplicateToolID = "duplicate tool call id"
	// reasonChatStreamIncompleteTool reports a tool call that reached the
	// finish reason without ever declaring both an ID and a name. mux cannot
	// execute a call it cannot identify.
	reasonChatStreamIncompleteTool = "incomplete tool call identity"
	// reasonChatStreamToolType reports a streamed tool call whose native type
	// mux cannot preserve as a JSON argument document.
	reasonChatStreamToolType = "unsupported streamed tool call type"
	// reasonChatStreamAggregateMismatch reports that the SDK accumulator's
	// own aggregate disagrees with the fragments this accumulator observed.
	// Presenting either side alone would be a guess, so the stream fails.
	reasonChatStreamAggregateMismatch = "stream aggregate does not match observed fragments"
)

// chatTextBlockID is the stream-local identity of the single text block a Chat
// Completions stream accumulates, stable across every delta that contributes to
// it.
func chatTextBlockID() string { return "chat:0:text" }

// chatToolBlockID is the stream-local identity of a tool call, keyed by the
// provider's native tool-call index. That index is stable before an ID or name
// arrives, so interleaved argument fragments can be attributed to the right
// call; the final call ID remains the provider's own ID.
func chatToolBlockID(index int) string { return "chat:0:tool:" + strconv.Itoa(index) }

// chatStreamToolIndex normalizes a native tool-call index the same way the SDK
// accumulator does: providers such as AWS Bedrock send -1 for a single tool
// call, which both sides treat as index 0 so the fragment and the aggregate
// stay attributable to the same call.
func chatStreamToolIndex(index int64) int {
	if index < 0 {
		return 0
	}
	return int(index)
}

// chatStreamTool holds the per-call state of one native tool index. Fields are
// stored in a map keyed by that index plus an ordered list of observed indexes,
// never iterated from the map, so final call order is deterministic.
type chatStreamTool struct {
	// id is the provider's completed call ID. The SDK assigns rather than
	// appends this field, so a later nonempty value replaces it only when it
	// is identical.
	id string
	// name accumulates native function-name fragments, following the SDK's
	// append semantics for that field.
	name strings.Builder
	// args accumulates native argument fragments verbatim, in arrival order.
	args strings.Builder
	// input is the validated argument object, set when the call stops.
	input map[string]any
}

// chatStreamAccumulator preserves interleaved text and tool-argument fragments
// from an OpenAI-compatible Chat Completions stream. Add wraps the SDK's own
// accumulator and emits each raw observed chunk exactly once; Finish builds the
// final response from those raw fragments after validating them against the
// SDK's aggregate.
type chatStreamAccumulator struct {
	provider string
	acc      openai.ChatCompletionAccumulator

	text        strings.Builder
	textStarted bool

	tools     map[int]*chatStreamTool
	toolOrder []int

	finished     bool
	finishReason string
}

// newChatStreamAccumulator creates an accumulator for the named provider, which
// is stamped on every protocol error it raises.
func newChatStreamAccumulator(provider string) *chatStreamAccumulator {
	return &chatStreamAccumulator{
		provider: provider,
		tools:    make(map[int]*chatStreamTool),
	}
}

// protocolError builds a fixed-reason protocol error for a native tool index.
func (a *chatStreamAccumulator) protocolError(reason string, index int) error {
	return &StreamProtocolError{Provider: a.provider, BlockID: chatToolBlockID(index), Reason: reason}
}

// Add feeds one raw observed chunk to the accumulator and returns the typed
// events describing it. Chunks must arrive in wire order. The chunk is added to
// the SDK accumulator first, so a chunk the SDK cannot accumulate (a changed
// response ID, for example) fails here rather than desynchronizing the two
// views of the stream.
func (a *chatStreamAccumulator) Add(chunk openai.ChatCompletionChunk) ([]StreamEvent, error) {
	if !a.acc.AddChunk(chunk) {
		return nil, &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamAggregateMismatch}
	}
	if len(chunk.Choices) == 0 {
		// A usage-only chunk: it may update usage, but it can neither add
		// content nor end the stream, so a post-finish usage trailer is fine.
		return nil, nil
	}
	if a.finished {
		// Any choice-bearing chunk after the finish reason could add content
		// the final response will not carry, so it is a protocol violation
		// even if its delta looks empty.
		return nil, &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamAfterFinish}
	}
	if len(chunk.Choices) != 1 || int(chunk.Choices[0].Index) != chatSelectedChoiceIndex {
		return nil, &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamChoice}
	}

	choice := chunk.Choices[0]
	delta := choice.Delta

	var events []StreamEvent
	if delta.Content != "" {
		if !a.textStarted {
			a.textStarted = true
			events = append(events, StreamEvent{
				Type:    EventContentStart,
				Index:   0,
				BlockID: chatTextBlockID(),
				Block:   &ContentBlock{Type: ContentTypeText},
			})
		}
		a.text.WriteString(delta.Content)
		events = append(events, StreamEvent{
			Type:      EventContentDelta,
			Index:     0,
			Text:      delta.Content,
			BlockID:   chatTextBlockID(),
			DeltaKind: StreamDeltaText,
		})
	}

	for _, tc := range delta.ToolCalls {
		index := chatStreamToolIndex(tc.Index)
		tool, exists := a.tools[index]
		if !exists {
			tool = &chatStreamTool{}
			a.tools[index] = tool
			a.toolOrder = append(a.toolOrder, index)
			// The start is emitted on first occurrence even though the call's
			// ID and name may not be complete yet: the native index is the
			// only stable key available at this point.
			events = append(events, StreamEvent{
				Type:    EventContentStart,
				Index:   index,
				BlockID: chatToolBlockID(index),
				Block:   &ContentBlock{Type: ContentTypeToolUse},
			})
		}
		if tc.Type != "" && tc.Type != "function" {
			return nil, a.protocolError(reasonChatStreamToolType, index)
		}
		if tc.ID != "" {
			if tool.id != "" && tool.id != tc.ID {
				return nil, a.protocolError(reasonChatStreamConflictingToolID, index)
			}
			tool.id = tc.ID
		}
		tool.name.WriteString(tc.Function.Name)
		if tc.Function.Arguments != "" {
			tool.args.WriteString(tc.Function.Arguments)
			events = append(events, StreamEvent{
				Type:      EventContentDelta,
				Index:     index,
				Text:      tc.Function.Arguments,
				BlockID:   chatToolBlockID(index),
				DeltaKind: StreamDeltaToolInput,
			})
		}
	}

	if choice.FinishReason != "" {
		// Every accumulated call must be validated before any stop is
		// emitted, so a malformed document cannot reach a consumer as a
		// complete call even for one event.
		if err := a.finalizeTools(); err != nil {
			return nil, err
		}
		a.finished = true
		a.finishReason = choice.FinishReason
		events = append(events, a.stopEvents()...)
	}
	return events, nil
}

// stopEvents returns the block-stop events emitted on the observed finish
// reason, in final canonical order: the text block first when the stream
// produced any, then every tool call in the order its native index was first
// observed. Each stop carries the completed block, so a consumer that only
// follows stops sees the same content the final Response holds.
func (a *chatStreamAccumulator) stopEvents() []StreamEvent {
	var events []StreamEvent
	if a.textStarted {
		events = append(events, StreamEvent{
			Type:    EventContentStop,
			Index:   0,
			BlockID: chatTextBlockID(),
			Block:   &ContentBlock{Type: ContentTypeText, Text: a.text.String()},
		})
	}
	for _, index := range a.toolOrder {
		events = append(events, StreamEvent{
			Type:    EventContentStop,
			Index:   index,
			BlockID: chatToolBlockID(index),
			Block:   a.toolBlock(index),
		})
	}
	return events
}

// toolBlock renders one validated tool call as a ContentBlock, in the same
// shape convertOpenAIResponse produces. It is called only after
// finalizeTools, so the call has a complete identity and parsed input.
func (a *chatStreamAccumulator) toolBlock(index int) *ContentBlock {
	tool := a.tools[index]
	block := ContentBlock{
		Type:  ContentTypeToolUse,
		ID:    tool.id,
		Name:  tool.name.String(),
		Input: tool.input,
	}
	return &block
}

// finalizeTools validates every accumulated call and records its argument
// object, so a malformed, truncated, null or absent argument document fails the
// stream instead of being executed as an empty call.
func (a *chatStreamAccumulator) finalizeTools() error {
	seen := make(map[string]int, len(a.toolOrder))
	for _, index := range a.toolOrder {
		tool := a.tools[index]
		if tool.id == "" || tool.name.Len() == 0 {
			return a.protocolError(reasonChatStreamIncompleteTool, index)
		}
		if previous, ok := seen[tool.id]; ok && previous != index {
			return a.protocolError(reasonChatStreamDuplicateToolID, index)
		}
		seen[tool.id] = index
		input, err := parseStreamToolInput(a.provider, chatToolBlockID(index), tool.args.String())
		if err != nil {
			return err
		}
		tool.input = input
	}
	return nil
}

// Finish builds the final Response and its block identities from the fragments
// observed on the wire, after checking that the SDK's own aggregate agrees. It
// requires an observed finish reason: a stream that ended early is reported as
// a protocol error rather than a successful turn.
func (a *chatStreamAccumulator) Finish() (*Response, []StreamBlockRef, error) {
	if !a.finished {
		return nil, nil, &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamNoFinish}
	}
	if err := a.validateAggregate(); err != nil {
		return nil, nil, err
	}

	response := &Response{
		ID:    a.acc.ID,
		Model: a.acc.Model,
		Usage: Usage{
			InputTokens:    int(a.acc.Usage.PromptTokens),
			OutputTokens:   int(a.acc.Usage.CompletionTokens),
			ThinkingTokens: int(a.acc.Usage.CompletionTokensDetails.ReasoningTokens),
		},
		StopReason: mapChatStopReason(a.finishReason),
	}

	refs := make([]StreamBlockRef, 0, len(a.toolOrder)+1)
	if a.textStarted {
		refs = append(refs, StreamBlockRef{BlockID: chatTextBlockID(), ContentIndex: len(response.Content)})
		response.Content = append(response.Content, ContentBlock{Type: ContentTypeText, Text: a.text.String()})
	}
	for _, index := range a.toolOrder {
		refs = append(refs, StreamBlockRef{BlockID: chatToolBlockID(index), ContentIndex: len(response.Content)})
		response.Content = append(response.Content, *a.toolBlock(index))
	}
	return response, refs, nil
}

// validateAggregate cross-checks the SDK accumulator's aggregate against the
// raw fragments this accumulator observed. The raw fragments are what Finish
// reports, so any disagreement means one of the two views lost or invented
// bytes; neither may be presented as the model's answer.
func (a *chatStreamAccumulator) validateAggregate() error {
	choices := a.acc.Choices
	if len(choices) != 1 || choices[0].Index != chatSelectedChoiceIndex {
		return &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamAggregateMismatch}
	}
	choice := choices[0]
	if choice.FinishReason != "" && choice.FinishReason != a.finishReason {
		return &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamAggregateMismatch}
	}
	if choice.Message.Content != a.text.String() {
		return &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamAggregateMismatch}
	}

	aggregated := choice.Message.ToolCalls
	matched := make(map[int]bool, len(a.toolOrder))
	for _, index := range a.toolOrder {
		tool := a.tools[index]
		if index >= len(aggregated) {
			return &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamAggregateMismatch}
		}
		got := aggregated[index]
		if got.ID != tool.id || got.Function.Name != tool.name.String() || got.Function.Arguments != tool.args.String() {
			return &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamAggregateMismatch}
		}
		matched[index] = true
	}
	for index, got := range aggregated {
		if matched[index] || got.ID == "" {
			continue
		}
		return &StreamProtocolError{Provider: a.provider, Reason: reasonChatStreamAggregateMismatch}
	}
	return nil
}

// validateChatToolInputs validates every function tool call in a non-streaming
// Chat Completions response before it is converted. convertOpenAIResponse
// drops a call whose arguments do not decode, which would let a malformed or
// truncated document vanish into a successful response with the call silently
// missing — a consumer cannot tell that from the model choosing no tool. The
// stream path has the same requirement, and both reject with the same
// sanitized protocol error. An absent argument document is the Chat API's
// normal encoding of an argument string is absent from the response, and the
// SDK decodes that key as "" — indistinguishable from a stream cut off before
// any arguments arrived. mux cannot tell "no arguments" from "the stream never
// said," so, like the Anthropic stream path, it rejects the empty accumulation
// rather than inventing an empty object for it.
func validateChatToolInputs(provider string, resp *openai.ChatCompletion) error {
	if resp == nil || len(resp.Choices) == 0 {
		return nil
	}
	for i, tc := range resp.Choices[0].Message.ToolCalls {
		if tc.Type != "" && tc.Type != "function" {
			return &StreamProtocolError{Provider: provider, BlockID: chatToolBlockID(i), Reason: reasonChatStreamToolType}
		}
		if _, err := parseStreamToolInput(provider, chatToolBlockID(i), tc.Function.Arguments); err != nil {
			return err
		}
	}
	return nil
}

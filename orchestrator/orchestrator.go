// ABOUTME: Implements the core Orchestrator - the agentic think-act loop that
// ABOUTME: coordinates LLM responses, tool execution, and event streaming.
package orchestrator

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

const DefaultMaxIterations = 50

// warnedSchemaTools tracks tools we've warned about missing schemas (to avoid spam)
var warnedSchemaTools sync.Map

// Config holds orchestrator configuration.
type Config struct {
	MaxIterations int
	SystemPrompt  string
	Model         string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{MaxIterations: DefaultMaxIterations}
}

// Orchestrator manages the agentic think-act loop.
type Orchestrator struct {
	client   llm.Client
	executor *tool.Executor
	config   Config
	state    *StateMachine
	eventBus *EventBus
	mu       sync.Mutex
	messages []llm.Message
}

// New creates a new Orchestrator with default config.
func New(client llm.Client, executor *tool.Executor) *Orchestrator {
	return NewWithConfig(client, executor, DefaultConfig())
}

// NewWithConfig creates a new Orchestrator with custom config.
func NewWithConfig(client llm.Client, executor *tool.Executor, config Config) *Orchestrator {
	if client == nil {
		panic("mux: client must not be nil")
	}
	if executor == nil {
		panic("mux: executor must not be nil")
	}
	return &Orchestrator{
		client:   client,
		executor: executor,
		config:   config,
		state:    NewStateMachine(),
		eventBus: NewEventBus(),
		messages: make([]llm.Message, 0),
	}
}

// Subscribe returns a channel for receiving events.
func (o *Orchestrator) Subscribe() <-chan Event {
	return o.eventBus.Subscribe()
}

// State returns the current state.
func (o *Orchestrator) State() State {
	return o.state.Current()
}

// Messages returns a copy of the current conversation history.
func (o *Orchestrator) Messages() []llm.Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make([]llm.Message, len(o.messages))
	copy(result, o.messages)
	return result
}

// SetMessages sets the conversation history.
// Use this to restore conversation state from persistence.
func (o *Orchestrator) SetMessages(messages []llm.Message) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = make([]llm.Message, len(messages))
	copy(o.messages, messages)
}

// ClearMessages resets the conversation history.
func (o *Orchestrator) ClearMessages() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = nil
}

// Run executes the think-act loop with the given prompt.
// Each call starts fresh with only the new prompt (no conversation history).
// Use Continue() for multi-turn conversations that preserve history.
// The orchestrator is not safe for concurrent Run() calls on the same instance.
func (o *Orchestrator) Run(ctx context.Context, prompt string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.state.Reset()
	// Fresh start - replace any existing messages
	o.messages = []llm.Message{llm.NewUserMessage(prompt)}
	// Reset at END instead of Close - allows orchestrator reuse
	defer o.eventBus.Reset()

	return o.runLoop(ctx, prompt)
}

// Continue appends the prompt to existing conversation history and runs the think-act loop.
// Use this for multi-turn conversations where the agent should remember previous exchanges.
// Use SetMessages() to restore history from persistence before calling Continue().
// The orchestrator is not safe for concurrent calls on the same instance.
func (o *Orchestrator) Continue(ctx context.Context, prompt string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.state.Reset()
	// Append to existing conversation history
	o.messages = append(o.messages, llm.NewUserMessage(prompt))
	// Reset at END instead of Close - allows orchestrator reuse
	defer o.eventBus.Reset()

	return o.runLoop(ctx, prompt)
}

// runLoop executes the core think-act loop. Must be called with mutex held.
func (o *Orchestrator) runLoop(ctx context.Context, prompt string) error {
	for i := 0; i < o.config.MaxIterations; i++ {
		// Check context at start of each iteration
		select {
		case <-ctx.Done():
			return o.handleError(ctx.Err())
		default:
		}

		if err := o.transition(StateStreaming); err != nil {
			return o.handleError(err)
		}

		resp, err := o.client.CreateMessage(ctx, o.buildRequest())
		if err != nil {
			return o.handleError(err)
		}

		o.processResponse(resp)

		if resp.HasToolUse() {
			if err := o.executeTools(ctx, resp.ToolUses()); err != nil {
				return o.handleError(err)
			}
			continue
		}

		if err := o.transition(StateComplete); err != nil {
			return o.handleError(err)
		}
		o.eventBus.Publish(NewCompleteEvent(resp.TextContent()))
		return nil
	}

	return o.handleError(fmt.Errorf("exceeded max iterations (%d) while processing: %s", o.config.MaxIterations, prompt))
}

func (o *Orchestrator) buildRequest() *llm.Request {
	tools := o.buildToolDefinitions()
	return &llm.Request{
		Messages:  o.messages,
		System:    o.config.SystemPrompt,
		Model:     o.config.Model,
		MaxTokens: 4096,
		Tools:     tools,
	}
}

// buildToolDefinitions constructs tool definitions from the executor's source.
// NOTE: Tool definitions are rebuilt on every request. Caching could improve performance
// but would require careful invalidation when tools are dynamically added/removed.
// Current approach prioritizes correctness over optimization.
func (o *Orchestrator) buildToolDefinitions() []llm.ToolDefinition {
	source := o.executor.Source()
	allTools := source.All()
	definitions := make([]llm.ToolDefinition, 0, len(allTools))

	for _, t := range allTools {
		def := llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}
		// If the tool provides a schema, use it
		if sp, ok := t.(tool.SchemaProvider); ok {
			def.InputSchema = sp.InputSchema()
		} else {
			// Warn about tools without schemas - LLM won't know what parameters to use
			// Only warn once per tool name to avoid log spam
			if _, warned := warnedSchemaTools.LoadOrStore(t.Name(), true); !warned {
				fmt.Fprintf(os.Stderr, "Warning: tool %q has no InputSchema - LLM may not call it correctly. Implement tool.SchemaProvider to fix.\n", t.Name())
			}
		}
		definitions = append(definitions, def)
	}
	return definitions
}

func (o *Orchestrator) processResponse(resp *llm.Response) {
	for _, block := range resp.Content {
		switch block.Type {
		case llm.ContentTypeText:
			o.eventBus.Publish(NewTextEvent(block.Text))
		case llm.ContentTypeToolUse:
			o.eventBus.Publish(NewToolCallEvent(block.ID, block.Name, block.Input))
		}
	}
	o.messages = append(o.messages, llm.Message{Role: llm.RoleAssistant, Blocks: resp.Content})
}

func (o *Orchestrator) executeTools(ctx context.Context, toolUses []llm.ContentBlock) error {
	if err := o.transition(StateExecutingTool); err != nil {
		return err
	}

	resultBlocks := make([]llm.ContentBlock, 0, len(toolUses))
	for _, use := range toolUses {
		// Check context before each tool execution to handle cancellation during long-running operations.
		// On cancellation, we abandon all results (including any already collected) rather than sending
		// partial results to the LLM. This is intentional: partial tool execution state could confuse
		// the LLM's understanding of what happened.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		result, err := o.executor.Execute(ctx, use.Name, use.Input)
		if err != nil {
			resultBlocks = append(resultBlocks, llm.ContentBlock{
				Type:      llm.ContentTypeToolResult,
				ToolUseID: use.ID,
				Name:      use.Name, // Include tool name for Gemini compatibility
				Text:      fmt.Sprintf("Error: %v", err),
				IsError:   true,
			})
			o.eventBus.Publish(NewToolResultEvent(tool.NewErrorResult(use.Name, err.Error())))
			continue
		}
		// Defensive nil check - tools should never return (nil, nil) but handle gracefully
		if result == nil {
			result = &tool.Result{Success: true, Output: ""}
		}
		resultBlocks = append(resultBlocks, llm.ContentBlock{
			Type:      llm.ContentTypeToolResult,
			ToolUseID: use.ID,
			Name:      use.Name, // Include tool name for Gemini compatibility
			Text:      result.Output,
			IsError:   !result.Success,
		})
		o.eventBus.Publish(NewToolResultEvent(result))
	}

	o.messages = append(o.messages, llm.Message{Role: llm.RoleUser, Blocks: resultBlocks})
	return nil
}

func (o *Orchestrator) transition(to State) error {
	from := o.state.Current()
	if err := o.state.Transition(to); err != nil {
		return err
	}
	o.eventBus.Publish(NewStateChangeEvent(from, to))
	return nil
}

func (o *Orchestrator) handleError(err error) error {
	o.state.Transition(StateError) //nolint:errcheck // best-effort transition to error state
	o.eventBus.Publish(NewErrorEvent(err))
	return err
}

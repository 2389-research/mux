// ABOUTME: Implements the core Orchestrator - the agentic think-act loop that
// ABOUTME: coordinates LLM responses, tool execution, and event streaming.

// Package orchestrator implements the agentic think-act loop that drives LLM
// inference, tool execution, context compaction, and event streaming.
package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"github.com/2389-research/mux/hooks"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

// generateSessionID creates a unique session identifier.
func generateSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "session-unknown"
	}
	return "session-" + hex.EncodeToString(b)
}

const DefaultMaxIterations = 50

// DefaultContextBudget is the default token budget before compaction (0 = disabled by default).
const DefaultContextBudget = 0

// ThinkingStrategy controls when the orchestrator enables thinking on API calls.
type ThinkingStrategy int

const (
	ThinkingOff       ThinkingStrategy = iota // No thinking on any call
	ThinkingAlways                            // Thinking on every call
	ThinkingFirstOnly                         // Thinking on iteration 0 only
	ThinkingAdaptive                          // First call + re-enable on errors/stuck/compaction
)

// DefaultThinkingBudget is the default thinking token budget when thinking is enabled.
const DefaultThinkingBudget = 8192

// DefaultConsecutiveToolThreshold is the default number of consecutive tool iterations
// before adaptive thinking re-enables thinking.
const DefaultConsecutiveToolThreshold = 5

// ThinkingSettings configures per-call thinking behavior in the orchestrator.
type ThinkingSettings struct {
	Strategy                 ThinkingStrategy
	Budget                   int // Token budget when thinking is enabled
	ConsecutiveToolThreshold int // Re-enable after N consecutive tool iterations (Adaptive only)
}

// CompactUserMessageMaxTokens is the maximum tokens of recent user messages to preserve.
const CompactUserMessageMaxTokens = 20000

// warnedSchemaTools tracks tools we've warned about missing schemas (to avoid spam)
var warnedSchemaTools sync.Map

// Config holds orchestrator configuration.
type Config struct {
	MaxIterations    int
	SystemPrompt     string
	Model            string
	HookManager      *hooks.Manager    // Optional hook manager for lifecycle events
	ContextBudget    int               // Max tokens before compaction triggers (0 = disabled)
	CompactionModel  string            // Model to use for summarization (defaults to main Model)
	ThinkingSettings *ThinkingSettings // Per-call thinking control (nil = no thinking)
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{MaxIterations: DefaultMaxIterations}
}

// Orchestrator manages the agentic think-act loop.
type Orchestrator struct {
	client      llm.Client
	executor    *tool.Executor
	config      Config
	state       *StateMachine
	eventBus    *EventBus
	hookManager *hooks.Manager
	sessionID   string
	usage       *TokenUsage
	mu          sync.Mutex
	messages    []llm.Message
	// Thinking state tracking
	iteration                 int
	consecutiveToolIterations int
	justCompacted             bool
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
		client:      client,
		executor:    executor,
		config:      config,
		state:       NewStateMachine(),
		eventBus:    NewEventBus(),
		hookManager: config.HookManager,
		sessionID:   generateSessionID(),
		usage:       NewTokenUsage(),
		messages:    make([]llm.Message, 0),
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

// SessionID returns the unique session identifier for this orchestrator.
func (o *Orchestrator) SessionID() string {
	return o.sessionID
}

// Hooks returns the hook manager, or nil if none was configured.
func (o *Orchestrator) Hooks() *hooks.Manager {
	return o.hookManager
}

// Usage returns a snapshot of the current token usage statistics.
func (o *Orchestrator) Usage() TokenUsage {
	return o.usage.Snapshot()
}

// ResetUsage clears the token usage statistics.
func (o *Orchestrator) ResetUsage() {
	o.usage.Reset()
}

// Messages returns a copy of the current conversation history.
//
// Messages acquires the orchestrator lock, which Run and Continue hold for the
// entire think-act loop. It must not be called from within a hook or tool that
// runs during a Run or Continue: it would deadlock waiting for that loop to
// finish. Observers should snapshot history before or after a run, not during.
func (o *Orchestrator) Messages() []llm.Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make([]llm.Message, len(o.messages))
	copy(result, o.messages)
	return result
}

// SetMessages sets the conversation history.
// Use this to restore conversation state from persistence.
//
// Like Messages, SetMessages acquires the orchestrator lock that Run and
// Continue hold for the duration of the loop, so it must not be called from a
// hook or tool running inside that loop.
func (o *Orchestrator) SetMessages(messages []llm.Message) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = make([]llm.Message, len(messages))
	copy(o.messages, messages)
}

// ClearMessages resets the conversation history.
//
// Like Messages, ClearMessages acquires the orchestrator lock that Run and
// Continue hold for the duration of the loop, so it must not be called from a
// hook or tool running inside that loop.
func (o *Orchestrator) ClearMessages() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = nil
}

// Run executes the think-act loop with the given prompt.
// Each call starts fresh with only the new prompt (no conversation history).
// Use Continue() for multi-turn conversations that preserve history.
// The orchestrator is not safe for concurrent Run() calls on the same instance.
//
// Run holds the orchestrator lock for the entire loop, including LLM calls,
// tool execution, hooks, and compaction. The history accessors (Messages,
// SetMessages, ClearMessages) block until Run returns, so they must not be
// called from a hook or tool running inside the loop.
func (o *Orchestrator) Run(ctx context.Context, prompt string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.state.Reset()
	// Fresh start - replace any existing messages
	o.messages = []llm.Message{llm.NewUserMessage(prompt)}
	// Reset at END instead of Close - allows orchestrator reuse
	defer o.eventBus.Reset()

	return o.runWithHooks(ctx, prompt, "run")
}

// Continue appends the prompt to existing conversation history and runs the think-act loop.
// Use this for multi-turn conversations where the agent should remember previous exchanges.
// Use SetMessages() to restore history from persistence before calling Continue().
// The orchestrator is not safe for concurrent calls on the same instance.
//
// Like Run, Continue holds the orchestrator lock for the entire loop. The
// history accessors block until it returns and must not be called from a hook
// or tool running inside the loop.
func (o *Orchestrator) Continue(ctx context.Context, prompt string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.state.Reset()
	// Append to existing conversation history
	o.messages = append(o.messages, llm.NewUserMessage(prompt))
	// Reset at END instead of Close - allows orchestrator reuse
	defer o.eventBus.Reset()

	return o.runWithHooks(ctx, prompt, "continue")
}

// runWithHooks wraps runLoop with hook firing for session lifecycle.
// Must be called with mutex held.
func (o *Orchestrator) runWithHooks(ctx context.Context, prompt string, source string) error {
	// Fire SessionStart hook
	if o.hookManager != nil {
		event := &hooks.SessionStartEvent{
			SessionID: o.sessionID,
			Source:    source,
			Prompt:    prompt,
		}
		if err := o.hookManager.FireSessionStart(ctx, event); err != nil {
			return o.handleError(err)
		}
	}

	// Ensure SessionEnd fires when we return
	var runErr error
	defer func() {
		if o.hookManager != nil {
			reason := "complete"
			if runErr != nil {
				if ctx.Err() != nil {
					reason = "cancelled"
				} else {
					reason = "error"
				}
			}
			event := &hooks.SessionEndEvent{
				SessionID: o.sessionID,
				Error:     runErr,
				Reason:    reason,
			}
			// Fire SessionEnd - errors don't override runErr (notification-only)
			_ = o.hookManager.FireSessionEnd(ctx, event) //nolint:errcheck // notification-only hook
		}
	}()

	runErr = o.runLoop(ctx, prompt)
	return runErr
}

// runLoop executes the core think-act loop. Must be called with mutex held.
func (o *Orchestrator) runLoop(ctx context.Context, prompt string) error {
	o.iteration = 0
	o.consecutiveToolIterations = 0
	o.justCompacted = false
	for i := 0; i < o.config.MaxIterations; i++ {
		o.iteration = i
		// Check context at start of each iteration
		select {
		case <-ctx.Done():
			return o.handleError(ctx.Err())
		default:
		}

		// Check for compaction before LLM call
		if result, err := o.compact(ctx); err != nil {
			return o.handleError(fmt.Errorf("compaction failed: %w", err))
		} else if result != nil {
			o.justCompacted = true
			// Fire compaction hook
			if o.hookManager != nil {
				event := &hooks.CompactionEvent{
					SessionID:       o.sessionID,
					OriginalTokens:  result.OriginalTokens,
					CompactedTokens: result.CompactedTokens,
					MessagesRemoved: result.MessagesRemoved,
					Summary:         result.Summary,
				}
				if err := o.hookManager.FireCompaction(ctx, event); err != nil {
					return o.handleError(err)
				}
			}
		}

		// Fire Iteration hook at start of each loop iteration
		if o.hookManager != nil {
			event := &hooks.IterationEvent{
				SessionID: o.sessionID,
				Iteration: i,
			}
			if err := o.hookManager.FireIteration(ctx, event); err != nil {
				return o.handleError(err)
			}
		}

		if err := o.transition(StateStreaming); err != nil {
			return o.handleError(err)
		}

		resp, err := o.client.CreateMessage(ctx, o.buildRequest())
		o.justCompacted = false
		if err != nil {
			return o.handleError(err)
		}

		// Track token usage
		o.usage.Add(resp.Usage)

		o.processResponse(resp)

		if resp.HasToolUse() {
			o.consecutiveToolIterations++
			if err := o.executeTools(ctx, resp.ToolUses()); err != nil {
				return o.handleError(err)
			}
			continue
		}
		o.consecutiveToolIterations = 0

		// Fire Stop hook - allows hooks to prevent stopping
		if o.hookManager != nil {
			stopEvent := &hooks.StopEvent{
				SessionID: o.sessionID,
				FinalText: resp.TextContent(),
			}
			continueLoop, err := o.hookManager.FireStop(ctx, stopEvent)
			if err != nil {
				return o.handleError(err)
			}
			if continueLoop {
				// Hook requested continuation - reset state and add user message for next iteration
				o.state.Reset()
				o.messages = append(o.messages, llm.NewUserMessage("continue"))
				continue
			}
		}

		if err := o.transition(StateComplete); err != nil {
			return o.handleError(err)
		}
		o.eventBus.Publish(NewCompleteEvent(resp.TextContent()))
		return nil
	}

	return o.handleError(fmt.Errorf("exceeded max iterations (%d) while processing: %s", o.config.MaxIterations, prompt))
}

// shouldEnableThinking decides whether to enable thinking for the current API call.
func (o *Orchestrator) shouldEnableThinking() bool {
	if o.config.ThinkingSettings == nil {
		return false
	}
	switch o.config.ThinkingSettings.Strategy {
	case ThinkingOff:
		return false
	case ThinkingAlways:
		return true
	case ThinkingFirstOnly:
		return o.iteration == 0
	case ThinkingAdaptive:
		if o.iteration == 0 {
			return true
		}
		if o.justCompacted {
			return true
		}
		if o.consecutiveToolIterations >= o.config.ThinkingSettings.ConsecutiveToolThreshold {
			return true
		}
		if o.lastToolResultHadError() {
			return true
		}
		return false
	}
	return false
}

// lastToolResultHadError checks if any tool result in the last user message had an error.
func (o *Orchestrator) lastToolResultHadError() bool {
	if len(o.messages) == 0 {
		return false
	}
	last := o.messages[len(o.messages)-1]
	if last.Role != llm.RoleUser {
		return false
	}
	for _, block := range last.Blocks {
		if block.Type == llm.ContentTypeToolResult && block.IsError {
			return true
		}
	}
	return false
}

func (o *Orchestrator) buildRequest() *llm.Request {
	tools := o.buildToolDefinitions()
	req := &llm.Request{
		Messages:  o.messages,
		System:    o.config.SystemPrompt,
		Model:     o.config.Model,
		MaxTokens: llm.DefaultMaxTokens,
		Tools:     tools,
	}
	if o.shouldEnableThinking() {
		req.Thinking = &llm.ThinkingConfig{
			Enabled: true,
			Budget:  o.config.ThinkingSettings.Budget,
		}
	}
	return req
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
		case llm.ContentTypeThinking:
			o.eventBus.Publish(NewThinkingEvent(block.Thinking))
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

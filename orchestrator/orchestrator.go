// ABOUTME: Implements the core Orchestrator - the agentic think-act loop that
// ABOUTME: coordinates LLM responses, tool execution, and event streaming.
package orchestrator

import (
	"context"
	"fmt"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

const DefaultMaxIterations = 50

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
	messages []llm.Message
}

// New creates a new Orchestrator with default config.
func New(client llm.Client, executor *tool.Executor) *Orchestrator {
	return NewWithConfig(client, executor, DefaultConfig())
}

// NewWithConfig creates a new Orchestrator with custom config.
func NewWithConfig(client llm.Client, executor *tool.Executor, config Config) *Orchestrator {
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

// Run executes the think-act loop with the given prompt.
func (o *Orchestrator) Run(ctx context.Context, prompt string) error {
	o.state.Reset()
	o.messages = []llm.Message{llm.NewUserMessage(prompt)}
	defer o.eventBus.Close()

	for i := 0; i < o.config.MaxIterations; i++ {
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

	return o.handleError(fmt.Errorf("exceeded max iterations (%d)", o.config.MaxIterations))
}

func (o *Orchestrator) buildRequest() *llm.Request {
	return &llm.Request{
		Messages:  o.messages,
		System:    o.config.SystemPrompt,
		Model:     o.config.Model,
		MaxTokens: 4096,
	}
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

	var resultBlocks []llm.ContentBlock
	for _, use := range toolUses {
		result, err := o.executor.Execute(ctx, use.Name, use.Input)
		if err != nil {
			resultBlocks = append(resultBlocks, llm.ContentBlock{
				Type:      llm.ContentTypeToolResult,
				ToolUseID: use.ID,
				Text:      fmt.Sprintf("Error: %v", err),
				IsError:   true,
			})
			o.eventBus.Publish(NewToolResultEvent(tool.NewErrorResult(use.Name, err.Error())))
			continue
		}
		resultBlocks = append(resultBlocks, llm.ContentBlock{
			Type:      llm.ContentTypeToolResult,
			ToolUseID: use.ID,
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
	o.state.Transition(StateError)
	o.eventBus.Publish(NewErrorEvent(err))
	return err
}

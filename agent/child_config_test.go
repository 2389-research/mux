// ABOUTME: Tests for SpawnChild's per-Config-field inheritance policy and for
// ABOUTME: rejecting a child config that cannot be constructed (suspend without a store).
package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/hooks"
	"github.com/2389-research/mux/internal/llmtest"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/skill"
	"github.com/2389-research/mux/tool"
)

func newTestClient() *llmtest.Client {
	return scriptedResponse(&llm.Response{
		Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
	})
}

// TestSpawnChildRejectsSuspendWithoutSessionStore covers the invalid child config:
// suspend mode cannot work without a store, so SpawnChild must return an error
// instead of panicking inside child.init().
func TestSpawnChildRejectsSuspendWithoutSessionStore(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})

	hookCalls := 0
	hookMgr := hooks.NewManager()
	hookMgr.OnSubagentStart(func(context.Context, *hooks.SubagentStartEvent) error {
		hookCalls++
		return nil
	})

	parent := agent.New(agent.Config{
		Name:        "parent",
		Registry:    registry,
		LLMClient:   newTestClient(),
		HookManager: hookMgr,
	})

	child, err := parent.SpawnChild(agent.Config{
		Name:         "child",
		ApprovalMode: orchestrator.ApprovalSuspend,
	})

	if child != nil {
		t.Errorf("expected nil child, got %v", child)
	}
	if !errors.Is(err, agent.ErrInvalidChildConfig) {
		t.Fatalf("expected ErrInvalidChildConfig, got %v", err)
	}
	if got := len(parent.Children()); got != 0 {
		t.Errorf("invalid child was recorded on the parent: %d children", got)
	}
	if hookCalls != 0 {
		t.Errorf("SubagentStart fired for an invalid child: %d calls", hookCalls)
	}
}

// TestSpawnChildAcceptsSuspendWithSessionStore is the companion positive case: a
// child that restates both the mode and the store is valid.
func TestSpawnChildAcceptsSuspendWithSessionStore(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})

	store := newMemoryStore()

	parent := agent.New(agent.Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: newTestClient(),
	})

	child, err := parent.SpawnChild(agent.Config{
		Name:         "child",
		ApprovalMode: orchestrator.ApprovalSuspend,
		SessionStore: store,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if child == nil {
		t.Fatal("expected a child")
	}
	if got := child.Config(); got.ApprovalMode != orchestrator.ApprovalSuspend || got.SessionStore == nil {
		t.Errorf("child lost suspend settings: mode=%v store=%v", got.ApprovalMode, got.SessionStore)
	}
}

// TestSpawnChildInheritsApprovalFunc records the previously-broken behaviour: a
// child that inherits an approval-gated tool but does not restate ApprovalFunc
// must still be able to run that tool, because the parent's callback travels with
// the inherited tool.
func TestSpawnChildInheritsApprovalFunc(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "deploy", requiresApproval: true})

	parentCalls := 0
	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    newTestClient(),
		AllowedTools: []string{"deploy"},
		ApprovalFunc: func(context.Context, tool.Tool, map[string]any) (bool, error) {
			parentCalls++
			return true, nil
		},
	})

	child, err := parent.SpawnChild(agent.Config{Name: "child"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := child.Executor().Execute(context.Background(), "deploy", nil); err != nil {
		t.Fatalf("inherited approval-gated tool failed: %v", err)
	}
	if parentCalls != 1 {
		t.Errorf("parent approval func calls = %d, want 1", parentCalls)
	}
}

// TestSpawnChildExplicitApprovalFuncOverrides: a child that restates the callback
// uses its own, not the parent's.
func TestSpawnChildExplicitApprovalFuncOverrides(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "deploy", requiresApproval: true})

	parentCalls, childCalls := 0, 0
	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    newTestClient(),
		AllowedTools: []string{"deploy"},
		ApprovalFunc: func(context.Context, tool.Tool, map[string]any) (bool, error) {
			parentCalls++
			return true, nil
		},
	})

	child, err := parent.SpawnChild(agent.Config{
		Name: "child",
		ApprovalFunc: func(context.Context, tool.Tool, map[string]any) (bool, error) {
			childCalls++
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := child.Executor().Execute(context.Background(), "deploy", nil); err != nil {
		t.Fatalf("child tool failed: %v", err)
	}
	if childCalls != 1 || parentCalls != 0 {
		t.Errorf("approval calls parent=%d child=%d, want 0 and 1", parentCalls, childCalls)
	}
}

// TestSpawnChildFieldPolicy records the inheritance policy of every Config field
// on a child that omits it (and, where relevant, on a child that restates it).
func TestSpawnChildFieldPolicy(t *testing.T) {
	t.Run("Registry inherited when nil", func(t *testing.T) {
		registry := tool.NewRegistry()
		parent := agent.New(agent.Config{Name: "parent", Registry: registry, LLMClient: newTestClient()})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.Config().Registry != registry {
			t.Error("child did not inherit parent Registry")
		}
		if child.Executor() == nil {
			t.Error("child has no executor")
		}
	})

	t.Run("LLMClient inherited when nil", func(t *testing.T) {
		client := newTestClient()
		parent := agent.New(agent.Config{Name: "parent", Registry: tool.NewRegistry(), LLMClient: client})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.Config().LLMClient != client {
			t.Error("child did not inherit parent LLMClient")
		}
	})

	t.Run("HookManager inherited when nil", func(t *testing.T) {
		hookMgr := hooks.NewManager()
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(), HookManager: hookMgr,
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.Hooks() != hookMgr {
			t.Error("child did not inherit parent HookManager")
		}
	})

	t.Run("SystemPrompt restated, not inherited", func(t *testing.T) {
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(),
			SystemPrompt: "parent prompt",
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.Config().SystemPrompt != "" {
			t.Errorf("child inherited SystemPrompt %q, want empty", child.Config().SystemPrompt)
		}

		restated, err := parent.SpawnChild(agent.Config{Name: "child2", SystemPrompt: "child prompt"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if restated.Config().SystemPrompt != "child prompt" {
			t.Errorf("child SystemPrompt = %q, want %q", restated.Config().SystemPrompt, "child prompt")
		}
	})

	t.Run("MaxIterations restated, not inherited", func(t *testing.T) {
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(),
			MaxIterations: 42,
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.Config().MaxIterations != 0 {
			t.Errorf("child inherited MaxIterations %d, want 0", child.Config().MaxIterations)
		}
	})

	t.Run("Stream restated, not inherited", func(t *testing.T) {
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(),
			Stream: true,
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.Config().Stream {
			t.Error("child inherited Stream=true, want false")
		}
	})

	t.Run("ThinkingSettings restated, copied when set", func(t *testing.T) {
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(),
			ThinkingSettings: &orchestrator.ThinkingSettings{Strategy: orchestrator.ThinkingAlways, Budget: 4096},
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.Config().ThinkingSettings != nil {
			t.Error("child inherited ThinkingSettings, want nil")
		}

		want := &orchestrator.ThinkingSettings{Strategy: orchestrator.ThinkingAlways, Budget: 8192}
		restated, err := parent.SpawnChild(agent.Config{Name: "child2", ThinkingSettings: want})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := restated.Config().ThinkingSettings
		if got == nil || got.Strategy != want.Strategy || got.Budget != want.Budget {
			t.Fatalf("child ThinkingSettings = %+v, want %+v", got, want)
		}
		if got == want {
			t.Error("child ThinkingSettings aliases the caller's struct; want a copy")
		}
	})

	t.Run("SessionStore and ApprovalMode restated, not inherited", func(t *testing.T) {
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(),
			SessionStore: newMemoryStore(),
			ApprovalMode: orchestrator.ApprovalSuspend,
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg := child.Config()
		if cfg.SessionStore != nil {
			t.Error("child inherited SessionStore, want nil")
		}
		if cfg.ApprovalMode != orchestrator.ApprovalSync {
			t.Errorf("child ApprovalMode = %v, want ApprovalSync", cfg.ApprovalMode)
		}
	})

	t.Run("Skills restated, never propagated", func(t *testing.T) {
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(),
			Skills: skill.NewRegistry(),
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.Config().Skills != nil {
			t.Error("child inherited Skills, want nil")
		}
	})

	t.Run("Name restated by caller", func(t *testing.T) {
		parent := agent.New(agent.Config{Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient()})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.ID() != "parent.child" {
			t.Errorf("child ID = %q, want parent.child", child.ID())
		}
	})

	t.Run("AllowedTools inherits when empty, subset when explicit", func(t *testing.T) {
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(),
			AllowedTools: []string{"read", "write"},
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := child.Config().AllowedTools; len(got) != 2 || got[0] != "read" || got[1] != "write" {
			t.Errorf("child AllowedTools = %v, want inherited [read write]", got)
		}

		subset, err := parent.SpawnChild(agent.Config{Name: "child2", AllowedTools: []string{"read"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := subset.Config().AllowedTools; len(got) != 1 || got[0] != "read" {
			t.Errorf("child AllowedTools = %v, want [read]", got)
		}
	})

	t.Run("DeniedTools union", func(t *testing.T) {
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(),
			DeniedTools: []string{"rm"},
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child", DeniedTools: []string{"bash"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := child.Config().DeniedTools
		if len(got) != 2 {
			t.Fatalf("child DeniedTools = %v, want union of rm and bash", got)
		}
	})

	t.Run("ApprovalFunc inherits when nil, overrides when set", func(t *testing.T) {
		parentFn := func(context.Context, tool.Tool, map[string]any) (bool, error) { return true, nil }
		parent := agent.New(agent.Config{
			Name: "parent", Registry: tool.NewRegistry(), LLMClient: newTestClient(),
			ApprovalFunc: parentFn,
		})

		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if child.Config().ApprovalFunc == nil {
			t.Error("child did not inherit parent ApprovalFunc")
		}
	})
}

// newMemoryStore returns a minimal in-memory orchestrator.Store.
func newMemoryStore() orchestrator.Store {
	return &memoryStore{}
}

type memoryStore struct {
	snapshots map[string]*orchestrator.Snapshot
}

func (m *memoryStore) Save(_ context.Context, snap *orchestrator.Snapshot) error {
	if m.snapshots == nil {
		m.snapshots = make(map[string]*orchestrator.Snapshot)
	}
	m.snapshots[snap.SessionID] = snap
	return nil
}

func (m *memoryStore) Load(_ context.Context, sessionID string) (*orchestrator.Snapshot, error) {
	if snap, ok := m.snapshots[sessionID]; ok {
		return snap, nil
	}
	return nil, orchestrator.ErrSessionNotFound
}

func (m *memoryStore) List(context.Context) ([]string, error) {
	ids := make([]string, 0, len(m.snapshots))
	for id := range m.snapshots {
		ids = append(ids, id)
	}
	return ids, nil
}

func (m *memoryStore) Delete(_ context.Context, sessionID string) error {
	delete(m.snapshots, sessionID)
	return nil
}

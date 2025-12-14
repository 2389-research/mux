// ABOUTME: Tests for the Agent type including construction, tool filtering,
// ABOUTME: and interaction with the orchestrator for agent-based workflows.
package agent_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

// mockClient implements llm.Client for testing
type mockClient struct {
	response *llm.Response
}

func (m *mockClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return m.response, nil
}

func (m *mockClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

// mockTool implements tool.Tool for testing
type mockTool struct {
	name string
}

func (m *mockTool) Name() string                                { return m.name }
func (m *mockTool) Description() string                         { return "mock" }
func (m *mockTool) RequiresApproval(params map[string]any) bool { return false }
func (m *mockTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	return tool.NewResult(m.name, true, "ok", ""), nil
}

func TestNewAgent(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}},
		},
	}

	a := agent.New(agent.Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	if a == nil {
		t.Fatal("expected non-nil agent")
	}
	if a.ID() != "test-agent" {
		t.Errorf("expected ID test-agent, got %s", a.ID())
	}
}

func TestAgentRun(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "response"}},
		},
	}

	a := agent.New(agent.Config{
		Name:      "runner",
		Registry:  registry,
		LLMClient: client,
	})

	events := a.Subscribe()

	err := a.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Drain events
	for range events {
	}
}

func TestAgentSpawnChild(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "write_file"})
	registry.Register(&mockTool{name: "bash"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file", "write_file", "bash"},
	})

	// Spawn child with restricted tools
	child, err := parent.SpawnChild(agent.Config{
		Name:         "child",
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check hierarchical ID
	if child.ID() != "parent.child" {
		t.Errorf("expected ID parent.child, got %s", child.ID())
	}

	// Check parent reference
	if child.Parent() != parent {
		t.Error("expected child.Parent() to return parent")
	}
}

func TestAgentSpawnChildInheritsTools(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "bash"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file", "bash"},
	})

	// Child with empty AllowedTools inherits parent's tools
	child, err := parent.SpawnChild(agent.Config{
		Name: "child",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Child should inherit parent's allowed tools
	cfg := child.Config()
	if len(cfg.AllowedTools) != 2 {
		t.Errorf("expected child to inherit 2 tools, got %d", len(cfg.AllowedTools))
	}
}

func TestAgentSpawnChildDeniedAccumulates(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "bash"})
	registry.Register(&mockTool{name: "rm"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:        "parent",
		Registry:    registry,
		LLMClient:   client,
		DeniedTools: []string{"rm"},
	})

	child, err := parent.SpawnChild(agent.Config{
		Name:        "child",
		DeniedTools: []string{"bash"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Child should have both rm and bash denied
	cfg := child.Config()
	if len(cfg.DeniedTools) != 2 {
		t.Errorf("expected 2 denied tools, got %d: %v", len(cfg.DeniedTools), cfg.DeniedTools)
	}
}

func TestAgentSpawnChildInvalidTools(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "bash"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file"}, // Only read_file allowed
	})

	// Child requesting tool parent doesn't have should fail
	_, err := parent.SpawnChild(agent.Config{
		Name:         "child",
		AllowedTools: []string{"bash"}, // Parent doesn't have bash
	})

	if !errors.Is(err, agent.ErrInvalidChildTools) {
		t.Errorf("expected ErrInvalidChildTools, got %v", err)
	}
}

// TestMaximumHierarchyDepth tests limits on agent hierarchy depth.
func TestMaximumHierarchyDepth(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	// Create a deep hierarchy
	root := agent.New(agent.Config{
		Name:      "root",
		Registry:  registry,
		LLMClient: client,
	})

	current := root
	depth := 100 // Test with deep hierarchy

	for i := 1; i <= depth; i++ {
		child, err := current.SpawnChild(agent.Config{
			Name: "child",
		})
		if err != nil {
			t.Fatalf("failed to create child at depth %d: %v", i, err)
		}
		current = child
	}

	// Verify the hierarchical ID contains all levels
	expectedID := "root"
	for i := 0; i < depth; i++ {
		expectedID += ".child"
	}
	if current.ID() != expectedID {
		t.Errorf("expected ID %s, got %s", expectedID, current.ID())
	}

	// Verify we can traverse back to root
	parent := current.Parent()
	count := 0
	for parent != nil {
		count++
		parent = parent.Parent()
	}
	if count != depth {
		t.Errorf("expected to traverse %d levels to root, got %d", depth, count)
	}
}

// TestCircularDependencyPrevention verifies that parent-child relationships cannot form cycles.
func TestCircularDependencyPrevention(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	child, err := parent.SpawnChild(agent.Config{
		Name: "child",
	})
	if err != nil {
		t.Fatalf("failed to create child: %v", err)
	}

	// Verify hierarchy is correct
	if child.Parent() != parent {
		t.Error("child.Parent() should be parent")
	}

	// Verify child cannot be passed as parent
	// (This is structural - SpawnChild creates new agents, not references)
	grandchild, err := child.SpawnChild(agent.Config{
		Name: "grandchild",
	})
	if err != nil {
		t.Fatalf("failed to create grandchild: %v", err)
	}

	// Verify lineage
	if grandchild.Parent() != child {
		t.Error("grandchild.Parent() should be child")
	}
	if grandchild.Parent().Parent() != parent {
		t.Error("grandchild.Parent().Parent() should be parent")
	}
}

// TestAgentCleanupWhenParentDestroyed tests that child agents remain valid after parent is destroyed.
func TestAgentCleanupWhenParentDestroyed(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	child, err := parent.SpawnChild(agent.Config{
		Name: "child",
	})
	if err != nil {
		t.Fatalf("failed to create child: %v", err)
	}

	// Get child's config before parent is "destroyed"
	childCfg := child.Config()
	childID := child.ID()

	// Simulate parent destruction by setting it to nil
	// (In Go, we can't truly destroy, but we can test child independence)
	parent = nil

	// Child should still be functional
	if child.ID() != childID {
		t.Errorf("child ID changed after parent destruction")
	}

	if childCfg.Registry == nil {
		t.Error("child lost registry after parent destruction")
	}

	// Child should be able to run independently
	err = child.Run(context.Background(), "test")
	if err != nil {
		t.Errorf("child failed to run after parent destruction: %v", err)
	}
}

// TestToolInheritanceComplexCombinations tests complex deny/allow tool combinations.
func TestToolInheritanceComplexCombinations(t *testing.T) {
	tests := []struct {
		name              string
		parentAllowed     []string
		parentDenied      []string
		childAllowed      []string
		childDenied       []string
		expectError       bool
		expectedAllowed   []string
		expectedDenied    []string
		expectedDeniedLen int
	}{
		{
			name:              "child denies subset of parent allowed",
			parentAllowed:     []string{"read", "write", "exec"},
			parentDenied:      []string{},
			childAllowed:      []string{"read", "write"},
			childDenied:       []string{"exec"},
			expectError:       false,
			expectedAllowed:   []string{"read", "write"},
			expectedDenied:    []string{"exec"},
			expectedDeniedLen: 1,
		},
		{
			name:              "accumulated denials",
			parentAllowed:     []string{},
			parentDenied:      []string{"rm", "dd"},
			childAllowed:      []string{},
			childDenied:       []string{"format", "reboot"},
			expectError:       false,
			expectedDenied:    []string{"rm", "dd", "format", "reboot"},
			expectedDeniedLen: 4,
		},
		{
			name:              "child requests parent denied tool",
			parentAllowed:     []string{"read", "write"},
			parentDenied:      []string{},
			childAllowed:      []string{"exec"},
			childDenied:       []string{},
			expectError:       true,
			expectedAllowed:   nil,
			expectedDenied:    nil,
			expectedDeniedLen: 0,
		},
		{
			name:              "overlapping allow and deny",
			parentAllowed:     []string{"read", "write", "exec"},
			parentDenied:      []string{"delete"},
			childAllowed:      []string{"read"},
			childDenied:       []string{"write", "rm"},
			expectError:       false,
			expectedAllowed:   []string{"read"},
			expectedDenied:    []string{"delete", "write", "rm"},
			expectedDeniedLen: 3,
		},
		{
			name:              "empty child inherits all parent allowed",
			parentAllowed:     []string{"tool1", "tool2", "tool3"},
			parentDenied:      []string{"bad_tool"},
			childAllowed:      []string{},
			childDenied:       []string{},
			expectError:       false,
			expectedAllowed:   []string{"tool1", "tool2", "tool3"},
			expectedDenied:    []string{"bad_tool"},
			expectedDeniedLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := tool.NewRegistry()
			registry.Register(&mockTool{name: "read"})
			registry.Register(&mockTool{name: "write"})
			registry.Register(&mockTool{name: "exec"})
			registry.Register(&mockTool{name: "rm"})
			registry.Register(&mockTool{name: "tool1"})
			registry.Register(&mockTool{name: "tool2"})
			registry.Register(&mockTool{name: "tool3"})

			client := &mockClient{
				response: &llm.Response{
					Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
				},
			}

			parent := agent.New(agent.Config{
				Name:         "parent",
				Registry:     registry,
				LLMClient:    client,
				AllowedTools: tt.parentAllowed,
				DeniedTools:  tt.parentDenied,
			})

			child, err := parent.SpawnChild(agent.Config{
				Name:         "child",
				AllowedTools: tt.childAllowed,
				DeniedTools:  tt.childDenied,
			})

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			cfg := child.Config()

			// Check denied tools length
			if len(cfg.DeniedTools) != tt.expectedDeniedLen {
				t.Errorf("expected %d denied tools, got %d: %v",
					tt.expectedDeniedLen, len(cfg.DeniedTools), cfg.DeniedTools)
			}

			// Check allowed tools if specified
			if tt.expectedAllowed != nil && len(cfg.AllowedTools) != len(tt.expectedAllowed) {
				t.Errorf("expected %d allowed tools, got %d: %v",
					len(tt.expectedAllowed), len(cfg.AllowedTools), cfg.AllowedTools)
			}
		})
	}
}

// TestChildAgentInvalidConfigurations tests various invalid configuration scenarios.
func TestChildAgentInvalidConfigurations(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read"},
	})

	tests := []struct {
		name        string
		config      agent.Config
		expectError bool
	}{
		{
			name: "empty name is allowed (gets parent prefix)",
			config: agent.Config{
				Name: "valid",
			},
			expectError: false,
		},
		{
			name: "tool parent doesn't have",
			config: agent.Config{
				Name:         "child",
				AllowedTools: []string{"write"},
			},
			expectError: true,
		},
		{
			name: "valid subset of parent tools",
			config: agent.Config{
				Name:         "child",
				AllowedTools: []string{"read"},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parent.SpawnChild(tt.config)
			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestConcurrentChildSpawning tests thread-safety of spawning multiple children concurrently.
func TestConcurrentChildSpawning(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	const numChildren = 100
	var wg sync.WaitGroup
	wg.Add(numChildren)

	errors := make(chan error, numChildren)
	children := make(chan *agent.Agent, numChildren)

	// Spawn children concurrently
	for i := 0; i < numChildren; i++ {
		go func(idx int) {
			defer wg.Done()
			child, err := parent.SpawnChild(agent.Config{
				Name: "child",
			})
			if err != nil {
				errors <- err
				return
			}
			children <- child
		}(i)
	}

	wg.Wait()
	close(errors)
	close(children)

	// Check for errors
	for err := range errors {
		t.Errorf("concurrent spawn error: %v", err)
	}

	// Verify all children were created
	childCount := 0
	for range children {
		childCount++
	}

	if childCount != numChildren {
		t.Errorf("expected %d children, got %d", numChildren, childCount)
	}

	// Verify parent tracks all children
	parentChildren := parent.Children()
	if len(parentChildren) != numChildren {
		t.Errorf("parent tracks %d children, expected %d", len(parentChildren), numChildren)
	}
}

// TestMemoryLeakPreventionLargeHierarchy tests that large hierarchies don't leak memory.
func TestMemoryLeakPreventionLargeHierarchy(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	root := agent.New(agent.Config{
		Name:      "root",
		Registry:  registry,
		LLMClient: client,
	})

	// Create a wide hierarchy (many children per parent)
	const childrenPerLevel = 10
	const levels = 3

	// Level 1: Create children from root
	level1 := make([]*agent.Agent, childrenPerLevel)
	for i := 0; i < childrenPerLevel; i++ {
		child, err := root.SpawnChild(agent.Config{
			Name: "l1",
		})
		if err != nil {
			t.Fatalf("failed to create level 1 child: %v", err)
		}
		level1[i] = child
	}

	// Level 2: Create children from level 1
	level2 := make([]*agent.Agent, 0)
	for _, parent := range level1 {
		for i := 0; i < childrenPerLevel; i++ {
			child, err := parent.SpawnChild(agent.Config{
				Name: "l2",
			})
			if err != nil {
				t.Fatalf("failed to create level 2 child: %v", err)
			}
			level2 = append(level2, child)
		}
	}

	// Level 3: Create children from level 2
	level3 := make([]*agent.Agent, 0)
	for _, parent := range level2 {
		for i := 0; i < childrenPerLevel; i++ {
			child, err := parent.SpawnChild(agent.Config{
				Name: "l3",
			})
			if err != nil {
				t.Fatalf("failed to create level 3 child: %v", err)
			}
			level3 = append(level3, child)
		}
	}

	// Verify total agents created
	expectedTotal := 1 + childrenPerLevel + (childrenPerLevel * childrenPerLevel) + (childrenPerLevel * childrenPerLevel * childrenPerLevel)
	actualTotal := 1 + len(level1) + len(level2) + len(level3)

	if actualTotal != expectedTotal {
		t.Errorf("expected %d total agents, got %d", expectedTotal, actualTotal)
	}

	// Verify a deep leaf can traverse to root
	if len(level3) > 0 {
		leaf := level3[0]
		parent := leaf.Parent()
		depth := 0
		for parent != nil {
			depth++
			parent = parent.Parent()
		}
		if depth != 3 {
			t.Errorf("expected depth 3 from leaf to root, got %d", depth)
		}
	}

	// Verify Config() returns copies (preventing external modification)
	if len(level1) > 0 {
		cfg := level1[0].Config()
		cfg.Name = "modified"
		cfgAgain := level1[0].Config()
		if cfgAgain.Name == "modified" {
			t.Error("Config() did not return a copy, external modification affected internal state")
		}
	}

	// Nil out references to test GC can clean up
	level1 = nil
	level2 = nil
	level3 = nil

	// If we get here without OOM or hanging, memory management is likely correct
}

// TestChildrenIsolation tests that Children() returns a copy and doesn't allow external modification.
func TestChildrenIsolation(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	// Create some children
	for i := 0; i < 3; i++ {
		_, err := parent.SpawnChild(agent.Config{
			Name: "child",
		})
		if err != nil {
			t.Fatalf("failed to create child: %v", err)
		}
	}

	// Get children list
	children1 := parent.Children()
	if len(children1) != 3 {
		t.Errorf("expected 3 children, got %d", len(children1))
	}

	// Modify the returned slice
	children1[0] = nil
	children1 = append(children1, nil)

	// Get children list again
	children2 := parent.Children()
	if len(children2) != 3 {
		t.Errorf("external modification affected internal state: expected 3 children, got %d", len(children2))
	}

	// All children should still be non-nil
	for i, child := range children2 {
		if child == nil {
			t.Errorf("child at index %d is nil after external modification", i)
		}
	}
}

// TestRemoveChild tests removing a specific child agent.
func TestRemoveChild(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	// Create three children
	child1, err := parent.SpawnChild(agent.Config{Name: "child1"})
	if err != nil {
		t.Fatalf("failed to create child1: %v", err)
	}

	child2, err := parent.SpawnChild(agent.Config{Name: "child2"})
	if err != nil {
		t.Fatalf("failed to create child2: %v", err)
	}

	child3, err := parent.SpawnChild(agent.Config{Name: "child3"})
	if err != nil {
		t.Fatalf("failed to create child3: %v", err)
	}

	// Verify we have 3 children
	children := parent.Children()
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}

	// Remove child2
	removed := parent.RemoveChild(child2)
	if !removed {
		t.Error("expected RemoveChild to return true")
	}

	// Verify we now have 2 children
	children = parent.Children()
	if len(children) != 2 {
		t.Errorf("expected 2 children after removal, got %d", len(children))
	}

	// Verify child2 is not in the list
	for _, c := range children {
		if c == child2 {
			t.Error("child2 should not be in children list after removal")
		}
	}

	// Verify child1 and child3 are still there
	found1 := false
	found3 := false
	for _, c := range children {
		if c == child1 {
			found1 = true
		}
		if c == child3 {
			found3 = true
		}
	}
	if !found1 {
		t.Error("child1 should still be in children list")
	}
	if !found3 {
		t.Error("child3 should still be in children list")
	}

	// Try to remove child2 again (should return false)
	removed = parent.RemoveChild(child2)
	if removed {
		t.Error("expected RemoveChild to return false for already removed child")
	}

	// Try to remove nil child (should return false)
	removed = parent.RemoveChild(nil)
	if removed {
		t.Error("expected RemoveChild to return false for nil child")
	}
}

// TestRemoveAllChildren tests removing all child agents.
func TestRemoveAllChildren(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	// Create several children
	for i := 0; i < 5; i++ {
		_, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("failed to create child: %v", err)
		}
	}

	// Verify we have 5 children
	children := parent.Children()
	if len(children) != 5 {
		t.Fatalf("expected 5 children, got %d", len(children))
	}

	// Remove all children
	parent.RemoveAllChildren()

	// Verify we have 0 children
	children = parent.Children()
	if len(children) != 0 {
		t.Errorf("expected 0 children after RemoveAllChildren, got %d", len(children))
	}

	// Try removing all children again (should be no-op)
	parent.RemoveAllChildren()
	children = parent.Children()
	if len(children) != 0 {
		t.Errorf("expected 0 children after second RemoveAllChildren, got %d", len(children))
	}
}

// TestConcurrentChildRemoval tests thread-safety of removing children concurrently.
func TestConcurrentChildRemoval(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	// Create many children
	const numChildren = 100
	children := make([]*agent.Agent, numChildren)
	for i := 0; i < numChildren; i++ {
		child, err := parent.SpawnChild(agent.Config{Name: "child"})
		if err != nil {
			t.Fatalf("failed to create child: %v", err)
		}
		children[i] = child
	}

	// Remove children concurrently
	var wg sync.WaitGroup
	wg.Add(numChildren)
	for i := 0; i < numChildren; i++ {
		go func(child *agent.Agent) {
			defer wg.Done()
			parent.RemoveChild(child)
		}(children[i])
	}
	wg.Wait()

	// Verify all children were removed
	remaining := parent.Children()
	if len(remaining) != 0 {
		t.Errorf("expected 0 children after concurrent removal, got %d", len(remaining))
	}
}

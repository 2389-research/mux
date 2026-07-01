// ABOUTME: Implements the load_skill tool — an ordinary tool.Tool that returns a
// ABOUTME: skill's markdown body so the model can follow it as a tool_result.
package skill

import (
	"context"
	"strings"

	"github.com/2389-research/mux/tool"
)

// loadSkillToolName is the registered name of the load_skill tool.
const loadSkillToolName = "load_skill"

// Tool returns the load_skill tool bound to this registry. Registering it and
// injecting Catalog() into the system prompt is what makes the registry's skills
// available to an agent.
func (r *Registry) Tool() tool.Tool {
	return &loadSkillTool{reg: r}
}

// loadSkillTool is the tool.Tool implementation backing load_skill.
type loadSkillTool struct {
	reg *Registry
}

func (t *loadSkillTool) Name() string { return loadSkillToolName }

func (t *loadSkillTool) Description() string {
	return "Load the full instructions for a skill by name. Call this with a skill " +
		"name from the Available Skills list before acting on that skill."
}

// RequiresApproval is always false: loading a skill is a pure read with no side effects.
func (t *loadSkillTool) RequiresApproval(map[string]any) bool { return false }

// InputSchema advertises the single required string parameter, name.
func (t *loadSkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of the skill to load, from the Available Skills list.",
			},
		},
		"required": []string{"name"},
	}
}

// Execute returns the named skill's body. An unknown, missing, empty, or non-string
// name yields a failed Result (a recoverable error tool_result), never a Go error.
func (t *loadSkillTool) Execute(_ context.Context, params map[string]any) (*tool.Result, error) {
	name, ok := params["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return tool.NewErrorResult(loadSkillToolName, "load_skill requires a non-empty string 'name' parameter"), nil
	}
	s, ok := t.reg.Get(name)
	if !ok {
		return tool.NewErrorResult(loadSkillToolName, "unknown skill: "+name), nil
	}
	return tool.NewResult(loadSkillToolName, true, s.Body, ""), nil
}

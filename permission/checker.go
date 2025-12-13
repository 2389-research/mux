// ABOUTME: Implements the permission checker - evaluates rules and modes
// ABOUTME: to determine if tool execution should be allowed.
package permission

import "context"

// Rule defines a permission rule.
type Rule struct {
	ToolName string
	Allow    bool
}

// AllowTool creates a rule that allows a specific tool.
func AllowTool(name string) Rule {
	return Rule{ToolName: name, Allow: true}
}

// DenyTool creates a rule that denies a specific tool.
func DenyTool(name string) Rule {
	return Rule{ToolName: name, Allow: false}
}

// Checker evaluates permission rules for tool execution.
type Checker struct {
	mode  Mode
	rules []Rule
}

// NewChecker creates a new permission checker with the given mode.
func NewChecker(mode Mode) *Checker {
	return &Checker{mode: mode, rules: make([]Rule, 0)}
}

// AddRule adds a permission rule.
func (c *Checker) AddRule(rule Rule) {
	c.rules = append(c.rules, rule)
}

// SetMode changes the permission mode.
func (c *Checker) SetMode(mode Mode) { c.mode = mode }

// Mode returns the current permission mode.
func (c *Checker) Mode() Mode { return c.mode }

// Check evaluates whether a tool execution should be allowed.
func (c *Checker) Check(ctx context.Context, toolName string, params map[string]any) (bool, error) {
	switch c.mode {
	case ModeAuto:
		return true, nil
	case ModeDeny:
		return false, nil
	}

	// Mode is Ask - check rules
	for _, rule := range c.rules {
		if rule.ToolName == toolName {
			return rule.Allow, nil
		}
	}

	// No matching rule - default to deny
	return false, nil
}

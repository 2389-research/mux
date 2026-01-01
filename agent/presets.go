// ABOUTME: Provides preset agent configurations for common patterns.
// ABOUTME: Includes Explorer, Planner, and Researcher presets with sensible defaults.
package agent

// Preset represents a pre-configured agent template.
type Preset struct {
	// Name is the default name for agents using this preset.
	Name string

	// SystemPrompt is the specialized system prompt for this agent type.
	SystemPrompt string

	// AllowedTools restricts the agent to these tools only.
	// Empty means all tools are allowed.
	AllowedTools []string

	// DeniedTools prevents the agent from using these tools.
	DeniedTools []string

	// MaxIterations overrides the default iteration limit.
	// 0 means use the default.
	MaxIterations int
}

// ExplorerPreset creates agents optimized for codebase exploration.
// Uses read-only tools and a system prompt focused on finding information.
var ExplorerPreset = Preset{
	Name: "explorer",
	SystemPrompt: `You are a codebase explorer. Your job is to find information efficiently.

Guidelines:
- Use search tools (grep, glob) to locate relevant files
- Read files to understand structure and patterns
- Report findings clearly and concisely
- Do not modify any files
- Focus on answering the specific question asked`,
	AllowedTools: []string{
		"read_file", "read", "glob", "grep", "search",
		"list_files", "list_directory", "ls",
	},
	MaxIterations: 20,
}

// PlannerPreset creates agents optimized for planning and design.
// Uses read-only tools and a system prompt focused on architecture.
var PlannerPreset = Preset{
	Name: "planner",
	SystemPrompt: `You are a software architect and planner. Your job is to design implementation approaches.

Guidelines:
- Analyze existing code structure before proposing changes
- Create step-by-step implementation plans
- Consider edge cases and error handling
- Identify dependencies and potential conflicts
- Do not implement - only plan
- Output structured, actionable plans`,
	AllowedTools: []string{
		"read_file", "read", "glob", "grep", "search",
		"list_files", "list_directory", "ls",
	},
	MaxIterations: 30,
}

// ResearcherPreset creates agents optimized for multi-source research.
// Can access web and codebase for comprehensive research.
var ResearcherPreset = Preset{
	Name: "researcher",
	SystemPrompt: `You are a researcher. Your job is to gather and synthesize information from multiple sources.

Guidelines:
- Search the codebase for existing patterns and implementations
- Use web search for external documentation and best practices
- Cross-reference findings from multiple sources
- Cite sources for your findings
- Summarize key findings clearly
- Note any contradictions or uncertainties`,
	AllowedTools: []string{
		"read_file", "read", "glob", "grep", "search",
		"list_files", "list_directory", "ls",
		"web_search", "web_fetch", "fetch_url",
	},
	MaxIterations: 40,
}

// WriterPreset creates agents optimized for writing and editing files.
// Has full write access with a focused system prompt.
var WriterPreset = Preset{
	Name: "writer",
	SystemPrompt: `You are a code writer. Your job is to implement changes based on specifications.

Guidelines:
- Follow the existing code style and patterns
- Write clean, readable, well-documented code
- Handle errors appropriately
- Write tests for new functionality
- Make minimal, focused changes
- Explain significant design decisions`,
	MaxIterations: 50,
}

// ReviewerPreset creates agents optimized for code review.
// Read-only access with a review-focused system prompt.
var ReviewerPreset = Preset{
	Name: "reviewer",
	SystemPrompt: `You are a code reviewer. Your job is to analyze code for issues and improvements.

Guidelines:
- Check for bugs, security issues, and performance problems
- Verify code follows project conventions
- Look for edge cases and error handling gaps
- Suggest specific improvements with examples
- Be constructive and actionable
- Prioritize issues by severity`,
	AllowedTools: []string{
		"read_file", "read", "glob", "grep", "search",
		"list_files", "list_directory", "ls",
	},
	MaxIterations: 30,
}

// Apply returns a new Config based on this preset with optional overrides.
// Base config values take precedence over preset defaults where specified.
func (p Preset) Apply(base Config) Config {
	// Start with base config
	cfg := base

	// Apply preset defaults where base doesn't specify
	if cfg.Name == "" {
		cfg.Name = p.Name
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = p.SystemPrompt
	}
	if len(cfg.AllowedTools) == 0 && len(p.AllowedTools) > 0 {
		cfg.AllowedTools = make([]string, len(p.AllowedTools))
		copy(cfg.AllowedTools, p.AllowedTools)
	}
	if len(cfg.DeniedTools) == 0 && len(p.DeniedTools) > 0 {
		cfg.DeniedTools = make([]string, len(p.DeniedTools))
		copy(cfg.DeniedTools, p.DeniedTools)
	}
	if cfg.MaxIterations == 0 && p.MaxIterations > 0 {
		cfg.MaxIterations = p.MaxIterations
	}

	return cfg
}

// WithName returns a new preset with the specified name.
func (p Preset) WithName(name string) Preset {
	p.Name = name
	return p
}

// WithSystemPrompt returns a new preset with the specified system prompt.
func (p Preset) WithSystemPrompt(prompt string) Preset {
	p.SystemPrompt = prompt
	return p
}

// WithMaxIterations returns a new preset with the specified max iterations.
func (p Preset) WithMaxIterations(max int) Preset {
	p.MaxIterations = max
	return p
}

// WithAllowedTools returns a new preset with the specified allowed tools.
func (p Preset) WithAllowedTools(tools ...string) Preset {
	p.AllowedTools = tools
	return p
}

// WithDeniedTools returns a new preset with the specified denied tools.
func (p Preset) WithDeniedTools(tools ...string) Preset {
	p.DeniedTools = tools
	return p
}

// SpawnExplorer creates an explorer child agent with the given config overrides.
func (a *Agent) SpawnExplorer(cfg Config) (*Agent, error) {
	return a.SpawnChild(ExplorerPreset.Apply(cfg))
}

// SpawnPlanner creates a planner child agent with the given config overrides.
func (a *Agent) SpawnPlanner(cfg Config) (*Agent, error) {
	return a.SpawnChild(PlannerPreset.Apply(cfg))
}

// SpawnResearcher creates a researcher child agent with the given config overrides.
func (a *Agent) SpawnResearcher(cfg Config) (*Agent, error) {
	return a.SpawnChild(ResearcherPreset.Apply(cfg))
}

// SpawnWriter creates a writer child agent with the given config overrides.
func (a *Agent) SpawnWriter(cfg Config) (*Agent, error) {
	return a.SpawnChild(WriterPreset.Apply(cfg))
}

// SpawnReviewer creates a reviewer child agent with the given config overrides.
func (a *Agent) SpawnReviewer(cfg Config) (*Agent, error) {
	return a.SpawnChild(ReviewerPreset.Apply(cfg))
}

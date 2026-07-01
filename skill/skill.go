// ABOUTME: Defines the Skill type and parseSkill, which reads a SKILL.md byte
// ABOUTME: slice (YAML frontmatter + markdown body) into a validated Skill.

// Package skill loads file-authored procedures ("skills") and exposes them to an
// agent via a system-prompt catalog and an on-demand load_skill tool.
package skill

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill is a single file-authored procedure: frontmatter metadata + markdown body.
type Skill struct {
	Name        string // unique identifier, from frontmatter `name`
	Description string // one-line "what + when", from frontmatter `description`
	Body        string // the markdown instructions following the frontmatter
}

// parseSkill reads a SKILL.md document: a YAML frontmatter block delimited by
// `---` fences, followed by a markdown body. Name, description, and body must all
// be non-empty. Extra frontmatter keys are ignored so skills carrying additional
// fields still load.
func parseSkill(data []byte) (Skill, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return Skill{}, errors.New("missing frontmatter: file must begin with '---'")
	}

	// Drop the opening fence line, then find the closing fence.
	rest := text[strings.IndexByte(text, '\n')+1:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Skill{}, errors.New("unterminated frontmatter: no closing '---'")
	}
	frontmatter := rest[:end]

	// Body is everything after the closing fence line.
	body := rest[end+len("\n---"):]
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}

	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
		return Skill{}, fmt.Errorf("invalid frontmatter yaml: %w", err)
	}

	name := strings.TrimSpace(meta.Name)
	desc := strings.TrimSpace(meta.Description)
	body = strings.TrimSpace(body)
	if name == "" {
		return Skill{}, errors.New("frontmatter 'name' is required")
	}
	if desc == "" {
		return Skill{}, errors.New("frontmatter 'description' is required")
	}
	if body == "" {
		return Skill{}, errors.New("skill body is empty")
	}
	return Skill{Name: name, Description: desc, Body: body}, nil
}

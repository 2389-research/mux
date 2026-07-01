// ABOUTME: White-box tests for the SKILL.md frontmatter parser, covering valid
// ABOUTME: parses, ignored extra keys, and every validation error path.
package skill

import "testing"

func TestParseSkill(t *testing.T) {
	data := []byte("---\nname: commit-message\ndescription: Write a commit. Use when asked.\n---\n\n# Commit\n\n1. Do the thing.\n")
	s, err := parseSkill(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "commit-message" {
		t.Errorf("Name = %q, want commit-message", s.Name)
	}
	if s.Description != "Write a commit. Use when asked." {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Body != "# Commit\n\n1. Do the thing." {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestParseSkillIgnoresExtraKeys(t *testing.T) {
	data := []byte("---\nname: x\ndescription: y\nversion: 2\nextra: stuff\n---\nbody\n")
	s, err := parseSkill(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "x" || s.Description != "y" || s.Body != "body" {
		t.Errorf("got %+v", s)
	}
}

func TestParseSkillErrors(t *testing.T) {
	cases := map[string]string{
		"no frontmatter":    "# Just a heading\n",
		"unterminated":      "---\nname: x\ndescription: y\n",
		"empty name":        "---\nname: \"\"\ndescription: y\n---\nbody\n",
		"missing name":      "---\ndescription: y\n---\nbody\n",
		"empty description": "---\nname: x\ndescription: \"\"\n---\nbody\n",
		"empty body":        "---\nname: x\ndescription: y\n---\n\n",
		"bad yaml":          "---\nname: [unclosed\n---\nbody\n",
	}
	for label, in := range cases {
		if _, err := parseSkill([]byte(in)); err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}

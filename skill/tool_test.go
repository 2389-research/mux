// ABOUTME: Tests for the load_skill tool: metadata, schema, and Execute behavior
// ABOUTME: across found, unknown, and malformed-argument cases.
package skill

import (
	"context"
	"testing"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Register(Skill{Name: "greet", Description: "Say hi.", Body: "Say hello to the user."}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLoadSkillToolMetadata(t *testing.T) {
	tl := newTestRegistry(t).Tool()
	if tl.Name() != "load_skill" {
		t.Errorf("Name = %q, want load_skill", tl.Name())
	}
	if tl.Description() == "" {
		t.Error("Description is empty")
	}
	if tl.RequiresApproval(nil) {
		t.Error("load_skill must not require approval")
	}
	sp, ok := tl.(interface{ InputSchema() map[string]any })
	if !ok {
		t.Fatal("load_skill tool does not implement InputSchema")
	}
	schema := sp.InputSchema()
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
	req, ok := schema["required"].([]string)
	if !ok || len(req) != 1 || req[0] != "name" {
		t.Errorf("schema required = %v, want [name]", schema["required"])
	}
}

func TestLoadSkillToolExecuteFound(t *testing.T) {
	tl := newTestRegistry(t).Tool()
	res, err := tl.Execute(context.Background(), map[string]any{"name": "greet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Errorf("Success = false, Error = %q", res.Error)
	}
	if res.Output != "Say hello to the user." {
		t.Errorf("Output = %q", res.Output)
	}
}

func TestLoadSkillToolExecuteUnknown(t *testing.T) {
	tl := newTestRegistry(t).Tool()
	res, err := tl.Execute(context.Background(), map[string]any{"name": "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Error("Success = true for unknown skill, want false")
	}
}

func TestLoadSkillToolExecuteBadArgs(t *testing.T) {
	tl := newTestRegistry(t).Tool()
	cases := []map[string]any{
		{},           // missing name
		{"name": ""}, // empty name
		{"name": 42}, // non-string name
	}
	for i, params := range cases {
		res, err := tl.Execute(context.Background(), params)
		if err != nil {
			t.Fatalf("case %d: unexpected error: %v", i, err)
		}
		if res.Success {
			t.Errorf("case %d: Success = true, want false", i)
		}
	}
}

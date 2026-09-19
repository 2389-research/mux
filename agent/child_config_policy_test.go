// ABOUTME: Guards that every Config field has a recorded child inheritance policy.
package agent_test

import (
	"reflect"
	"testing"

	"github.com/2389-research/mux/agent"
)

// TestConfigFieldPoliciesAreComplete is the forcing function: adding a Config
// field without recording its child inheritance policy must fail here.
func TestConfigFieldPoliciesAreComplete(t *testing.T) {
	typ := reflect.TypeOf(agent.Config{})
	policies := agent.ConfigFieldChildPolicies()

	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := policies[name]; !ok {
			t.Errorf("Config field %q has no recorded child inheritance policy; add it to childConfigPolicies in agent/config.go", name)
		}
	}
	for name := range policies {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("ConfigFieldChildPolicies names %q, which is not a Config field", name)
		}
	}
}

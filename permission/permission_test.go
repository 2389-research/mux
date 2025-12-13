package permission_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/permission"
)

func TestPermissionModes(t *testing.T) {
	// Auto mode - always allows
	auto := permission.NewChecker(permission.ModeAuto)
	allowed, _ := auto.Check(context.Background(), "any_tool", nil)
	if !allowed {
		t.Error("expected Auto to allow")
	}

	// Deny mode - always denies
	deny := permission.NewChecker(permission.ModeDeny)
	allowed, _ = deny.Check(context.Background(), "any_tool", nil)
	if allowed {
		t.Error("expected Deny to deny")
	}
}

func TestPermissionRules(t *testing.T) {
	checker := permission.NewChecker(permission.ModeAsk)

	checker.AddRule(permission.AllowTool("safe_tool"))
	allowed, _ := checker.Check(context.Background(), "safe_tool", nil)
	if !allowed {
		t.Error("expected safe_tool to be allowed")
	}

	allowed, _ = checker.Check(context.Background(), "unknown_tool", nil)
	if allowed {
		t.Error("expected unknown_tool to require asking")
	}

	checker.AddRule(permission.DenyTool("dangerous"))
	allowed, _ = checker.Check(context.Background(), "dangerous", nil)
	if allowed {
		t.Error("expected dangerous to be denied")
	}
}

func TestModeChange(t *testing.T) {
	checker := permission.NewChecker(permission.ModeAsk)
	if checker.Mode() != permission.ModeAsk {
		t.Error("expected ModeAsk")
	}

	checker.SetMode(permission.ModeAuto)
	if checker.Mode() != permission.ModeAuto {
		t.Error("expected ModeAuto after SetMode")
	}
}

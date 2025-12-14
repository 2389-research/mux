// ABOUTME: Tests for the resource coordinator - locking functionality.
// ABOUTME: Verifies thread-safe resource locking across agents.
package coordinator

import (
	"context"
	"testing"
)

func TestCoordinatorAcquireRelease(t *testing.T) {
	c := New()

	// Acquire should succeed
	err := c.Acquire(context.Background(), "agent1", "resource1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Same agent re-acquire should succeed (idempotent)
	err = c.Acquire(context.Background(), "agent1", "resource1")
	if err != nil {
		t.Fatalf("expected idempotent acquire, got %v", err)
	}

	// Different agent should fail
	err = c.Acquire(context.Background(), "agent2", "resource1")
	if err == nil {
		t.Fatal("expected error for different agent")
	}

	// Release should succeed
	err = c.Release("agent1", "resource1")
	if err != nil {
		t.Fatalf("expected no error on release, got %v", err)
	}

	// Now agent2 can acquire
	err = c.Acquire(context.Background(), "agent2", "resource1")
	if err != nil {
		t.Fatalf("expected acquire after release, got %v", err)
	}
}

func TestCoordinatorReleaseAll(t *testing.T) {
	c := New()

	// Acquire multiple resources
	_ = c.Acquire(context.Background(), "agent1", "r1")
	_ = c.Acquire(context.Background(), "agent1", "r2")
	_ = c.Acquire(context.Background(), "agent2", "r3")

	// Release all for agent1
	c.ReleaseAll("agent1")

	// agent1's resources should be free
	if err := c.Acquire(context.Background(), "agent3", "r1"); err != nil {
		t.Error("expected r1 to be free")
	}
	if err := c.Acquire(context.Background(), "agent3", "r2"); err != nil {
		t.Error("expected r2 to be free")
	}

	// agent2's resource should still be locked
	if err := c.Acquire(context.Background(), "agent3", "r3"); err == nil {
		t.Error("expected r3 to still be locked")
	}
}

// ABOUTME: Tests for the resource coordinator - locking functionality.
// ABOUTME: Verifies thread-safe resource locking across agents.
package coordinator

import (
	"context"
	"testing"
	"time"
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

func TestRateLimiterTake(t *testing.T) {
	// Small bucket for fast testing
	rl := NewRateLimiter(10, 100) // 10 capacity, 100/sec refill

	// Should be able to take up to capacity
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := rl.Take(ctx, 1); err != nil {
			t.Fatalf("expected take %d to succeed, got %v", i, err)
		}
	}

	// Next take should block briefly then succeed (refill)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := rl.Take(ctx, 1); err != nil {
		t.Fatalf("expected take after refill to succeed, got %v", err)
	}
}

func TestRateLimiterContextCancel(t *testing.T) {
	rl := NewRateLimiter(1, 0.1) // Very slow refill

	// Drain the bucket
	_ = rl.Take(context.Background(), 1)

	// Try to take with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := rl.Take(ctx, 1)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

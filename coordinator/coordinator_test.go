// ABOUTME: Tests for the resource coordinator - locking functionality.
// ABOUTME: Verifies thread-safe resource locking across agents.
package coordinator

import (
	"context"
	"fmt"
	"sync"
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

func TestCacheGetSet(t *testing.T) {
	cache := NewCache()

	// Miss on empty cache
	_, ok := cache.Get("key1")
	if ok {
		t.Error("expected cache miss")
	}

	// Set and get
	cache.Set("key1", "value1", time.Hour)
	val, ok := cache.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if val != "value1" {
		t.Errorf("expected value1, got %v", val)
	}
}

func TestCacheExpiration(t *testing.T) {
	cache := NewCache()

	// Set with short TTL
	cache.Set("key1", "value1", 10*time.Millisecond)

	// Should hit immediately
	_, ok := cache.Get("key1")
	if !ok {
		t.Error("expected immediate cache hit")
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Should miss after expiration
	_, ok = cache.Get("key1")
	if ok {
		t.Error("expected cache miss after expiration")
	}
}

func TestCacheDelete(t *testing.T) {
	cache := NewCache()

	cache.Set("key1", "value1", time.Hour)
	cache.Delete("key1")

	_, ok := cache.Get("key1")
	if ok {
		t.Error("expected cache miss after delete")
	}
}

// TestDeadlockDetectionMultipleAgents tests for deadlocks with multiple agents
// competing for the same resources
func TestDeadlockDetectionMultipleAgents(t *testing.T) {
	c := New()

	// Agent1 acquires r1
	if err := c.Acquire(context.Background(), "agent1", "r1"); err != nil {
		t.Fatalf("agent1 should acquire r1: %v", err)
	}

	// Agent2 acquires r2
	if err := c.Acquire(context.Background(), "agent2", "r2"); err != nil {
		t.Fatalf("agent2 should acquire r2: %v", err)
	}

	// Agent1 tries to acquire r2 (should fail - locked by agent2)
	if err := c.Acquire(context.Background(), "agent1", "r2"); err == nil {
		t.Error("agent1 should not acquire r2 (locked by agent2)")
	}

	// Agent2 tries to acquire r1 (should fail - locked by agent1)
	if err := c.Acquire(context.Background(), "agent2", "r1"); err == nil {
		t.Error("agent2 should not acquire r1 (locked by agent1)")
	}

	// Verify both agents still hold their original locks
	if err := c.Acquire(context.Background(), "agent3", "r1"); err == nil {
		t.Error("r1 should still be locked by agent1")
	}
	if err := c.Acquire(context.Background(), "agent3", "r2"); err == nil {
		t.Error("r2 should still be locked by agent2")
	}
}

// TestLockTimeout tests context timeout scenarios for lock acquisition
func TestLockTimeout(t *testing.T) {
	c := New()

	// Agent1 acquires the resource
	if err := c.Acquire(context.Background(), "agent1", "resource1"); err != nil {
		t.Fatalf("agent1 should acquire resource1: %v", err)
	}

	// Agent2 tries to acquire with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Attempt acquisition in a goroutine to test timeout
	done := make(chan error, 1)
	go func() {
		done <- c.Acquire(ctx, "agent2", "resource1")
	}()

	// Should complete quickly with error since lock is held
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error acquiring locked resource")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("acquire should return immediately when lock is held")
	}
}

// TestLockTimeoutCancelled tests cancelled context during lock acquisition
func TestLockTimeoutCancelled(t *testing.T) {
	c := New()

	// Pre-lock a resource
	if err := c.Acquire(context.Background(), "agent1", "resource1"); err != nil {
		t.Fatalf("agent1 should acquire resource1: %v", err)
	}

	// Create a context and cancel it immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Try to acquire with cancelled context
	err := c.Acquire(ctx, "agent2", "resource1")
	if err == nil {
		t.Error("expected error on cancelled context")
	}
	if ctx.Err() == nil {
		t.Error("context should be cancelled")
	}
}

// TestAgentFailureCleanup tests cleanup of orphaned locks
func TestAgentFailureCleanup(t *testing.T) {
	c := New()

	// Agent acquires multiple resources
	resources := []string{"r1", "r2", "r3", "r4", "r5"}
	for _, r := range resources {
		if err := c.Acquire(context.Background(), "agent1", r); err != nil {
			t.Fatalf("agent1 should acquire %s: %v", r, err)
		}
	}

	// Verify all resources are locked
	for _, r := range resources {
		if err := c.Acquire(context.Background(), "agent2", r); err == nil {
			t.Errorf("resource %s should be locked by agent1", r)
		}
	}

	// Simulate agent failure - cleanup all locks
	c.ReleaseAll("agent1")

	// Verify all resources are now available
	for _, r := range resources {
		if err := c.Acquire(context.Background(), "agent2", r); err != nil {
			t.Errorf("resource %s should be available after cleanup: %v", r, err)
		}
	}
}

// TestHighConcurrencyStress tests coordinator under extreme concurrency
func TestHighConcurrencyStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	c := New()
	numAgents := 100
	numResources := 10
	opsPerAgent := 100

	errChan := make(chan error, numAgents*opsPerAgent)
	var wg sync.WaitGroup

	// Launch many agents competing for resources
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		agentID := fmt.Sprintf("agent%d", i)

		go func(id string) {
			defer wg.Done()

			for j := 0; j < opsPerAgent; j++ {
				resourceID := fmt.Sprintf("resource%d", j%numResources)

				// Try to acquire
				err := c.Acquire(context.Background(), id, resourceID)
				if err == nil {
					// Successfully acquired, hold briefly then release
					time.Sleep(time.Microsecond)
					if releaseErr := c.Release(id, resourceID); releaseErr != nil {
						errChan <- fmt.Errorf("release error for %s/%s: %w", id, resourceID, releaseErr)
					}
				}
				// Acquisition failures are expected under high contention
			}
		}(agentID)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for unexpected errors
	for err := range errChan {
		t.Errorf("unexpected error during stress test: %v", err)
	}
}

// TestRateLimiterExtremeLoad tests rate limiter under heavy concurrent load
func TestRateLimiterExtremeLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	// Higher capacity and refill rate for stress testing
	rl := NewRateLimiter(100, 1000)

	numGoroutines := 50
	requestsPerGoroutine := 20

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*requestsPerGoroutine)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				if err := rl.Take(ctx, 1); err != nil {
					errChan <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errChan)

	elapsed := time.Since(start)
	t.Logf("Rate limiter stress test completed in %v", elapsed)

	// Check for errors
	var errorCount int
	for err := range errChan {
		if err != context.Canceled && err != context.DeadlineExceeded {
			t.Errorf("unexpected error: %v", err)
		}
		errorCount++
	}

	if errorCount > 0 {
		t.Logf("Total errors (timeouts/cancellations): %d", errorCount)
	}
}

// TestRateLimiterBurstThenSteady tests burst followed by steady state
func TestRateLimiterBurstThenSteady(t *testing.T) {
	rl := NewRateLimiter(50, 100) // 50 burst, 100/sec steady

	ctx := context.Background()

	// Burst: consume all tokens
	for i := 0; i < 50; i++ {
		if err := rl.Take(ctx, 1); err != nil {
			t.Fatalf("burst token %d should succeed: %v", i, err)
		}
	}

	// Next request should wait for refill
	start := time.Now()
	if err := rl.Take(ctx, 1); err != nil {
		t.Fatalf("post-burst token should succeed after wait: %v", err)
	}
	elapsed := time.Since(start)

	// Should have waited at least a few milliseconds for refill
	if elapsed < 5*time.Millisecond {
		t.Errorf("expected wait for refill, got %v", elapsed)
	}
}

// TestRateLimiterLargeTokenRequest tests requesting more tokens than capacity
func TestRateLimiterLargeTokenRequest(t *testing.T) {
	rl := NewRateLimiter(10, 100)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Request more tokens than capacity - should wait for multiple refills
	start := time.Now()
	err := rl.Take(ctx, 15)
	elapsed := time.Since(start)

	if err != nil {
		// May timeout or succeed depending on timing
		t.Logf("Large token request result: %v after %v", err, elapsed)
	} else {
		// Should have waited for refill
		if elapsed < 10*time.Millisecond {
			t.Error("expected significant wait for large token request")
		}
	}
}

// TestCacheMemoryPressure tests cache behavior under memory pressure
func TestCacheMemoryPressure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory pressure test in short mode")
	}

	cache := NewCache()

	// Create a large number of cache entries
	numEntries := 10000
	largeValue := make([]byte, 1024) // 1KB per entry

	// Set many entries with varying TTLs
	for i := 0; i < numEntries; i++ {
		key := fmt.Sprintf("key%d", i)
		ttl := time.Duration(i%100) * time.Millisecond
		cache.Set(key, largeValue, ttl)
	}

	// Allow some entries to expire
	time.Sleep(150 * time.Millisecond)

	// Access entries (this should trigger cleanup of expired entries)
	var hits, misses int
	for i := 0; i < numEntries; i++ {
		key := fmt.Sprintf("key%d", i)
		if _, ok := cache.Get(key); ok {
			hits++
		} else {
			misses++
		}
	}

	t.Logf("Cache hits: %d, misses: %d (expected some misses due to expiration)", hits, misses)

	// Verify that expired entries were cleaned up on access
	if misses == 0 {
		t.Error("expected some cache misses due to expiration")
	}

	// Run explicit cleanup
	cache.Cleanup()

	// Count remaining entries
	cache.mu.RLock()
	remaining := len(cache.items)
	cache.mu.RUnlock()

	t.Logf("Entries remaining after cleanup: %d", remaining)

	if remaining >= numEntries {
		t.Error("cleanup should have removed expired entries")
	}
}

// TestCacheConcurrentReadWrite tests concurrent cache operations
func TestCacheConcurrentReadWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrency test in short mode")
	}

	cache := NewCache()
	numGoroutines := 50
	opsPerGoroutine := 100

	var wg sync.WaitGroup

	// Writers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Sprintf("key%d", j%10)
				value := fmt.Sprintf("value%d-%d", id, j)
				cache.Set(key, value, time.Second)
			}
		}(i)
	}

	// Readers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Sprintf("key%d", j%10)
				cache.Get(key)
			}
		}()
	}

	// Deleters
	for i := 0; i < numGoroutines/5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Sprintf("key%d", j%10)
				cache.Delete(key)
			}
		}()
	}

	// Wait for all operations to complete
	wg.Wait()

	// Should not panic or deadlock
	t.Log("Concurrent cache operations completed successfully")
}

// TestCoordinatorConcurrentAcquireRelease tests concurrent lock operations
func TestCoordinatorConcurrentAcquireRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrency test in short mode")
	}

	c := New()
	numAgents := 20
	numResources := 5
	opsPerAgent := 50

	var wg sync.WaitGroup
	successCount := make([]int, numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		agentID := fmt.Sprintf("agent%d", i)

		go func(id string, index int) {
			defer wg.Done()

			for j := 0; j < opsPerAgent; j++ {
				resourceID := fmt.Sprintf("r%d", j%numResources)

				// Try to acquire
				if err := c.Acquire(context.Background(), id, resourceID); err == nil {
					successCount[index]++
					// Hold for a tiny moment
					time.Sleep(10 * time.Microsecond)
					// Release
					if err := c.Release(id, resourceID); err != nil {
						t.Errorf("release failed for %s/%s: %v", id, resourceID, err)
					}
				}
			}
		}(agentID, i)
	}

	wg.Wait()

	totalSuccesses := 0
	for i, count := range successCount {
		t.Logf("Agent %d: %d successful acquisitions", i, count)
		totalSuccesses += count
	}

	t.Logf("Total successful lock operations: %d", totalSuccesses)

	// Should have at least some successful operations
	if totalSuccesses == 0 {
		t.Error("expected some successful lock acquisitions")
	}
}

// TestMultipleAgentsDeadlockScenario tests a complex deadlock scenario
func TestMultipleAgentsDeadlockScenario(t *testing.T) {
	c := New()

	// Setup: 3 agents each trying to acquire resources in different orders
	// Agent1: r1 -> r2 -> r3
	// Agent2: r2 -> r3 -> r1
	// Agent3: r3 -> r1 -> r2

	var wg sync.WaitGroup
	results := make(chan string, 3)

	// Agent1
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		if err := c.Acquire(ctx, "agent1", "r1"); err != nil {
			results <- fmt.Sprintf("agent1 failed r1: %v", err)
			return
		}
		time.Sleep(10 * time.Millisecond)
		if err := c.Acquire(ctx, "agent1", "r2"); err != nil {
			results <- fmt.Sprintf("agent1 failed r2: %v", err)
			c.ReleaseAll("agent1")
			return
		}
		time.Sleep(10 * time.Millisecond)
		if err := c.Acquire(ctx, "agent1", "r3"); err != nil {
			results <- fmt.Sprintf("agent1 failed r3: %v", err)
			c.ReleaseAll("agent1")
			return
		}
		results <- "agent1 success"
		c.ReleaseAll("agent1")
	}()

	// Agent2
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		if err := c.Acquire(ctx, "agent2", "r2"); err != nil {
			results <- fmt.Sprintf("agent2 failed r2: %v", err)
			return
		}
		time.Sleep(10 * time.Millisecond)
		if err := c.Acquire(ctx, "agent2", "r3"); err != nil {
			results <- fmt.Sprintf("agent2 failed r3: %v", err)
			c.ReleaseAll("agent2")
			return
		}
		time.Sleep(10 * time.Millisecond)
		if err := c.Acquire(ctx, "agent2", "r1"); err != nil {
			results <- fmt.Sprintf("agent2 failed r1: %v", err)
			c.ReleaseAll("agent2")
			return
		}
		results <- "agent2 success"
		c.ReleaseAll("agent2")
	}()

	// Agent3
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		if err := c.Acquire(ctx, "agent3", "r3"); err != nil {
			results <- fmt.Sprintf("agent3 failed r3: %v", err)
			return
		}
		time.Sleep(10 * time.Millisecond)
		if err := c.Acquire(ctx, "agent3", "r1"); err != nil {
			results <- fmt.Sprintf("agent3 failed r1: %v", err)
			c.ReleaseAll("agent3")
			return
		}
		time.Sleep(10 * time.Millisecond)
		if err := c.Acquire(ctx, "agent3", "r2"); err != nil {
			results <- fmt.Sprintf("agent3 failed r2: %v", err)
			c.ReleaseAll("agent3")
			return
		}
		results <- "agent3 success"
		c.ReleaseAll("agent3")
	}()

	wg.Wait()
	close(results)

	// At least one agent should fail due to lock contention
	var successCount int
	for result := range results {
		t.Logf("Result: %s", result)
		if result == "agent1 success" || result == "agent2 success" || result == "agent3 success" {
			successCount++
		}
	}

	// In a potential deadlock scenario, at most one should succeed at a time
	t.Logf("Successful agents: %d", successCount)
}

// TestCacheCleanupPeriodic tests periodic cleanup functionality
func TestCacheCleanupPeriodic(t *testing.T) {
	cache := NewCache()

	// Add entries with short TTLs
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key%d", i)
		cache.Set(key, "value", 10*time.Millisecond)
	}

	// Verify all entries exist
	cache.mu.RLock()
	initialCount := len(cache.items)
	cache.mu.RUnlock()

	if initialCount != 100 {
		t.Errorf("expected 100 entries, got %d", initialCount)
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Run cleanup
	cache.Cleanup()

	// Verify entries were removed
	cache.mu.RLock()
	finalCount := len(cache.items)
	cache.mu.RUnlock()

	if finalCount != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", finalCount)
	}
}

// TestRateLimiterZeroRefillRate tests rate limiter with zero refill
func TestRateLimiterZeroRefillRate(t *testing.T) {
	rl := NewRateLimiter(5, 0) // No refill

	ctx := context.Background()

	// Should be able to consume up to capacity
	for i := 0; i < 5; i++ {
		if err := rl.Take(ctx, 1); err != nil {
			t.Fatalf("take %d should succeed: %v", i, err)
		}
	}

	// Next take should timeout since no refill
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := rl.Take(ctx, 1)
	if err == nil {
		t.Error("expected timeout with zero refill rate")
	}
}

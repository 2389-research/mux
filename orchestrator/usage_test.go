// ABOUTME: Tests for token usage tracking - aggregation, snapshots, and reset.
// ABOUTME: Validates usage accumulation across multiple requests.
package orchestrator

import (
	"testing"

	"github.com/2389-research/mux/llm"
)

func TestNewTokenUsage(t *testing.T) {
	u := NewTokenUsage()
	if u == nil {
		t.Fatal("NewTokenUsage returned nil")
	}
	if u.Total() != 0 {
		t.Errorf("initial Total() = %d, want 0", u.Total())
	}
}

func TestTokenUsageAdd(t *testing.T) {
	u := NewTokenUsage()

	u.Add(llm.Usage{InputTokens: 100, OutputTokens: 50})

	snapshot := u.Snapshot()
	if snapshot.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", snapshot.InputTokens)
	}
	if snapshot.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", snapshot.OutputTokens)
	}
	if snapshot.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1", snapshot.RequestCount)
	}
}

func TestTokenUsageAddMultiple(t *testing.T) {
	u := NewTokenUsage()

	u.Add(llm.Usage{InputTokens: 100, OutputTokens: 50})
	u.Add(llm.Usage{InputTokens: 200, OutputTokens: 100})
	u.Add(llm.Usage{InputTokens: 50, OutputTokens: 25})

	snapshot := u.Snapshot()
	if snapshot.InputTokens != 350 {
		t.Errorf("InputTokens = %d, want 350", snapshot.InputTokens)
	}
	if snapshot.OutputTokens != 175 {
		t.Errorf("OutputTokens = %d, want 175", snapshot.OutputTokens)
	}
	if snapshot.RequestCount != 3 {
		t.Errorf("RequestCount = %d, want 3", snapshot.RequestCount)
	}
	if u.Total() != 525 {
		t.Errorf("Total() = %d, want 525", u.Total())
	}
}

func TestTokenUsageAddWithCache(t *testing.T) {
	u := NewTokenUsage()

	u.AddWithCache(llm.Usage{InputTokens: 100, OutputTokens: 50}, 20, 10)

	snapshot := u.Snapshot()
	if snapshot.CacheReadTokens != 20 {
		t.Errorf("CacheReadTokens = %d, want 20", snapshot.CacheReadTokens)
	}
	if snapshot.CacheWriteTokens != 10 {
		t.Errorf("CacheWriteTokens = %d, want 10", snapshot.CacheWriteTokens)
	}
}

func TestTokenUsageReset(t *testing.T) {
	u := NewTokenUsage()

	u.Add(llm.Usage{InputTokens: 100, OutputTokens: 50})
	u.Reset()

	snapshot := u.Snapshot()
	if snapshot.InputTokens != 0 {
		t.Errorf("InputTokens after reset = %d, want 0", snapshot.InputTokens)
	}
	if snapshot.RequestCount != 0 {
		t.Errorf("RequestCount after reset = %d, want 0", snapshot.RequestCount)
	}
}

func TestTokenUsageSnapshot(t *testing.T) {
	u := NewTokenUsage()
	u.Add(llm.Usage{InputTokens: 100, OutputTokens: 50})

	snapshot := u.Snapshot()

	// Modify original - snapshot should not change
	u.Add(llm.Usage{InputTokens: 200, OutputTokens: 100})

	if snapshot.InputTokens != 100 {
		t.Errorf("snapshot InputTokens = %d, want 100 (unchanged)", snapshot.InputTokens)
	}
}

func TestTokenUsageString(t *testing.T) {
	u := NewTokenUsage()
	u.Add(llm.Usage{InputTokens: 100, OutputTokens: 50})

	s := u.String()
	if s == "" {
		t.Error("String() returned empty string")
	}
	// Should contain the numbers
	if !contains(s, "100") || !contains(s, "50") || !contains(s, "150") {
		t.Errorf("String() = %q, should contain token counts", s)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestTokenUsageConcurrent(t *testing.T) {
	u := NewTokenUsage()
	done := make(chan bool)

	// Concurrent adds
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				u.Add(llm.Usage{InputTokens: 1, OutputTokens: 1})
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have 1000 adds
	snapshot := u.Snapshot()
	if snapshot.RequestCount != 1000 {
		t.Errorf("RequestCount = %d, want 1000", snapshot.RequestCount)
	}
}

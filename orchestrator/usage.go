// ABOUTME: Implements token usage tracking across sessions.
// ABOUTME: Aggregates input/output/cache tokens and provides usage statistics.
package orchestrator

import (
	"sync"

	"github.com/2389-research/mux/llm"
)

// TokenUsage tracks cumulative token consumption for a session.
type TokenUsage struct {
	mu sync.RWMutex

	// Core token counts
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`

	// Cache tokens (if supported by provider)
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`

	// Request count
	RequestCount int64 `json:"request_count"`
}

// NewTokenUsage creates a new token usage tracker.
func NewTokenUsage() *TokenUsage {
	return &TokenUsage{}
}

// Add records token usage from an LLM response.
func (u *TokenUsage) Add(usage llm.Usage) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.InputTokens += int64(usage.InputTokens)
	u.OutputTokens += int64(usage.OutputTokens)
	u.RequestCount++
}

// AddWithCache records token usage including cache information.
func (u *TokenUsage) AddWithCache(usage llm.Usage, cacheRead, cacheWrite int) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.InputTokens += int64(usage.InputTokens)
	u.OutputTokens += int64(usage.OutputTokens)
	u.CacheReadTokens += int64(cacheRead)
	u.CacheWriteTokens += int64(cacheWrite)
	u.RequestCount++
}

// Total returns the total tokens used (input + output).
func (u *TokenUsage) Total() int64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.InputTokens + u.OutputTokens
}

// Snapshot returns a copy of the current usage statistics.
func (u *TokenUsage) Snapshot() TokenUsage {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return TokenUsage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		RequestCount:     u.RequestCount,
	}
}

// Reset clears all usage statistics.
func (u *TokenUsage) Reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.InputTokens = 0
	u.OutputTokens = 0
	u.CacheReadTokens = 0
	u.CacheWriteTokens = 0
	u.RequestCount = 0
}

// String returns a human-readable summary.
func (u *TokenUsage) String() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return formatTokenUsage(u.InputTokens, u.OutputTokens, u.RequestCount)
}

func formatTokenUsage(input, output, requests int64) string {
	return sprintf("%d input + %d output = %d total (%d requests)",
		input, output, input+output, requests)
}

// sprintf is a simple format helper to avoid importing fmt
func sprintf(format string, args ...int64) string {
	// Simple implementation for the specific format we need
	result := format
	for _, arg := range args {
		result = replaceFirst(result, "%d", itoa(arg))
	}
	return result
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func replaceFirst(s, old, new string) string {
	i := indexOf(s, old)
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

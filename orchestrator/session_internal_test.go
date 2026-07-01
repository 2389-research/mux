// ABOUTME: White-box unit tests for unexported durable-session helpers.
// ABOUTME: Lives in package orchestrator to reach internal decision logic.
package orchestrator

import "testing"

func TestDecisionApproves_PerIDOverride(t *testing.T) {
	d := Decision{Approvals: map[string]bool{"a": true, "b": false}, DefaultApprove: false}
	if !d.approves("a") {
		t.Errorf("approves(a) = false, want true")
	}
	if d.approves("b") {
		t.Errorf("approves(b) = true, want false")
	}
}

func TestDecisionApproves_DefaultFallback(t *testing.T) {
	if got := (Decision{DefaultApprove: true}).approves("missing"); !got {
		t.Errorf("approves(missing) with DefaultApprove=true = false, want true")
	}
	if got := (Decision{}).approves("missing"); got {
		t.Errorf("approves(missing) with zero Decision = true, want false")
	}
}

func TestApprove_SetsDefault(t *testing.T) {
	if !Approve(true).DefaultApprove {
		t.Errorf("Approve(true).DefaultApprove = false, want true")
	}
	if Approve(false).DefaultApprove {
		t.Errorf("Approve(false).DefaultApprove = true, want false")
	}
}

func TestSuspendedError_MentionsSessionAndReason(t *testing.T) {
	s := &Suspended{SessionID: "session-abc", Suspension: Suspension{Reason: ReasonApprovalRequired}}
	msg := s.Error()
	if msg == "" {
		t.Fatal("Suspended.Error() returned empty string")
	}
	// Must reference the session and reason so logs are actionable.
	for _, want := range []string{"session-abc", string(ReasonApprovalRequired)} {
		if !contains(msg, want) {
			t.Errorf("Suspended.Error() = %q, missing %q", msg, want)
		}
	}
}

// contains is defined in usage_test.go (same package).

func TestTokenUsageRestore_CopiesCounters(t *testing.T) {
	src := TokenUsage{
		InputTokens:      11,
		OutputTokens:     22,
		ThinkingTokens:   3,
		CacheReadTokens:  4,
		CacheWriteTokens: 5,
		RequestCount:     6,
	}
	dst := NewTokenUsage()
	dst.Restore(&src)
	got := dst.Snapshot()
	if got != src {
		t.Errorf("Restore counters mismatch: got input=%d out=%d thinking=%d cacheRead=%d cacheWrite=%d req=%d, want input=%d out=%d thinking=%d cacheRead=%d cacheWrite=%d req=%d",
			got.InputTokens, got.OutputTokens, got.ThinkingTokens, got.CacheReadTokens, got.CacheWriteTokens, got.RequestCount,
			src.InputTokens, src.OutputTokens, src.ThinkingTokens, src.CacheReadTokens, src.CacheWriteTokens, src.RequestCount,
		)
	}
}

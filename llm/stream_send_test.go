// ABOUTME: Tests for the cancellable provider stream send helper.
// ABOUTME: Verifies sends unblock on context cancellation without consumer draining.
package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSendStreamEventCancellation verifies the helper abandons a blocked send
// when the context is canceled while the channel buffer is full, and never
// drains the channel to unblock the producer.
func TestSendStreamEventCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: EventMessageStart} // fill the buffer

	done := make(chan bool, 1)
	go func() {
		done <- sendStreamEvent(ctx, ch, StreamEvent{Type: EventError, Error: errors.New("fixture")})
	}()

	cancel()

	select {
	case sent := <-done:
		if sent {
			t.Fatal("reported send into a full canceled channel")
		}
	case <-time.After(time.Second):
		t.Fatal("send remained blocked after cancellation")
	}

	if len(ch) != 1 {
		t.Fatalf("test must not drain to unblock producer, len=%d", len(ch))
	}
}

// TestSendStreamEventDelivers verifies the helper still delivers events when
// the channel has capacity and the context is live.
func TestSendStreamEventDelivers(t *testing.T) {
	ctx := context.Background()
	ch := make(chan StreamEvent, 1)
	if !sendStreamEvent(ctx, ch, StreamEvent{Type: EventMessageStart}) {
		t.Fatal("expected send to succeed on live context")
	}
	select {
	case ev := <-ch:
		if ev.Type != EventMessageStart {
			t.Fatalf("unexpected event: %+v", ev)
		}
	default:
		t.Fatal("expected event to be buffered")
	}
}

// TestSendStreamEventPreCanceled verifies a pre-canceled context never sends.
func TestSendStreamEventPreCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan StreamEvent, 1)
	if sendStreamEvent(ctx, ch, StreamEvent{Type: EventMessageStart}) {
		t.Fatal("send succeeded on canceled context")
	}
	if len(ch) != 0 {
		t.Fatal("canceled context must not enqueue events")
	}
}

// TestSendStreamEventUnblocksWhileBlocked verifies cancellation is observed
// while a send is already blocked, not only before it starts.
func TestSendStreamEventUnblocksWhileBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: EventMessageStart}

	done := make(chan bool, 1)
	go func() {
		done <- sendStreamEvent(ctx, ch, StreamEvent{Type: EventContentDelta, Text: "x"})
	}()

	// Give the producer time to block on the full channel first.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case sent := <-done:
		if sent {
			t.Fatal("reported send into a full canceled channel")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked send did not observe cancellation")
	}
}

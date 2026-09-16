// ABOUTME: Cancellable send helper for provider stream event channels.
// ABOUTME: Guarantees producers abandon blocked sends when consumers stop reading.
package llm

import "context"

// sendStreamEvent delivers an event to a provider's stream channel, abandoning
// the send when the request context is canceled. It returns false when the
// event was not delivered, which every provider producer treats as a signal to
// unwind immediately: the consumer has stopped reading (or will never read),
// so no further event can be observed and continuing would leak the producer
// goroutine and the SDK stream it holds open.
//
// This is the single send path for every provider stream producer. A direct
// channel send inside a producer goroutine is forbidden: without a select on
// ctx.Done(), a send blocked on a full buffer outlives the HTTP request that
// feeds it. That is the leak audited in mux#mgpj.
//
// Delivery semantics under cancellation: once the context is done, the stream
// ends without a guaranteed terminal or error event. Callers that need to
// distinguish cancellation from a clean end must inspect their own context.
func sendStreamEvent(ctx context.Context, eventChan chan<- StreamEvent, event StreamEvent) bool {
	// Fast path: when the context is already dead, prefer honoring
	// cancellation over racing one more event through a channel that still
	// happens to have room. Not a correctness check — the select below is
	// correct on its own either way.
	select {
	case <-ctx.Done():
		return false
	default:
	}
	select {
	case <-ctx.Done():
		return false
	case eventChan <- event:
		return true
	}
}

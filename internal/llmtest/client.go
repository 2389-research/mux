// ABOUTME: Shared scripted llm.Client test double for consumer test suites,
// ABOUTME: with callback-driven responses and recorded request snapshots.
//
// This is test support, not an application mock mode. A test scripts the
// behavior it needs through Respond and Stream, and inspects what the code
// under test sent through Requests. The fixture grows a method only when
// the llm.Client interface does, so a new interface method is one edit here
// instead of one per suite.
//
// Media must be set explicitly by the test: the zero Capabilities reports no
// media support, matching a producer that never declares any.
package llmtest

import (
	"context"
	"errors"
	"sync"

	"github.com/2389-research/mux/internal/llmcopy"
	"github.com/2389-research/mux/llm"
)

// Client is a scripted llm.Client. Respond and Stream may be nil; calling the
// corresponding method then fails with an explicit error rather than panicking,
// which is how a test asserts that the code under test took the wrong path.
type Client struct {
	// Respond scripts CreateMessage. A nil Respond makes any non-stream call fail.
	Respond func(context.Context, *llm.Request) (*llm.Response, error)
	// Stream scripts CreateMessageStream. A nil Stream makes any stream call fail.
	Stream func(context.Context, *llm.Request) (<-chan llm.StreamEvent, error)
	// Media is returned by Capabilities.
	Media llm.Capabilities

	mu       sync.Mutex
	requests []*llm.Request
}

var _ llm.Client = (*Client)(nil)

// record stores an owned deep copy of the request's messages, so later
// mutation by the caller cannot rewrite what Requests reports.
func (c *Client) record(req *llm.Request) {
	if req == nil {
		return
	}
	snapshot := *req
	snapshot.Messages = llmcopy.CloneMessages(req.Messages)
	c.mu.Lock()
	c.requests = append(c.requests, &snapshot)
	c.mu.Unlock()
}

// CreateMessage records the request and delegates to Respond.
func (c *Client) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	c.record(req)
	if c.Respond == nil {
		return nil, errors.New("llmtest: unexpected nonstream request (Respond is nil)")
	}
	return c.Respond(ctx, req)
}

// CreateMessageStream records the request and delegates to Stream.
func (c *Client) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	c.record(req)
	if c.Stream == nil {
		return nil, errors.New("llmtest: unexpected stream request (Stream is nil)")
	}
	return c.Stream(ctx, req)
}

// Capabilities reports the configured Media value.
func (c *Client) Capabilities() llm.Capabilities {
	return c.Media
}

// Requests returns owned snapshots of every request seen so far, in call
// order. Message content is deep-copied per llmcopy; other request fields are
// shallow. Mutating a returned request therefore never reaches the fixture.
func (c *Client) Requests() []*llm.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*llm.Request, len(c.requests))
	for i, req := range c.requests {
		snapshot := *req
		snapshot.Messages = llmcopy.CloneMessages(req.Messages)
		out[i] = &snapshot
	}
	return out
}

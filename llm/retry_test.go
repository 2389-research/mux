// ABOUTME: Tests for the RetryClient decorator that adds exponential backoff
// ABOUTME: retry logic to any llm.Client implementation.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

// failClient is a test double that fails a configurable number of times.
type failClient struct {
	callCount   int
	failCount   int
	err         error
	successResp *Response
}

func (f *failClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	f.callCount++
	if f.callCount <= f.failCount {
		return nil, f.err
	}
	return f.successResp, nil
}

func (f *failClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	f.callCount++
	if f.callCount <= f.failCount {
		return nil, f.err
	}
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: EventMessageStop}
	close(ch)
	return ch, nil
}

func (f *failClient) Capabilities() Capabilities { return FullCapabilities() }

func fastRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries:   3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}
}

func okResponse() *Response {
	return &Response{Content: []ContentBlock{{Type: ContentTypeText, Text: "ok"}}}
}

// makeOpenAIError creates an openai.Error suitable for testing. The SDK's Error()
// method dereferences Request and Response, so we must populate those fields.
func makeOpenAIError(statusCode int) *openai.Error {
	return &openai.Error{
		StatusCode: statusCode,
		Request:    &http.Request{Method: "POST", URL: &url.URL{Path: "/test"}},
		Response:   &http.Response{StatusCode: statusCode},
	}
}

// makeAnthropicError creates an anthropic.Error suitable for testing.
func makeAnthropicError(statusCode int) *anthropic.Error {
	return &anthropic.Error{
		StatusCode: statusCode,
		Request:    &http.Request{Method: "POST", URL: &url.URL{Path: "/test"}},
		Response:   &http.Response{StatusCode: statusCode},
	}
}

func TestRetryClient_RetriesOnServerError(t *testing.T) {
	inner := &failClient{
		failCount:   2,
		err:         makeOpenAIError(500),
		successResp: okResponse(),
	}
	rc := NewRetryClient(inner, fastRetryConfig())
	resp, err := rc.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if resp.Content[0].Text != "ok" {
		t.Errorf("expected 'ok', got %s", resp.Content[0].Text)
	}
	if inner.callCount != 3 {
		t.Errorf("expected 3 calls, got %d", inner.callCount)
	}
}

func TestRetryClient_RetriesOn429(t *testing.T) {
	inner := &failClient{
		failCount:   1,
		err:         makeOpenAIError(429),
		successResp: okResponse(),
	}
	rc := NewRetryClient(inner, fastRetryConfig())
	resp, err := rc.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if resp.Content[0].Text != "ok" {
		t.Errorf("expected 'ok', got %s", resp.Content[0].Text)
	}
	if inner.callCount != 2 {
		t.Errorf("expected 2 calls, got %d", inner.callCount)
	}
}

func TestRetryClient_RetriesOnAnthropicOverloaded(t *testing.T) {
	inner := &failClient{
		failCount:   1,
		err:         makeAnthropicError(529),
		successResp: okResponse(),
	}
	rc := NewRetryClient(inner, fastRetryConfig())
	_, err := rc.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	// 529 is not in retryableStatusCodes, so it should not be retried
	if err == nil {
		t.Fatal("expected error for non-retryable Anthropic status 529")
	}
	if inner.callCount != 1 {
		t.Errorf("expected 1 call, got %d", inner.callCount)
	}
}

func TestRetryClient_RetriesOnAnthropicServerError(t *testing.T) {
	inner := &failClient{
		failCount:   1,
		err:         makeAnthropicError(500),
		successResp: okResponse(),
	}
	rc := NewRetryClient(inner, fastRetryConfig())
	resp, err := rc.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp.Content[0].Text != "ok" {
		t.Errorf("expected 'ok', got %s", resp.Content[0].Text)
	}
	if inner.callCount != 2 {
		t.Errorf("expected 2 calls, got %d", inner.callCount)
	}
}

func TestRetryClient_DoesNotRetryOn400(t *testing.T) {
	inner := &failClient{
		failCount:   5,
		err:         makeOpenAIError(400),
		successResp: okResponse(),
	}
	rc := NewRetryClient(inner, fastRetryConfig())
	_, err := rc.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if inner.callCount != 1 {
		t.Errorf("expected 1 call (no retry for 400), got %d", inner.callCount)
	}
}

func TestRetryClient_ExhaustsRetries(t *testing.T) {
	inner := &failClient{
		failCount:   10,
		err:         makeOpenAIError(500),
		successResp: okResponse(),
	}
	rc := NewRetryClient(inner, fastRetryConfig())
	_, err := rc.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// 1 initial + 3 retries = 4
	if inner.callCount != 4 {
		t.Errorf("expected 4 calls, got %d", inner.callCount)
	}
}

func TestRetryClient_RespectsContextCancellation(t *testing.T) {
	inner := &failClient{
		failCount: 10,
		err:       makeOpenAIError(500),
	}
	rc := NewRetryClient(inner, &RetryConfig{
		MaxRetries:   10,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := rc.CreateMessage(ctx, &Request{Messages: []Message{NewUserMessage("hi")}})
	if err == nil {
		t.Fatal("expected context error")
	}
}

func TestRetryClient_StreamRetriesOnConnectionError(t *testing.T) {
	inner := &failClient{
		failCount:   1,
		err:         makeOpenAIError(502),
		successResp: okResponse(),
	}
	rc := NewRetryClient(inner, fastRetryConfig())
	ch, err := rc.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	for range ch {
	}
	if inner.callCount != 2 {
		t.Errorf("expected 2 calls, got %d", inner.callCount)
	}
}

func TestRetryClient_DefaultConfig(t *testing.T) {
	inner := &failClient{failCount: 0, successResp: okResponse()}
	rc := NewRetryClient(inner, nil)
	resp, err := rc.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content[0].Text != "ok" {
		t.Errorf("expected 'ok', got %s", resp.Content[0].Text)
	}
}

func TestRetryClient_FallbackStringMatching(t *testing.T) {
	inner := &failClient{
		failCount:   1,
		err:         fmt.Errorf("upstream error: 502 Bad Gateway"),
		successResp: okResponse(),
	}
	rc := NewRetryClient(inner, fastRetryConfig())
	resp, err := rc.CreateMessage(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("expected success after retry on string-matched 502, got: %v", err)
	}
	if resp.Content[0].Text != "ok" {
		t.Errorf("expected 'ok', got %s", resp.Content[0].Text)
	}
}

func TestRetryClient_StreamDoesNotRetryOn401(t *testing.T) {
	inner := &failClient{
		failCount:   5,
		err:         makeOpenAIError(401),
		successResp: okResponse(),
	}
	rc := NewRetryClient(inner, fastRetryConfig())
	_, err := rc.CreateMessageStream(context.Background(), &Request{Messages: []Message{NewUserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if inner.callCount != 1 {
		t.Errorf("expected 1 call (no retry for 401), got %d", inner.callCount)
	}
}

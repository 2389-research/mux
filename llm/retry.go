// ABOUTME: RetryClient wraps any llm.Client with exponential backoff retry logic.
// ABOUTME: Retries on transient errors (429, 500, 502, 503, 504) while failing fast on client errors.
package llm

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

// RetryConfig controls retry behavior.
type RetryConfig struct {
	MaxRetries   int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
}

// DefaultRetryConfig returns sensible defaults for retry behavior.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
	}
}

// RetryClient wraps a Client with retry logic for transient errors.
type RetryClient struct {
	inner  Client
	config RetryConfig
}

// NewRetryClient creates a RetryClient wrapping the given client.
// If config is nil, DefaultRetryConfig is used.
func NewRetryClient(inner Client, config *RetryConfig) *RetryClient {
	cfg := DefaultRetryConfig()
	if config != nil {
		cfg = *config
	}
	return &RetryClient{inner: inner, config: cfg}
}

// CreateMessage sends a message with retry on transient errors.
func (r *RetryClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	var lastErr error
	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := r.backoff(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		resp, err := r.inner.CreateMessage(ctx, req)
		if err == nil {
			return resp, nil
		}
		if !isRetryable(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// CreateMessageStream creates a streaming message with retry on connection errors.
// Only retries on initial connection failure (error return), not mid-stream errors.
func (r *RetryClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	var lastErr error
	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := r.backoff(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		ch, err := r.inner.CreateMessageStream(ctx, req)
		if err == nil {
			return ch, nil
		}
		if !isRetryable(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// backoff calculates the delay for a given retry attempt with jitter.
func (r *RetryClient) backoff(attempt int) time.Duration {
	delay := float64(r.config.InitialDelay) * math.Pow(r.config.Multiplier, float64(attempt-1))
	if delay > float64(r.config.MaxDelay) {
		delay = float64(r.config.MaxDelay)
	}
	// Add jitter: 75-100% of calculated delay
	jitter := 0.75 + cryptoFloat64()*0.25
	return time.Duration(delay * jitter)
}

// retryableStatusCodes are HTTP status codes that should trigger a retry.
var retryableStatusCodes = map[int]bool{
	429: true, 500: true, 502: true, 503: true, 504: true,
}

// isRetryable determines if an error should trigger a retry.
// Uses errors.As to check SDK-specific error types.
func isRetryable(err error) bool {
	// Check Anthropic SDK errors
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return retryableStatusCodes[anthropicErr.StatusCode]
	}

	// Check OpenAI SDK errors (also covers OpenRouter and Ollama)
	var openaiErr *openai.Error
	if errors.As(err, &openaiErr) {
		return retryableStatusCodes[openaiErr.StatusCode]
	}

	// Fallback: check error message for status codes (for Gemini and other providers)
	msg := err.Error()
	for code := range retryableStatusCodes {
		if strings.Contains(msg, fmt.Sprintf("%d", code)) {
			return true
		}
	}

	return false
}

// cryptoFloat64 returns a cryptographically random float64 in [0, 1).
func cryptoFloat64() float64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0.5 // safe fallback; should never happen with crypto/rand
	}
	// Use top 53 bits for a uniform float64 in [0, 1)
	return float64(binary.BigEndian.Uint64(b[:])>>(64-53)) / (1 << 53)
}

// Capabilities delegates to the wrapped client. RetryClient adds no media
// support of its own; it forwards whatever the inner provider declares.
func (r *RetryClient) Capabilities() Capabilities {
	return r.inner.Capabilities()
}

// Compile-time interface assertion.
var _ Client = (*RetryClient)(nil)

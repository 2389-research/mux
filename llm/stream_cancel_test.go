// ABOUTME: Table-driven provider cancellation tests with full event buffers.
// ABOUTME: Verifies producers terminate and release SDK streams without consumer draining.
package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthopt "github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/openai/openai-go/v3"
	openaiopt "github.com/openai/openai-go/v3/option"
	"google.golang.org/genai"
)

// observedBody wraps an HTTP response body so the test can observe when the
// SDK releases the underlying wire stream.
type observedBody struct {
	io.ReadCloser
	once   sync.Once
	closed chan struct{}
}

func newObservedBody(rc io.ReadCloser) *observedBody {
	return &observedBody{ReadCloser: rc, closed: make(chan struct{})}
}

func (b *observedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { close(b.closed) })
	return err
}

// observedClient is an http.Client whose transport wraps every response body
// in an observedBody, so the test can assert when a stream is released. It
// implements both http.RoundTripper and the SDKs' Do-shaped HTTPClient
// interface so one type wires into every provider under test.
type observedClient struct {
	mu   sync.Mutex
	last *observedBody
}

func newObservedClient() *observedClient {
	return &observedClient{}
}

// RoundTrip implements http.RoundTripper (used by the Gemini SDK's transport hook).
func (c *observedClient) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	obs := newObservedBody(resp.Body)
	c.mu.Lock()
	c.last = obs
	c.mu.Unlock()
	resp.Body = obs
	return resp, nil
}

// Do implements the option.HTTPClient interface accepted by WithHTTPClient
// on both the Anthropic and OpenAI SDKs.
func (c *observedClient) Do(req *http.Request) (*http.Response, error) {
	return c.RoundTrip(req)
}

func (c *observedClient) observed() *observedBody {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// waitForFullBuffer blocks until the channel buffer is full or the deadline
// passes. The consumer never reads during this window, matching the kata's
// scenario: a caller that has stopped draining.
func waitForFullBuffer(t *testing.T, ch <-chan StreamEvent) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(ch) < cap(ch) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(ch) < cap(ch) {
		t.Fatalf("event channel never filled: len=%d cap=%d", len(ch), cap(ch))
	}
}

// assertProducerTerminated fails the test unless the SDK released the HTTP
// response body within the timeout, with no consumer draining the channel.
func assertProducerTerminated(t *testing.T, obs *observedBody) {
	t.Helper()
	if obs == nil {
		t.Fatal("no observed response body: transport never saw a request")
	}
	select {
	case <-obs.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("producer still holds the HTTP stream after context cancellation; response body never closed")
	}
}

// sseTextDeltaFixture serves a protocol-valid SSE stream with 250 text
// chunks in the given provider's wire format, then holds the connection
// open. The producer can only stop because the context was canceled — the
// consumer in the test never drains the channel to help it along.
func sseTextDeltaFixture(provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		if provider == "anthropic" {
			// The Anthropic producer rejects a delta for a block that was
			// never started (kata 1kr7): start index 0 once before the
			// delta flood below.
			fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		for i := 0; i < 250; i++ {
			switch provider {
			case "openai":
				fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n")
			case "anthropic":
				fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n")
			case "gemini":
				fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"x\"}]}}]}\n\n")
			default: // ollama, openrouter share the chat-completions shape
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n")
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		// Hold the connection open; only cancellation can release it.
		<-r.Context().Done()
	}
}

// TestProviderStreamCancellationFullBuffer is the acceptance test for the
// audit finding: a consumer that stops draining and cancels its context must
// not leave the producer blocked forever on the full event channel, and the
// SDK stream must be released. The consumer never drains the channel during
// the termination assertion, so a producer that still does a bare channel
// send hangs here exactly as it would in production.
func TestProviderStreamCancellationFullBuffer(t *testing.T) {
	t.Run("openai", func(t *testing.T) {
		server := httptest.NewServer(sseTextDeltaFixture("openai"))
		defer server.Close()

		obs := newObservedClient()
		c := &OpenAIClient{
			client: openai.NewClient(
				openaiopt.WithAPIKey("key"),
				openaiopt.WithBaseURL(server.URL),
				openaiopt.WithHTTPClient(obs),
			),
			model: "gpt-test",
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch, err := c.CreateMessageStream(ctx, &Request{Messages: []Message{NewUserMessage("hi")}})
		if err != nil {
			t.Fatalf("CreateMessageStream: %v", err)
		}
		waitForFullBuffer(t, ch)
		cancel()
		assertProducerTerminated(t, obs.observed())
	})

	t.Run("anthropic", func(t *testing.T) {
		server := httptest.NewServer(sseTextDeltaFixture("anthropic"))
		defer server.Close()

		obs := newObservedClient()
		c := &AnthropicClient{
			client: anthropic.NewClient(
				anthopt.WithAPIKey("key"),
				anthopt.WithBaseURL(server.URL),
				anthopt.WithHTTPClient(obs),
			),
			model: "claude-test",
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch, err := c.CreateMessageStream(ctx, &Request{Messages: []Message{NewUserMessage("hi")}})
		if err != nil {
			t.Fatalf("CreateMessageStream: %v", err)
		}
		waitForFullBuffer(t, ch)
		cancel()
		assertProducerTerminated(t, obs.observed())
	})

	t.Run("gemini", func(t *testing.T) {
		server := httptest.NewServer(sseTextDeltaFixture("gemini"))
		defer server.Close()

		obs := newObservedClient()
		gc, err := genai.NewClient(context.Background(), &genai.ClientConfig{
			APIKey:     "key",
			Backend:    genai.BackendGeminiAPI,
			HTTPClient: &http.Client{Transport: obs},
			HTTPOptions: genai.HTTPOptions{
				BaseURL: server.URL,
			},
		})
		if err != nil {
			t.Fatalf("genai client: %v", err)
		}
		c := &GeminiClient{client: gc, model: "gemini-test"}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch, err := c.CreateMessageStream(ctx, &Request{Messages: []Message{NewUserMessage("hi")}})
		if err != nil {
			t.Fatalf("CreateMessageStream: %v", err)
		}
		waitForFullBuffer(t, ch)
		cancel()
		assertProducerTerminated(t, obs.observed())
	})

	t.Run("ollama", func(t *testing.T) {
		server := httptest.NewServer(sseTextDeltaFixture("ollama"))
		defer server.Close()

		obs := newObservedClient()
		c := &OllamaClient{
			client: openai.NewClient(
				openaiopt.WithAPIKey("ollama"),
				openaiopt.WithBaseURL(server.URL),
				openaiopt.WithHTTPClient(obs),
			),
			model: "llama3.2",
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch, err := c.CreateMessageStream(ctx, &Request{Messages: []Message{NewUserMessage("hi")}})
		if err != nil {
			t.Fatalf("CreateMessageStream: %v", err)
		}
		waitForFullBuffer(t, ch)
		cancel()
		assertProducerTerminated(t, obs.observed())
	})

	t.Run("openrouter", func(t *testing.T) {
		server := httptest.NewServer(sseTextDeltaFixture("openrouter"))
		defer server.Close()

		obs := newObservedClient()
		c := &OpenRouterClient{
			client: openai.NewClient(
				openaiopt.WithAPIKey("key"),
				openaiopt.WithBaseURL(server.URL),
				openaiopt.WithHTTPClient(obs),
			),
			model: "openrouter/test",
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch, err := c.CreateMessageStream(ctx, &Request{Messages: []Message{NewUserMessage("hi")}})
		if err != nil {
			t.Fatalf("CreateMessageStream: %v", err)
		}
		waitForFullBuffer(t, ch)
		cancel()
		assertProducerTerminated(t, obs.observed())
	})
}

// errorPathCases enumerates terminal/error send paths that are reachable
// over the wire (a real or malformed server response can drive the producer
// into each one). Each fixture fills the 100-slot event channel exactly with
// deltas, so the next producer send is the one named in the case, and that
// send blocks on the full channel until context cancellation releases it.
// The connection is then held open, so nothing but cancellation can end the
// producer.
//
// Three newer error-return sites are deliberately not covered here:
// anthropic's content_block_stop tool-input path (stopBlock), and Gemini's
// geminiPartReplay/convertGeminiResponse paths. All three only return a
// non-nil error from json.Marshal on data the client already parsed
// successfully off the wire (a Go struct, not attacker-controlled bytes),
// which is not reachable through any real or malformed SSE payload. They
// are converted to the same sendStreamEvent call as every other site and
// verified by code inspection, not by a dedicated fixture.
var errorPathCases = []struct {
	name     string
	provider string
	fixture  http.HandlerFunc
}{
	{
		// 100 deltas fill the buffer; the response.failed terminal
		// EventError send is the next send and blocks.
		name: "openai/terminal-response.failed", provider: "openai",
		fixture: errorPathHandler(
			sseOpenAIDeltas(100),
			"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"boom\"}}}\n\n"),
	},
	{
		// 100 deltas fill the buffer; an error-shaped frame makes the
		// openai-go SDK's decoder stop with a non-nil Err(), so the
		// post-loop EventError send is the next send and blocks.
		name: "openai/post-loop-stream.Err", provider: "openai",
		fixture: errorPathHandler(
			sseOpenAIDeltas(100),
			"event: error\ndata: {\"type\":\"error\",\"message\":\"boom\"}\n\n"),
	},
	{
		// A content_block_start plus 99 deltas fill the buffer (the
		// Anthropic producer rejects a delta for a block that was never
		// started, kata 1kr7); an SSE error event makes the anthropic SDK
		// surface a non-nil stream.Err(), so the post-loop EventError send
		// blocks.
		name: "anthropic/post-loop-stream.Err", provider: "anthropic",
		fixture: errorPathHandler(
			sseAnthropicDeltas(99),
			"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"boom\"}}\n\n"),
	},
	{
		// start + 99 deltas fill the buffer; a malformed JSON chunk makes
		// genai's stream iterator yield a non-nil error, so the in-loop
		// EventError send is the next send and blocks.
		name: "gemini/in-loop-error", provider: "gemini",
		fixture: errorPathHandler(
			sseGeminiChunks(99),
			"data: {not-json}\n\n"),
	},
	{
		// start + 99 deltas fill the buffer; an error-key chunk sets
		// stream.Err() on the openai-go SDK, so the post-loop EventError
		// send blocks.
		name: "ollama/post-loop-stream.Err", provider: "ollama",
		fixture: errorPathHandler(
			sseChatDeltas(99),
			"data: {\"error\":{\"message\":\"boom\"}}\n\n"),
	},
	{
		// Same shape as ollama on the openrouter producer.
		name: "openrouter/post-loop-stream.Err", provider: "openrouter",
		fixture: errorPathHandler(
			sseChatDeltas(99),
			"data: {\"error\":{\"message\":\"boom\"}}\n\n"),
	},
}

// sseOpenAIDeltas builds n Responses-API text-delta frames.
func sseOpenAIDeltas(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"
	}
	return s
}

// sseAnthropicDeltas builds a content_block_start for index 0 followed by n
// content_block_delta frames. The Anthropic producer rejects a delta for a
// block that was never started (kata 1kr7), so every caller needs the start
// frame; n is the delta count, making n+1 the total buffered-event count.
func sseAnthropicDeltas(n int) string {
	s := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
	for i := 0; i < n; i++ {
		s += "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n"
	}
	return s
}

// sseGeminiChunks builds n generate-content stream chunks, preceded by one
// message-start send from the producer itself (accounted for by callers).
func sseGeminiChunks(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"x\"}]}}]}\n\n"
	}
	return s
}

// sseChatDeltas builds n chat-completions delta frames, preceded by one
// message-start send from the producer itself (accounted for by callers).
func sseChatDeltas(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n"
	}
	return s
}

// errorPathHandler serves the delta prefix followed by the terminal frame,
// flushing so the httptest server doesn't batch them, then holds the
// connection open until the request context is done.
func errorPathHandler(deltas string, terminal string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, deltas)
		fmt.Fprint(w, terminal)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Hold the connection open; cancellation must release it.
		<-r.Context().Done()
	}
}

// requestDoer is the HTTP client shape the SDK options accept; observedClient
// satisfies it.
type requestDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// newErrorPathClient builds the provider client wired to the given HTTP
// client for the terminal/error-path table.
func newErrorPathClient(provider string, serverURL string, doer requestDoer) Client {
	switch provider {
	case "openai":
		return &OpenAIClient{
			client: openai.NewClient(
				openaiopt.WithAPIKey("key"),
				openaiopt.WithBaseURL(serverURL),
				openaiopt.WithHTTPClient(doer),
			),
			model: "gpt-test",
		}
	case "anthropic":
		return &AnthropicClient{
			client: anthropic.NewClient(
				anthopt.WithAPIKey("key"),
				anthopt.WithBaseURL(serverURL),
				anthopt.WithHTTPClient(doer),
			),
			model: "claude-test",
		}
	case "gemini":
		gc, err := genai.NewClient(context.Background(), &genai.ClientConfig{
			APIKey:     "key",
			Backend:    genai.BackendGeminiAPI,
			HTTPClient: &http.Client{Transport: doerToTransport(doer)},
			HTTPOptions: genai.HTTPOptions{
				BaseURL: serverURL,
			},
		})
		if err != nil {
			panic(err)
		}
		return &GeminiClient{client: gc, model: "gemini-test"}
	case "ollama":
		return &OllamaClient{
			client: openai.NewClient(
				openaiopt.WithAPIKey("ollama"),
				openaiopt.WithBaseURL(serverURL),
				openaiopt.WithHTTPClient(doer),
			),
			model: "llama3.2",
		}
	default: // openrouter
		return &OpenRouterClient{
			client: openai.NewClient(
				openaiopt.WithAPIKey("key"),
				openaiopt.WithBaseURL(serverURL),
				openaiopt.WithHTTPClient(doer),
			),
			model: "openrouter/test",
		}
	}
}

// doerToTransport adapts a Do-shaped HTTP client to an http.RoundTripper for
// genai's client config, which takes a Transport rather than a Do-shaped
// client. Only used with observedClient here, whose RoundTrip and Do are
// identical.
func doerToTransport(doer requestDoer) http.RoundTripper {
	if rt, ok := doer.(http.RoundTripper); ok {
		return rt
	}
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return doer.Do(req)
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestProviderStreamTerminalErrorSendsAreCancellable is the acceptance test
// for the terminal and error send paths. Each case fills the event channel
// buffer exactly, so the send named in the case is the next producer send
// and blocks on the full channel. Cancellation must release the producer and
// the SDK stream without any consumer draining — pinning the audit's "all
// sends, including recovery/error paths, must select on cancellation"
// criterion.
func TestProviderStreamTerminalErrorSendsAreCancellable(t *testing.T) {
	for _, tc := range errorPathCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.fixture)
			defer server.Close()

			obs := newObservedClient()
			c := newErrorPathClient(tc.provider, server.URL, obs)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch, err := c.CreateMessageStream(ctx, &Request{Messages: []Message{NewUserMessage("hi")}})
			if err != nil {
				t.Fatalf("CreateMessageStream: %v", err)
			}
			waitForFullBuffer(t, ch)
			cancel()
			// The producer is blocked delivering the terminal/error event
			// into the full channel. Cancellation must unblock it and
			// release the HTTP stream.
			assertProducerTerminated(t, obs.observed())
		})
	}
}

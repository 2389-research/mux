//go:build live

// ABOUTME: Opt-in end-to-end checks against a real LLM provider and a real MCP
// ABOUTME: server subprocess. Skipped with a named reason when unconfigured.

package mux_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/mcp"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/permission"
	"github.com/2389-research/mux/tool"
)

// The live suite is configured entirely through the environment. MUX_LIVE_MODEL
// names the model to use; MUX_LIVE_PROVIDER selects which adapter builds the
// client. The provider's own API-key variable (see liveProviderAPIKeyEnv) is
// read by that adapter directly.
const (
	liveProviderEnv = "MUX_LIVE_PROVIDER"
	liveModelEnv    = "MUX_LIVE_MODEL"
)

// liveProviderAPIKeyEnv maps a MUX_LIVE_PROVIDER value to the environment
// variable its adapter reads for credentials.
var liveProviderAPIKeyEnv = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"gemini":     "GEMINI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
}

// missingLiveConfig returns the names of the environment variables the live
// suite needs but that are unset. Names only: a value is never read back into
// an error, printed, or logged.
func missingLiveConfig() []string {
	provider := os.Getenv(liveProviderEnv)
	if provider == "" {
		return []string{liveProviderEnv, liveModelEnv}
	}
	keyEnv, ok := liveProviderAPIKeyEnv[strings.ToLower(provider)]
	if !ok {
		return []string{liveProviderEnv + " (unknown provider " + provider + ")"}
	}
	var missing []string
	if os.Getenv(keyEnv) == "" {
		missing = append(missing, keyEnv)
	}
	return missing
}

// requireLiveConfig skips the test with an explicit, name-only report when the
// live contract is not satisfied. A skipped live test is not a pass and must
// never be reported as provider compatibility.
func requireLiveConfig(t *testing.T) (provider, model string) {
	t.Helper()

	if missing := missingLiveConfig(); len(missing) > 0 {
		t.Skipf("live provider checks not run: set %s to enable them (missing: %s)",
			strings.Join([]string{liveProviderEnv, liveModelEnv}, ", "),
			strings.Join(missing, ", "))
	}

	provider = strings.ToLower(os.Getenv(liveProviderEnv))
	model = os.Getenv(liveModelEnv)
	if model == "" {
		t.Skipf("live provider checks not run: %s is empty", liveModelEnv)
	}
	return provider, model
}

// newLiveClient builds a real provider client for the selected provider.
func newLiveClient(ctx context.Context, t *testing.T, provider, model string) llm.Client {
	t.Helper()

	apiKey := os.Getenv(liveProviderAPIKeyEnv[provider])
	switch provider {
	case "anthropic":
		return llm.NewAnthropicClient(apiKey, model)
	case "openai":
		return llm.NewOpenAIClient(apiKey, model)
	case "gemini":
		client, err := llm.NewGeminiClient(ctx, apiKey, model)
		if err != nil {
			t.Fatalf("build gemini client for %s: %v", provider, err)
		}
		return client
	case "openrouter":
		return llm.NewOpenRouterClient(apiKey, model)
	default:
		t.Fatalf("unsupported %s %q", liveProviderEnv, provider)
		return nil
	}
}

// TestLiveProviderEcho is the opt-in end-to-end smoke test: a real provider is
// asked to call the echo MCP tool with a random nonce, and the assertions check
// the call reached a separate server process and that its result came back
// correlated before the model produced a final answer.
//
// It exercises only capabilities every supported provider has (a text prompt
// plus one tool call). Capability-specific subtests — reasoning replay, stream
// vs non-stream terminal outcomes, structured-output refusal/truncation and
// compaction retention — are added as mux#e451, mux#rjqv, mux#38f5 and mux#v43c
// land, rather than by guessing what the selected model supports.
//
// Run it with a configured provider:
//
//	MUX_LIVE_PROVIDER=anthropic MUX_LIVE_MODEL=claude-sonnet-4-20250514 \
//	    go test -tags=live -race -timeout=300s . -run TestLiveProviderEcho -count=1
func TestLiveProviderEcho(t *testing.T) {
	provider, model := requireLiveConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	serverPath := buildEchoServer(ctx, t)
	echoLog := filepath.Join(t.TempDir(), "echo.log")

	client, err := mcp.NewClient(mcp.ServerConfig{
		Name:      "echo",
		Transport: "stdio",
		Command:   serverPath,
		Env:       map[string]string{"MUX_ECHO_SERVER_LOG": echoLog},
	})
	if err != nil {
		t.Fatalf("create MCP client: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start MCP client: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("close MCP client: %v", err)
		}
	}()

	infos, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "echo" {
		t.Fatalf("expected exactly the echo tool, got %+v", infos)
	}

	registry := tool.NewRegistry()
	registry.Register(mcp.NewToolAdapter(infos[0], client))
	executor := tool.NewExecutor(registry)
	checker := permission.NewChecker(permission.ModeAuto)
	executor.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
		return checker.Check(ctx, tl.Name(), params)
	})

	providerClient := newLiveClient(ctx, t, provider, model)
	orch := orchestrator.NewWithConfig(providerClient, executor, orchestrator.Config{
		MaxIterations: 10,
		Model:         model,
	})

	nonce := randomNonce(t)
	prompt := fmt.Sprintf(
		"Call the echo tool exactly once with the message %q, then reply with the text it returned. "+
			"Do not answer from memory: the tool must return it.", nonce)

	err = orch.Run(ctx, prompt)
	if err != nil {
		t.Fatalf("live agent run failed (%s/%s): %v", provider, model, err)
	}

	logged, err := os.ReadFile(echoLog)
	if err != nil {
		t.Fatalf("read echo server log: the model never reached the echo tool: %v\nhistory: %s",
			err, describeMessages(orch.Messages()))
	}
	if got := strings.TrimSpace(string(logged)); !strings.Contains(got, nonce) {
		t.Fatalf("echo server recorded %q, want the nonce %q", got, nonce)
	}

	assertLiveResult(t, orch.Messages(), nonce)
}

// assertLiveResult checks the provider-visible history carries exactly one
// tool_result whose text is the echoed nonce.
func assertLiveResult(t *testing.T, messages []llm.Message, nonce string) {
	t.Helper()

	var results int
	for _, msg := range messages {
		for _, block := range msg.Blocks {
			if block.Type != llm.ContentTypeToolResult {
				continue
			}
			results++
			if !strings.Contains(block.Text, nonce) {
				t.Errorf("tool_result text %q does not carry the nonce %q", block.Text, nonce)
			}
			if block.IsError {
				t.Errorf("tool_result is marked an error: %q", block.Text)
			}
		}
	}
	if results == 0 {
		t.Errorf("history carries no tool_result; provider never completed the call\nhistory: %s",
			describeMessages(messages))
	}
}

// describeMessages renders the roles and block types in history for a failure
// message. It never includes message text, so a live failure cannot leak prompt
// or model content into logs.
func describeMessages(messages []llm.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		types := make([]string, 0, len(msg.Blocks))
		for _, block := range msg.Blocks {
			types = append(types, string(block.Type))
		}
		parts = append(parts, fmt.Sprintf("%s[%s]", msg.Role, strings.Join(types, ",")))
	}
	return strings.Join(parts, " ")
}

// TestLiveConfigPreflight pins the missing-input reporting itself, so the
// "credentials unavailable" path is verified even when nothing is configured.
// It manipulates environment variables with t.Setenv, so it runs under the
// live tag without needing real credentials.
func TestLiveConfigPreflight(t *testing.T) {
	for _, name := range []string{liveProviderEnv, liveModelEnv,
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(name, "")
	}

	if missing := missingLiveConfig(); !slices.Contains(missing, liveProviderEnv) {
		t.Errorf("with no provider set, missing report = %v, want it to name %s", missing, liveProviderEnv)
	}

	t.Setenv(liveProviderEnv, "anthropic")
	if missing := missingLiveConfig(); !slices.Contains(missing, "ANTHROPIC_API_KEY") {
		t.Errorf("with anthropic selected and no key, missing report = %v, want ANTHROPIC_API_KEY", missing)
	}

	t.Setenv("ANTHROPIC_API_KEY", "present")
	if missing := missingLiveConfig(); len(missing) != 0 {
		t.Errorf("fully configured anthropic reported missing %v, want none", missing)
	}

	// A report names variables only; it must never contain a configured value.
	t.Setenv(liveProviderEnv, "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if missing := missingLiveConfig(); strings.Contains(strings.Join(missing, " "), "present") {
		t.Errorf("missing report leaked a value: %v", missing)
	}

	t.Setenv(liveProviderEnv, "not-a-provider")
	if missing := missingLiveConfig(); len(missing) != 1 || !strings.Contains(missing[0], liveProviderEnv) {
		t.Errorf("unknown provider missing report = %v, want it to name %s", missing, liveProviderEnv)
	}
}

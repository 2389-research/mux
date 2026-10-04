//go:build integration

// ABOUTME: Verifies HTTP MCP discovery, SSE notifications and lifecycle against a real server process.
// ABOUTME: Uses the official TypeScript SDK transport on an isolated loopback listener.
package mux_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/mux/mcp"
)

func TestMCPHTTPProcessContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	logPath := filepath.Join(t.TempDir(), "http-echo.log")
	receiptPath := filepath.Join(t.TempDir(), "http-receipt.log")
	serverPath := filepath.Join(repoRoot(t), "tests", "servers", "http.mjs")
	processCtx, stopProcess := context.WithCancel(ctx)
	cmd := exec.CommandContext(processCtx, "node", serverPath)
	cmd.Env = append(os.Environ(), "MUX_ECHO_SERVER_LOG="+logPath, "MUX_HTTP_RECEIPT_LOG="+receiptPath)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		stopProcess()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopProcess()
		_ = cmd.Wait() // Context cancellation intentionally stops this owned server.
		if cmd.ProcessState == nil {
			t.Error("HTTP server was not reaped")
		}
	})
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- strings.TrimSpace(line)
	}()
	var url string
	select {
	case url = <-ready:
		if !strings.HasPrefix(url, "http://127.0.0.1:") {
			t.Fatalf("server did not announce a loopback URL: %q", url)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP echo server did not announce readiness")
	}
	client, err := mcp.NewClient(mcp.ServerConfig{Name: "http-echo", Transport: "http", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	infos, err := client.ListTools(ctx)
	if err != nil || len(infos) != 2 {
		t.Fatalf("HTTP discovery: infos=%+v err=%v", infos, err)
	}
	for _, name := range []string{"echo", "wait"} {
		found := false
		for _, info := range infos {
			if info.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("HTTP discovery missing %s", name)
		}
	}
	if result, err := client.CallTool(ctx, "missing", map[string]any{}); err == nil && !result.IsError {
		t.Fatal("unknown HTTP tool succeeded")
	}
	canceled, cancelCall := context.WithCancel(ctx)
	cancelCall()
	if _, err := client.CallTool(canceled, "echo", map[string]any{"message": "must-not-run"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled HTTP call = %v", err)
	}
	activeCtx, cancelActive := context.WithCancel(ctx)
	defer cancelActive()
	activeNonce := randomNonce(t)
	activeResult := make(chan error, 1)
	go func() {
		_, err := client.CallTool(activeCtx, "wait", map[string]any{"message": activeNonce})
		activeResult <- err
	}()
	receiptDeadline := time.NewTimer(5 * time.Second)
	defer receiptDeadline.Stop()
	receiptPoll := time.NewTicker(10 * time.Millisecond)
	defer receiptPoll.Stop()
	received := false
	for !received {
		select {
		case err := <-activeResult:
			t.Fatalf("blocked call finished before cancellation/receipt: %v", err)
		case <-receiptDeadline.C:
			t.Fatal("blocked SDK handler receipt was not observed")
		case <-receiptPoll.C:
			receipt, err := os.ReadFile(receiptPath)
			received = err == nil && string(receipt) == activeNonce+"\n"
		}
	}
	// Receipt proves the request is active; HTTP cancellation does not promise
	// rollback or cancellation of remote application side effects.
	select {
	case err := <-activeResult:
		t.Fatalf("blocked call finished before cancellation: %v", err)
	default:
	}
	cancelActive()
	select {
	case err := <-activeResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("in-flight canceled HTTP call = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight HTTP cancellation did not return promptly")
	}
	message := randomNonce(t) + " ☃"
	result, err := client.CallTool(ctx, "echo", map[string]any{"message": message})
	if err != nil || result.IsError || len(result.Content) != 1 || result.Content[0].Text != message {
		t.Fatalf("HTTP echo: result=%+v err=%v", result, err)
	}
	assertProcessLog(t, logPath, message+"\n")
	select {
	case notification := <-client.Notifications():
		if notification.Method != "notifications/message" || !strings.Contains(string(notification.Params), message) {
			t.Fatalf("HTTP echo notification = %+v", notification)
		}
	case <-ctx.Done():
		t.Fatal("HTTP echo notification missing")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(ctx); !errors.Is(err, mcp.ErrTransportClosed) {
		t.Fatalf("HTTP ListTools after close = %v", err)
	}
	if _, open := <-client.Notifications(); open {
		t.Fatal("HTTP notifications remained open after close")
	}
}

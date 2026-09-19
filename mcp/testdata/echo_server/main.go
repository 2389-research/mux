// ABOUTME: Minimal MCP stdio server used by integration tests as a real
// ABOUTME: subprocess: it speaks JSON-RPC 2.0 on stdin/stdout and echoes a nonce.

// Command echo_server is a minimal Model Context Protocol server over stdio.
//
// It exists so the integration suite can exercise the assembled agent -> MCP
// path against an actual child process instead of a skip or an in-process fake.
// It is deliberately small and strict:
//
//   - initialize answers with the protocol version mux negotiates.
//   - notifications/initialized is a notification (no id) and gets no response.
//   - tools/list advertises one "echo" tool with a JSON schema.
//   - tools/call validates the arguments and echoes the supplied message.
//   - An unknown method is rejected with JSON-RPC -32601; malformed arguments
//     with -32602. A bad request is never silently ignored.
//
// This is a local protocol fixture, not a production server and not evidence of
// live provider compatibility. When MUX_ECHO_SERVER_LOG names a file, every
// successful echo appends the nonce it returned, so a test can prove the call
// reached this separate process rather than only mux's in-process state.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	// protocolVersion is the MCP revision mux's stdio client negotiates.
	protocolVersion = "2024-11-05"
	// maxFrameBytes bounds one stdin line, mirroring the client's own ceiling.
	maxFrameBytes = 16 * 1024 * 1024
	// echoLogEnv optionally names a file to append each echoed nonce to.
	echoLogEnv = "MUX_ECHO_SERVER_LOG"
)

// request is a decoded JSON-RPC 2.0 message. Params stays raw so this server
// rejects malformed arguments instead of coercing them.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// response is a JSON-RPC 2.0 response. Exactly one of Result or Error is set.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolCallParams is the tools/call payload.
type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// echoArguments is the echo tool's argument schema.
type echoArguments struct {
	Message string `json:"message"`
}

type toolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type initParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

func main() {
	logPath := os.Getenv(echoLogEnv)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
	out := bufio.NewWriter(os.Stdout)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			// No id is recoverable from an undecodable frame, so report it with
			// a null id rather than dropping it.
			if err := writeResponse(out, response{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "parse error"},
			}); err != nil {
				fmt.Fprintf(os.Stderr, "echo_server: write: %v\n", err)
				os.Exit(1)
			}
			continue
		}

		resp, hasResponse := handle(req, logPath)
		if !hasResponse {
			continue
		}
		if err := writeResponse(out, resp); err != nil {
			fmt.Fprintf(os.Stderr, "echo_server: write: %v\n", err)
			os.Exit(1)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "echo_server: read: %v\n", err)
		os.Exit(1)
	}
}

// handle routes one request. It returns hasResponse=false for notifications,
// which the protocol forbids answering.
func handle(req request, logPath string) (response, bool) {
	notification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		var params initParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorResponse(req, -32602, "invalid initialize params"), true
		}
		version := params.ProtocolVersion
		if version == "" {
			version = protocolVersion
		}
		return response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "echo", "version": "1.0.0"},
			},
		}, true

	case "notifications/initialized", "initialized":
		return response{}, false

	case "tools/list":
		return response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": []toolInfo{echoTool()},
			},
		}, true

	case "tools/call":
		return handleToolCall(req, logPath), true

	default:
		if notification {
			return response{}, false
		}
		return errorResponse(req, -32601, fmt.Sprintf("method not found: %s", req.Method)), true
	}
}

// handleToolCall validates the call and echoes the message back. It logs the
// nonce when a log path was configured, proving the call reached this process.
func handleToolCall(req request, logPath string) response {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req, -32602, "invalid tools/call params")
	}
	if params.Name != "echo" {
		return errorResponse(req, -32602, fmt.Sprintf("unknown tool: %s", params.Name))
	}

	var args echoArguments
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return errorResponse(req, -32602, "echo arguments must be {\"message\": string}")
	}
	if args.Message == "" {
		return errorResponse(req, -32602, "echo requires a non-empty message")
	}

	if logPath != "" {
		//nolint:gosec // G703: logPath is a test harness temp file, not untrusted input.
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return errorResponse(req, -32603, fmt.Sprintf("open echo log: %v", err))
		}
		if _, err := fmt.Fprintln(f, args.Message); err != nil {
			_ = f.Close()
			return errorResponse(req, -32603, fmt.Sprintf("write echo log: %v", err))
		}
		if err := f.Close(); err != nil {
			return errorResponse(req, -32603, fmt.Sprintf("close echo log: %v", err))
		}
	}

	return response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": args.Message}},
			"isError": false,
		},
	}
}

func errorResponse(req request, code int, message string) response {
	return response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   &rpcError{Code: code, Message: message},
	}
}

// writeResponse encodes one response as a single stdout line and flushes it, so
// the client's line scanner sees a complete frame immediately.
func writeResponse(out *bufio.Writer, resp response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	if _, err := out.Write(data); err != nil {
		return err
	}
	if err := out.WriteByte('\n'); err != nil {
		return err
	}
	return out.Flush()
}

func echoTool() toolInfo {
	return toolInfo{
		Name:        "echo",
		Description: "Echoes the provided message back to the caller.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The text to echo back.",
				},
			},
			"required":             []string{"message"},
			"additionalProperties": false,
		},
	}
}

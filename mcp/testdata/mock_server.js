#!/usr/bin/env node
// ABOUTME: Mock MCP server for testing - responds to JSON-RPC 2.0 protocol
// ABOUTME: messages with predefined tool lists and results.

const readline = require('readline');

const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout,
  terminal: false
});

// Mock tools available
const TOOLS = [
  {
    name: "test_tool",
    description: "A test tool",
    inputSchema: {
      type: "object",
      properties: {
        input: { type: "string" }
      }
    }
  },
  {
    name: "echo_tool",
    description: "Echoes the input",
    inputSchema: {
      type: "object",
      properties: {
        message: { type: "string" }
      }
    }
  }
];

function handleRequest(req) {
  const response = {
    jsonrpc: "2.0",
    id: req.id
  };

  switch (req.method) {
    case "initialize":
      response.result = {
        protocolVersion: "2024-11-05",
        capabilities: {},
        serverInfo: { name: "mock-server", version: "1.0.0" }
      };
      break;

    case "tools/list":
      response.result = {
        tools: TOOLS
      };
      break;

    case "tools/call":
      const toolName = req.params?.name;
      const args = req.params?.arguments || {};

      if (toolName === "test_tool") {
        response.result = {
          content: [
            { type: "text", text: "test result" }
          ]
        };
      } else if (toolName === "echo_tool") {
        response.result = {
          content: [
            { type: "text", text: args.message || "no message" }
          ]
        };
      } else if (toolName === "error_tool") {
        response.result = {
          content: [
            { type: "text", text: "tool error occurred" }
          ],
          isError: true
        };
      } else {
        response.error = {
          code: -32601,
          message: "Tool not found"
        };
        delete response.result;
      }
      break;

    default:
      response.error = {
        code: -32601,
        message: "Method not found"
      };
      delete response.result;
  }

  console.log(JSON.stringify(response));
}

rl.on('line', (line) => {
  try {
    const req = JSON.parse(line);
    // Only respond to requests, not notifications
    if (req.id !== undefined) {
      handleRequest(req);
    }
  } catch (err) {
    // Ignore parse errors
  }
});

// Handle process termination gracefully
process.on('SIGTERM', () => process.exit(0));
process.on('SIGINT', () => process.exit(0));

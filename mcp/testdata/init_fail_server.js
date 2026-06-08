#!/usr/bin/env node
// ABOUTME: Mock MCP server that rejects initialize with a JSON-RPC error and
// ABOUTME: stays alive, to test stdio resource teardown when initialize fails.

const readline = require('readline');

const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout,
  terminal: false
});

rl.on('line', (line) => {
  try {
    const req = JSON.parse(line);
    if (req.id !== undefined && req.method === 'initialize') {
      // Echo the request id so the client matches the response, then refuse.
      console.log(JSON.stringify({
        jsonrpc: "2.0",
        id: req.id,
        error: { code: -32000, message: "initialize refused" }
      }));
    }
    // Ignore everything else and stay alive: a client that does not tear down
    // on init failure will leak this process and its reader goroutine.
  } catch (err) {
    // Ignore parse errors.
  }
});

process.on('SIGTERM', () => process.exit(0));
process.on('SIGINT', () => process.exit(0));

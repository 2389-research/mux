#!/usr/bin/env node
// ABOUTME: Mock MCP server that answers initialize and then exits on the first
// ABOUTME: request, so the client sees EOF while a call is still pending.

const readline = require('readline');

const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout,
  terminal: false
});

rl.on('line', (line) => {
  let req;
  try {
    req = JSON.parse(line);
  } catch (err) {
    return; // Ignore parse errors
  }

  if (req.method === 'initialize') {
    console.log(JSON.stringify({
      jsonrpc: "2.0",
      id: req.id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: {},
        serverInfo: { name: "eof-server", version: "1.0.0" }
      }
    }));
    return;
  }

  // Notifications carry no id: stay alive for them.
  if (req.id === undefined) {
    return;
  }

  // Any real request kills the server, leaving the client's call outstanding.
  process.exit(0);
});

process.on('SIGTERM', () => process.exit(0));
process.on('SIGINT', () => process.exit(0));

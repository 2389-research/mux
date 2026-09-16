#!/usr/bin/env node
// ABOUTME: Mock MCP server that stops reading stdin once initialize is answered,
// ABOUTME: so the client's next large request blocks in a full pipe.

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
  if (req.method !== 'initialize') {
    return;
  }

  // Stop draining stdin before answering, so everything the client sends after
  // the handshake piles up in the pipe.
  rl.pause();
  process.stdin.pause();

  console.log(JSON.stringify({
    jsonrpc: "2.0",
    id: req.id,
    result: {
      protocolVersion: "2024-11-05",
      capabilities: {},
      serverInfo: { name: "blocked-stdin-server", version: "1.0.0" }
    }
  }));
});

// Stay alive without reading stdin. The client kills this process on Close;
// the timer only bounds a stray child if that never happens.
setTimeout(() => process.exit(0), 60000);

process.on('SIGTERM', () => process.exit(0));
process.on('SIGINT', () => process.exit(0));

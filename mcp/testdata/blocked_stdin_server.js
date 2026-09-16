#!/usr/bin/env node
// ABOUTME: Mock MCP server that stops reading stdin once initialize is answered,
// ABOUTME: so the client's next large request blocks in a full pipe.
//
// Usage: blocked_stdin_server.js [resumeAfterMs]
// With no argument the server never drains stdin again. With one, it resumes
// that many milliseconds after the handshake, so a test can watch a frame the
// client stopped waiting for still arrive intact.

const readline = require('readline');

const resumeAfterMs = Number(process.argv[2] || 0);

// Framing counters: every well-formed line the server parses, and every line it
// could not. A client that truncates a frame shows up here as malformed > 0.
let frames = 0;
let malformed = 0;

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
    malformed++;
    return;
  }
  frames++;

  if (req.method === 'initialize') {
    // Stop draining stdin before answering, so everything the client sends
    // after the handshake piles up in the pipe.
    rl.pause();
    process.stdin.pause();

    if (resumeAfterMs > 0) {
      setTimeout(() => {
        process.stdin.resume();
        rl.resume();
      }, resumeAfterMs);
    }

    console.log(JSON.stringify({
      jsonrpc: "2.0",
      id: req.id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: {},
        serverInfo: { name: "blocked-stdin-server", version: "1.0.0" }
      }
    }));
    return;
  }

  if (req.id === undefined || req.id === null) {
    return; // Notification: nothing to answer.
  }

  console.log(JSON.stringify({
    jsonrpc: "2.0",
    id: req.id,
    result: {
      content: [{ type: "text", text: `frames=${frames} malformed=${malformed}` }]
    }
  }));
});

// Stay alive without reading stdin. The client kills this process on Close;
// the timer only bounds a stray child if that never happens.
setTimeout(() => process.exit(0), 60000);

process.on('SIGTERM', () => process.exit(0));
process.on('SIGINT', () => process.exit(0));

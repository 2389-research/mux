// ABOUTME: Hosts a real echo tool using the official MCP SDK HTTP transport.
// ABOUTME: Binds an owned ephemeral loopback listener; the SDK handles all protocol messages.
import { createServer } from 'node:http';
import { appendFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { z } from 'zod';
const server = new McpServer({ name: 'mux-http-contract', version: '1.0.0' }, { capabilities: { logging: {} } });
server.registerTool('echo', { inputSchema: { message: z.string() } }, async ({ message }, extra) => {
  await appendFile(process.env.MUX_ECHO_SERVER_LOG, message + '\n');
  await extra.sendNotification({ method: 'notifications/message', params: { level: 'info', data: message } });
  return { content: [{ type: 'text', text: message }] };
});
// The receipt file proves the request reached this actual handler before the
// client cancels. Only this test-owned wait operation is delayed.
server.registerTool('wait', { inputSchema: { message: z.string() } }, async ({ message }, extra) => {
  await appendFile(process.env.MUX_HTTP_RECEIPT_LOG, message + '\n');
  await new Promise(resolve => {
    const finish = () => {
      clearTimeout(timer);
      extra.signal.removeEventListener('abort', finish);
      resolve();
    };
    const timer = setTimeout(finish, 30_000);
    extra.signal.addEventListener('abort', finish, { once: true });
    if (extra.signal.aborted) finish();
  });
  return { content: [{ type: 'text', text: message }] };
});
const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: () => randomUUID() });
await server.connect(transport);
const http = createServer(async (req, res) => {
  try { await transport.handleRequest(req, res); }
  catch (error) { console.error(error); res.destroy(); }
});
http.listen(0, '127.0.0.1', () => console.log(`http://127.0.0.1:${http.address().port}/mcp`));

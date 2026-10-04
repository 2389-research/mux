// ABOUTME: Configures tools-only end-to-end checks for mux's public Go contracts.
// ABOUTME: Runs one worker without a browser, model provider, or live API calls.
import type { E2EConfig } from 'e2e';

export default {
  targets: [{ name: 'mcp-process', platform: 'tools' }],
  tests: 'tests/mcp.e2e.ts',
  workers: 1,
  retries: 0,
  timeout: 120_000,
  trace: 'off',
  cache: 'off',
} satisfies E2EConfig;

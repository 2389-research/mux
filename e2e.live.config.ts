// ABOUTME: Runs bounded opt-in real provider contracts through the tools-only target.
// ABOUTME: Requires explicit provider, model and credential configuration; retries stay disabled.
import type { E2EConfig } from 'e2e';
const provider = process.env.MUX_LIVE_PROVIDER;
const key = { gemini: 'GEMINI_API_KEY', openrouter: 'OPENROUTER_API_KEY' }[provider ?? ''];
if (!key || !process.env[key] || !process.env.MUX_LIVE_MODEL) {
  throw new Error('Live E2E requires MUX_LIVE_PROVIDER=gemini|openrouter, MUX_LIVE_MODEL, and its API key');
}
export default {
  targets: [{ name: 'live-provider', platform: 'tools' }],
  tests: 'tests/providers.live.e2e.ts', workers: 1, retries: 0,
  timeout: 180_000, trace: 'off', cache: 'off',
} satisfies E2EConfig;

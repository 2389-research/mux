// ABOUTME: Verifies actual provider streaming and representative capability outcomes.
// ABOUTME: Gemini exercises reasoning replay; OpenRouter also exercises token limits and compaction.
import { contract } from './go-contract.ts';
for (const name of [
  'TestLiveProviderStreaming',
  ...(process.env.MUX_LIVE_PROVIDER === 'openrouter' ? ['TestLiveProviderTokenLimit', 'TestLiveProviderCompaction'] : []),
]) {
  contract(`live_${name}`, `real provider contract: ${name}`, '.', [name], `^${name}$`, 'live');
}

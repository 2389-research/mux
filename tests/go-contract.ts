// ABOUTME: Runs real Go MCP and filesystem contracts through tester-army/e2e.
// ABOUTME: Requires explicit Go test passes so skipped or missing tests cannot pass.
import { execFile } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { promisify } from 'node:util';
import { expect, test } from 'e2e';

const run = promisify(execFile);
const root = resolve(import.meta.dirname, '..');

export function contract(flow: string, description: string, pkg: string, names: string[], pattern = `^(${names.join('|')})$`, tags = 'integration') {
  test(description, async () => {
    const log = resolve(root, '.e2e/logs', `${flow}.jsonl`);
    await mkdir(resolve(root, '.e2e/logs'), { recursive: true });
    let stdout = '';
    let stderr = '';
    try {
      ({ stdout, stderr } = await run('go', [
        'test', '-json', '-race', `-tags=${tags}`, '-count=1',
        '-timeout=150s', '-run', pattern, pkg,
      ], { cwd: root, timeout: 160_000, maxBuffer: 4 * 1024 * 1024 }));
    } catch (error) {
      const failure = error as Error & { stdout?: string; stderr?: string };
      stdout = failure.stdout ?? '';
      stderr = failure.stderr ?? failure.message;
      throw new Error(`Go contract ${flow} failed; see ${log}`, { cause: error });
    } finally {
      await writeFile(log, stdout + stderr);
    }
    const events = stdout.trim().split('\n').map(line => JSON.parse(line));
    for (const name of names) {
      await expect(events.filter(event => event.Test === name && event.Action === 'pass').length).toBe(1);
    }
    await expect(events.filter(event => event.Action === 'fail' || event.Action === 'skip').length).toBe(0);
  });
}

import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const launcher = path.join(root, 'bin/otoha.mjs');
const run = (args, cwd = root) => spawnSync(process.execPath, [launcher, ...args], { cwd, encoding: 'utf8' });

test('source launcher preserves project paths, live graph queries and shutdown', { timeout: 30000 }, async () => {
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'otoha-launcher-'));
  let preview;
  try {
    const help = run(['help'], temp);
    assert.equal(help.status, 0);
    assert.match(help.stdout, /Otoha/);
    const unknown = run(['unknown'], temp);
    assert.equal(unknown.status, 2);
    const server = run(['server', '--help'], temp);
    assert.equal(server.status, 0);
    assert.match(server.stdout, /serve/);
    fs.copyFileSync(path.join(root, 'packages/graph/examples/service.system.json'), path.join(temp, 'service.system.json'));
    preview = spawn(process.execPath, [launcher, 'graph', 'preview', 'service.system.json'], { cwd: temp, stdio: ['ignore', 'pipe', 'pipe'] });
    let stderr = '';
    preview.stderr.on('data', chunk => { stderr += chunk; });
    await new Promise((resolve, reject) => {
      let output = '';
      const timer = setTimeout(() => reject(new Error('Preview startup timeout: ' + stderr)), 15000);
      preview.once('error', error => { clearTimeout(timer); reject(error); });
      preview.once('exit', code => { clearTimeout(timer); reject(new Error(`Preview exited ${code}: ${stderr}`)); });
      preview.stdout.on('data', chunk => {
        output += chunk;
        if (output.includes('\n')) {
          clearTimeout(timer);
          try { assert.ok(JSON.parse(output.trim()).url); resolve(); } catch (error) { reject(error); }
        }
      });
    });
    const graph = run(['graph', 'manifest', 'service.system.json'], temp);
    assert.equal(graph.status, 0, graph.stderr);
    assert.ok(JSON.parse(graph.stdout).counts.entities > 0);
  } finally {
    if (preview && preview.exitCode === null) {
      const closed = once(preview, 'close');
      preview.kill('SIGTERM');
      await closed;
    }
    fs.rmSync(temp, { recursive: true, force: true });
  }
});

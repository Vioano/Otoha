import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
fs.mkdirSync(path.join(root, 'dist'), { recursive: true });
const output = path.join(root, 'dist', process.platform === 'win32' ? 'otoha.exe' : 'otoha');
const result = spawnSync(process.env.OTOHA_GO || 'go', ['build', '-o', output, './cmd/otoha'], {
  cwd: path.join(root, 'apps/cli'), stdio: 'inherit',
});
if (result.error) console.error(result.error.message);
process.exitCode = result.status ?? 1;

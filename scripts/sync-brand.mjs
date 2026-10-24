import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
let changed = false;
for (const name of ['otoha-mark.svg', 'otoha-icon.svg']) {
  const source = fs.readFileSync(path.join(root, 'assets/brand', name));
  const destination = path.join(root, 'packages/graph/assets', name);
  if (process.argv.includes('--check')) {
    if (!fs.existsSync(destination) || !source.equals(fs.readFileSync(destination))) {
      console.error(`Brand asset differs: ${destination}`);
      changed = true;
    }
  } else fs.writeFileSync(destination, source);
}
process.exitCode = changed ? 1 : 0;

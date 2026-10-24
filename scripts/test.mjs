import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const python = process.env.OTOHA_PYTHON || (process.platform === 'win32' ? 'python' : 'python3');
const suites = [
  ['graph', process.execPath, ['--test', 'test/system-design.test.mjs', 'test/v1-compatibility.test.mjs', 'test/preview.test.mjs', 'test/output-path.test.mjs', 'test/delivery-contract.test.mjs', 'test/nested-layout.test.mjs', 'test/agent-interface.test.mjs', 'test/team.test.mjs', 'test/reader-ui.test.mjs', 'test/task-board.test.mjs', 'test/atomic-write.test.mjs'], 'packages/graph'],
  ['collaboration', python, ['-m', 'unittest', 'discover', '-s', 'tests', '-v'], 'services/collaboration'],
  ['mailbox', python, ['-m', 'unittest', 'discover', '-s', 'skills/otoha-mailbox/tests', '-v'], 'integrations/mailbox'],
  ['mailbox installer', python, ['-m', 'unittest', 'discover', '-s', 'tests', '-v'], 'integrations/mailbox'],
  ['native CLI', process.env.OTOHA_GO || 'go', ['test', './...'], 'apps/cli'],
  ['native build', process.execPath, ['scripts/build-cli.mjs'], '.'],
  ['client/service integration', python, ['-m', 'unittest', 'discover', '-s', 'test', '-p', 'test_cli_service.py', '-v'], '.'],
  ['launcher', process.execPath, ['--test', 'test/launcher.test.mjs'], '.'],
];
let failed = false;
for (const [name, executable, args, cwd] of suites) {
  if (process.argv[2] && name !== process.argv[2]) continue;
  console.log(`\nOtoha: ${name}`);
  const result = spawnSync(executable, args, { cwd: path.join(root, cwd), stdio: 'inherit' });
  if (result.error) console.error(result.error.message);
  if (result.status !== 0) failed = true;
}
process.exitCode = failed ? 1 : 0;

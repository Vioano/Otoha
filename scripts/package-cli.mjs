import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const args = process.argv.slice(2);
const option = (name, fallback) => {
  const i = args.indexOf(name);
  if (i < 0) return fallback;
  if (!args[i + 1] || args[i + 1].startsWith('--')) throw new Error(`${name} requires a value`);
  return args[i + 1];
};
const version = option('--version', '0.1.0-alpha.1');
if (!/^[A-Za-z0-9][A-Za-z0-9.+-]{0,70}$/.test(version)) throw new Error('Invalid release version');
const output = path.resolve(option('--out', path.join(root, 'dist', 'releases')));
fs.mkdirSync(output, { recursive: true });
const sha256 = data => crypto.createHash('sha256').update(data).digest('hex');
const run = (command, argv, options = {}) => {
  const result = spawnSync(command, argv, { cwd: root, encoding: 'utf8', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.status}): ${result.stderr}`);
  return result.stdout.trim();
};
const sourceFiles = [];
function collect(directory) {
  for (const item of fs.readdirSync(path.join(root, directory), { withFileTypes: true })) {
    const relative = path.posix.join(directory, item.name);
    if (item.isDirectory() && !['__pycache__', 'testdata'].includes(item.name)) collect(relative);
    else if (item.isFile() && /\.(go|html|mod|sum|py)$/.test(item.name)) sourceFiles.push(relative);
  }
}
collect('apps/cli');
collect('services/collaboration');
const inputHash = crypto.createHash('sha256');
for (const file of sourceFiles.sort()) inputHash.update(file).update('\0').update(fs.readFileSync(path.join(root, file))).update('\0');
const build = {
  version,
  built_at_utc: new Date().toISOString(),
  source_commit: run('git', ['rev-parse', 'HEAD']),
  source_dirty: Boolean(run('git', ['status', '--porcelain'])),
  source_inputs_sha256: inputHash.digest('hex'),
  scope: 'Pre-release CLI candidate; release gates and live evidence determine readiness.',
};
const artifacts = [];
for (const [goos, goarch] of [['darwin', 'arm64'], ['linux', 'amd64']]) {
  const name = `otoha-${version}-${goos}-${goarch}`;
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'otoha-package-'));
  const destination = path.join(temporary, name);
  try {
    fs.mkdirSync(path.join(destination, 'bin'), { recursive: true });
    run(process.env.OTOHA_GO || 'go', ['build', '-trimpath', '-ldflags', `-s -w -X main.version=${version}`, '-o', path.join(destination, 'bin', 'otoha'), './cmd/otoha'], {
      cwd: path.join(root, 'apps', 'cli'), env: { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: goarch },
    });
    const server = path.join(destination, 'services', 'collaboration');
    fs.mkdirSync(server, { recursive: true });
    for (const file of fs.readdirSync(path.join(root, 'services', 'collaboration'))) {
      if (file.endsWith('.py')) fs.copyFileSync(path.join(root, 'services', 'collaboration', file), path.join(server, file));
    }
    for (const file of ['LICENSE', 'NOTICE.md']) fs.copyFileSync(path.join(root, file), path.join(destination, file));
    fs.copyFileSync(path.join(root, 'docs', 'AGENT-ENTRY.md'), path.join(destination, 'START-HERE.md'));
    fs.writeFileSync(path.join(destination, 'release.json'), JSON.stringify({ ...build, goos, goarch }, null, 2) + '\n');
    const archive = path.join(output, name + '.tar.gz');
    run('tar', ['-czf', archive, '-C', temporary, name], { env: { ...process.env, COPYFILE_DISABLE: '1' } });
    const bytes = fs.readFileSync(archive);
    artifacts.push({ file: path.basename(archive), platform: `${goos}/${goarch}`, bytes: bytes.length, sha256: sha256(bytes) });
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
}
fs.writeFileSync(path.join(output, 'SHA256SUMS'), artifacts.map(x => `${x.sha256}  ${x.file}`).join('\n') + '\n');
fs.writeFileSync(path.join(output, 'manifest.json'), JSON.stringify({ ...build, artifacts }, null, 2) + '\n');
console.log(JSON.stringify({ output, version, artifacts }, null, 2));

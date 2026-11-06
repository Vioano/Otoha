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
const version = option('--version', '0.1.0-alpha.3-rc.3');
const runtimeManifestURL = `https://github.com/Vioano/Otoha/releases/download/v${version}/runtime-manifest.json`;
const entry = path.resolve(option('--entry', path.join(root, 'docs', 'AGENT-ENTRY.md')));
const withCenterRuntime = args.includes('--with-center-runtime');
const runtimeOnly = args.includes('--runtime-only');
if (runtimeOnly && !withCenterRuntime) throw new Error('--runtime-only requires --with-center-runtime');
const runtimeCache = path.resolve(option('--runtime-cache', path.join(os.homedir(), '.cache', 'otoha-build-runtimes')));
const relayDialIP = option('--relay-dial-ip', '');
if (!/^[A-Za-z0-9][A-Za-z0-9.+-]{0,70}$/.test(version)) throw new Error('Invalid release version');
const output = path.resolve(option('--out', path.join(root, 'dist', 'releases', version)));
fs.mkdirSync(output, { recursive: true });
if (fs.existsSync(path.join(output, 'manifest.json'))) throw new Error('Output already contains a manifest; use a fresh output directory');
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
sourceFiles.push('scripts/package-cli.mjs', 'scripts/runtime-prepare.py', 'scripts/runtime-assets.json');
const inputHash = crypto.createHash('sha256');
for (const file of sourceFiles.sort()) inputHash.update(file).update('\0').update(fs.readFileSync(path.join(root, file))).update('\0');
const build = {
  version,
  built_at_utc: new Date().toISOString(),
  source_commit: run('git', ['rev-parse', 'HEAD']),
  source_dirty: Boolean(run('git', ['status', '--porcelain'])),
  source_inputs_sha256: inputHash.digest('hex'),
  runtime_manifest_url: withCenterRuntime ? runtimeManifestURL : null,
  scope: 'Pre-release CLI candidate; release gates and live evidence determine readiness.',
};
const artifacts = [];
const platforms = [['darwin', 'arm64'], ['linux', 'amd64']];
const backendFiles = ['otoha_server.py', 'otoha_message.py', 'otoha_projection.py', 'otoha_repository.py', 'otoha_graph.py'];
function copyBackend(destination) {
  const server = path.join(destination, 'services', 'collaboration');
  fs.mkdirSync(server, { recursive: true });
  for (const file of backendFiles) fs.copyFileSync(path.join(root, 'services', 'collaboration', file), path.join(server, file));
}
function archiveTree(name, temporary, role, platform) {
  const archive = path.join(output, name + '.tar.gz');
  if (fs.existsSync(archive)) throw new Error(`Refusing to replace ${archive}`);
  run('tar', ['-czf', archive, '-C', temporary, name], { env: { ...process.env, COPYFILE_DISABLE: '1' } });
  const bytes = fs.readFileSync(archive);
  const artifact = { file: path.basename(archive), role, platform, bytes: bytes.length, sha256: sha256(bytes) };
  artifacts.push(artifact);
  return artifact;
}
let runtimeManifest;
if (withCenterRuntime) {
  runtimeManifest = { schema: 'otoha-runtime/1', version, platforms: {}, relay: { url: 'https://otoha.kessokuu.top' } };
  if (relayDialIP) runtimeManifest.relay.dial_ip = relayDialIP;
  for (const [goos, goarch] of platforms) {
    const name = `otoha-center-runtime-${version}-${goos}-${goarch}`;
    const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'otoha-runtime-package-'));
    const destination = path.join(temporary, name);
    try {
      copyBackend(destination);
      run(process.env.OTOHA_PYTHON || 'python3', [path.join(root, 'scripts', 'runtime-prepare.py'), '--platform', `${goos}-${goarch}`, '--cache', runtimeCache, '--out', destination, '--version', version]);
      const componentManifest = JSON.parse(fs.readFileSync(path.join(destination, 'runtimes', 'manifest.json'), 'utf8'));
      const files = {};
      for (const [key, relative] of Object.entries(componentManifest.paths)) {
        const file = fs.readFileSync(path.join(destination, relative));
        files[key] = { path: relative, sha256: sha256(file), bytes: file.length };
      }
      fs.copyFileSync(path.join(root, 'NOTICE.md'), path.join(destination, 'OTOHA-NOTICE.md'));
      const artifact = archiveTree(name, temporary, 'center-runtime', `${goos}/${goarch}`);
      runtimeManifest.platforms[`${goos}/${goarch}`] = {
        url: `https://github.com/Vioano/Otoha/releases/download/v${version}/${artifact.file}`,
        sha256: artifact.sha256, bytes: artifact.bytes, format: 'tar.gz', archive_root: name,
        paths: componentManifest.paths, executables: componentManifest.executables, files,
      };
    } finally {
      fs.rmSync(temporary, { recursive: true, force: true });
    }
  }
  fs.writeFileSync(path.join(output, 'runtime-manifest.json'), JSON.stringify(runtimeManifest, null, 2) + '\n');
}
for (const [goos, goarch] of runtimeOnly ? [] : platforms) {
  const name = `otoha-${version}-${goos}-${goarch}`;
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'otoha-package-'));
  const destination = path.join(temporary, name);
  try {
    fs.mkdirSync(path.join(destination, 'bin'), { recursive: true });
    run(process.env.OTOHA_GO || 'go', ['build', '-trimpath', '-ldflags', `-s -w -X main.version=${version} -X github.com/Vioano/Otoha/apps/cli/internal/otoha.InitRuntimeManifestURL=${runtimeManifestURL}`, '-o', path.join(destination, 'bin', 'otoha'), './cmd/otoha'], {
      cwd: path.join(root, 'apps', 'cli'), env: { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: goarch },
    });
    copyBackend(destination);
    if (runtimeManifest) {
      const contents = JSON.stringify(runtimeManifest, null, 2) + '\n';
      fs.writeFileSync(path.join(destination, 'runtime-manifest.json'), contents);
      fs.mkdirSync(path.join(destination, 'runtimes'), { recursive: true });
      fs.writeFileSync(path.join(destination, 'runtimes', 'manifest.json'), contents);
    }
    fs.mkdirSync(path.join(destination, 'apps', 'cli'), { recursive: true });
    fs.copyFileSync(path.join(root, 'apps', 'cli', 'LICENSE'), path.join(destination, 'apps', 'cli', 'LICENSE'));
    fs.copyFileSync(path.join(root, 'NOTICE.md'), path.join(destination, 'NOTICE.md'));
    fs.copyFileSync(entry, path.join(destination, 'START-HERE.md'));
    if (path.basename(entry) === 'TEAM-AGENT-ENTRY.md') {
      for (const name of ['TEAM-GRAPH-API.md', 'PRIVATE-TEAM.zh-CN.md']) {
        fs.copyFileSync(path.join(root, 'docs', name), path.join(destination, name));
      }
    }
    fs.writeFileSync(path.join(destination, 'release.json'), JSON.stringify({ ...build, goos, goarch }, null, 2) + '\n');
    archiveTree(name, temporary, 'client', `${goos}/${goarch}`);
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
}
fs.writeFileSync(path.join(output, 'SHA256SUMS'), artifacts.map(x => `${x.sha256}  ${x.file}`).join('\n') + '\n');
fs.writeFileSync(path.join(output, 'manifest.json'), JSON.stringify({ ...build, artifacts }, null, 2) + '\n');
console.log(JSON.stringify({ output, version, artifacts }, null, 2));

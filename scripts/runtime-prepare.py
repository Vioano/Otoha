#!/usr/bin/env python3
"""Build one pinned, redistributable Otoha runtime tree; no user-service actions."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import urllib.parse
import urllib.request


def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as source:
        while block := source.read(1024 * 1024):
            result.update(block)
    return result.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--platform', choices=['darwin-arm64', 'linux-amd64'], required=True)
    parser.add_argument('--cache', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--version', required=True)
    args = parser.parse_args()
    lock = json.loads(Path(__file__).with_name('runtime-assets.json').read_text())
    args.cache.mkdir(parents=True, exist_ok=True)
    runtime = args.out / 'runtimes'
    if runtime.exists():
        raise SystemExit('Refusing to replace an existing runtime tree')
    assets = {name: {**component[args.platform], 'version': component['version']}
              for name, component in lock['components'].items()}
    assets.update(lock['common'])

    def fetch(item):
        name, asset = item
        filename = urllib.parse.unquote(Path(urllib.parse.urlsplit(asset['url']).path).name)
        # License source basenames are commonly all LICENSE; preserve their
        # explicit component names in the local build cache.
        if name.startswith('licenses/'):
            filename = Path(name).name
        cached = args.cache / filename
        if not cached.exists():
            partial = cached.with_name(cached.name + '.part')
            with urllib.request.urlopen(asset['url'], timeout=60) as response, partial.open('wb') as output:
                shutil.copyfileobj(response, output)
            partial.replace(cached)
        if cached.stat().st_size != asset['bytes'] or digest(cached) != asset['sha256']:
            raise ValueError('Pinned asset checksum/size mismatch: ' + name)
        return name, cached

    with ThreadPoolExecutor(max_workers=4) as pool:
        downloaded = dict(pool.map(fetch, assets.items()))
    runtime.mkdir(parents=True)
    with tempfile.TemporaryDirectory(prefix='otoha-runtime-extract-') as temporary:
        work = Path(temporary)
        for name in ('surreal', 'frpc', 'python'):
            dest = work / name
            dest.mkdir()
            # All archives are exact official bytes already checked above.
            subprocess.run(['tar', '-xzf', str(downloaded[name]), '-C', str(dest)], check=True)
            if name == 'python':
                shutil.copytree(dest / 'python', runtime / 'python', symlinks=True)
            elif name == 'frpc':
                upstream = next(dest.glob('frp_*'))
                shutil.copy2(upstream / 'frpc', runtime / 'frpc')
                (runtime / 'licenses').mkdir(exist_ok=True)
                shutil.copy2(upstream / 'LICENSE', runtime / 'licenses/frp-LICENSE')
            else:
                shutil.copy2(dest / 'surreal', runtime / 'surreal')
    for name in lock['common']:
        target = runtime / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(downloaded[name], target)
    paths = {'frpc': 'runtimes/frpc', 'surreal': 'runtimes/surreal',
             'python': 'runtimes/python/bin/python3', 'ca_bundle': 'runtimes/ca-bundle.pem',
             'server': 'services/collaboration/otoha_server.py'}
    executables = {}
    for name in ('frpc', 'surreal', 'python'):
        file = args.out / paths[name]
        resolved = file.resolve(strict=True)
        if not resolved.is_relative_to(runtime.resolve()):
            raise ValueError('Executable symlink leaves the runtime tree')
        resolved.chmod(resolved.stat().st_mode | 0o111)
        executables[name] = {'path': paths[name], 'sha256': digest(file), 'bytes': file.stat().st_size}
    manifest = {'schema': 'otoha-runtime-components/1', 'version': args.version,
                'platform': args.platform.replace('-', '/', 1), 'paths': paths,
                'executables': executables, 'sources': assets,
                'notice': 'frp Apache-2.0, SurrealDB BSL-1.1 with upstream terms, Python and bundled dependencies retain their licenses; CA data MPL-2.0. See licenses and the preserved Python tree.'}
    (runtime / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    (runtime / 'NOTICE.txt').write_text(manifest['notice'] + '\n\n' + '\n'.join(
        name + ': ' + asset['url'] for name, asset in assets.items()) + '\n')
    print(json.dumps({'runtime': str(runtime), 'executables': executables, 'paths': paths}))


if __name__ == '__main__':
    main()

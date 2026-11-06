#!/usr/bin/env python3
"""Install and operate one isolated Linux Otoha center (Python stdlib only)."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import secrets
import shutil
import socket
import sqlite3
import subprocess
import sys
import time
import urllib.parse
import urllib.request


MARKER = '# Managed by Otoha scripts/private-team/manage.py'
BACKENDS = ('otoha_server.py', 'otoha_message.py', 'otoha_projection.py',
            'otoha_repository.py', 'otoha_graph.py')
VERSION = re.compile(r'[A-Za-z0-9][A-Za-z0-9.+-]{0,70}')
IDENTIFIER = re.compile(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,79}')
LOCAL_HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def private_json(path, value):
    with path.open('x', encoding='utf-8') as output:
        os.chmod(path, 0o600)
        json.dump(value, output, ensure_ascii=False, indent=2)
        output.write('\n')


def read_json(path):
    return json.loads(path.read_text(encoding='utf-8'))


def emit(value):
    print(json.dumps(value, ensure_ascii=False))


class Deployment:
    def __init__(self, args):
        self.args = args
        self.root = Path(args.root).resolve()
        self.data = Path(args.data_root).resolve()
        for path in (self.root, self.data):
            if not re.fullmatch(r'/[A-Za-z0-9_./+-]+', str(path)) or path == Path('/'):
                raise ValueError('Use a dedicated absolute path without spaces')
        if self.root == self.data or self.root in self.data.parents or self.data in self.root.parents:
            raise ValueError('Release and data roots must be separate directories')
        if not re.fullmatch(r'[a-z][a-z0-9-]{1,40}', args.unit_prefix):
            raise ValueError('Invalid unit prefix')
        if not re.fullmatch(r'[a-z][a-z0-9_-]{1,31}', args.user):
            raise ValueError('Invalid service user')
        self.current = self.root / 'current'
        self.state = self.data / 'state'
        self.config = self.data / 'config'
        self.settings_path = self.config / 'service.json'
        self.owner_path = self.config / 'owner.json'
        self.units = {kind: args.unit_prefix + '-' + kind + '.service'
                      for kind in ('center', 'surrealdb')}

    def require_root(self):
        if sys.platform != 'linux' or os.geteuid() != 0:
            raise ValueError('This operation requires root on Linux with systemd')

    def release(self, version):
        if not VERSION.fullmatch(version):
            raise ValueError('Invalid release version')
        return self.root / 'releases' / version

    def selected_release(self):
        if not self.current.is_symlink():
            raise ValueError('No managed current symlink; initialize first')
        release = self.current.resolve(strict=True)
        if release.parent != self.root / 'releases':
            raise ValueError('current does not point to a managed release')
        read_json(release / 'private-team-release.json')
        return release

    def install(self):
        self.require_root()
        source = Path(self.args.source).resolve(strict=True)
        target = self.release(self.args.version)
        if target.exists():
            raise ValueError('Release already exists; use a new immutable version')
        surreal = Path(self.args.surreal_bin).resolve(strict=True)
        binary_version = run(str(surreal), 'version', capture_output=True).stdout.strip()
        if not re.search(r'\b3\.3\.5\b', binary_version):
            raise ValueError('This deployment recipe is verified against SurrealDB 3.3.5 only')
        inputs = [(source / 'bin' / 'otoha', Path('bin/otoha')),
                  (surreal, Path('bin/surreal')),
                  (Path(__file__).resolve(), Path('libexec/manage.py'))]
        inputs += [(source / 'services' / 'collaboration' / name,
                    Path('services/collaboration') / name) for name in BACKENDS]
        for optional in ('release.json', 'NOTICE.md', 'apps/cli/LICENSE'):
            if (source / optional).is_file():
                inputs.append((source / optional, Path(optional)))
        for original, _ in inputs:
            if not original.is_file():
                raise ValueError('Missing release input: ' + str(original))
        self.root.mkdir(parents=True, exist_ok=True)
        self.root.chmod(0o755)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.parent.chmod(0o755)
        target.mkdir(mode=0o755)
        hashes = {}
        try:
            for original, relative in inputs:
                dest = target / relative
                dest.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(original, dest)
                dest.chmod(0o555 if relative.parts[0] == 'bin' else 0o444)
                hashes[str(relative)] = hashlib.sha256(dest.read_bytes()).hexdigest()
            private_json(target / 'private-team-release.json', {
                'version': self.args.version, 'surreal_version': binary_version,
                'installed_at': datetime.now(timezone.utc).isoformat(), 'sha256': hashes})
            (target / 'private-team-release.json').chmod(0o444)
            for directory, _, _ in os.walk(target, topdown=False):
                Path(directory).chmod(0o555)
        except BaseException:
            # Keep an incomplete staging directory for inspection; never replace
            # it silently with different bytes under the same version name.
            raise
        emit({'installed': str(target), 'activated': False})

    def settings(self):
        return read_json(self.settings_path)

    def owned_units(self):
        for unit in self.units.values():
            path = Path('/etc/systemd/system') / unit
            if not path.exists():
                raise ValueError('Refusing to operate an unmanaged unit: ' + unit)
            content = path.read_text()
            if (MARKER not in content or f'WorkingDirectory={self.data}\n' not in content
                    or f'{self.current}/libexec/manage.py ' not in content
                    or f'User={self.args.user}\n' not in content):
                raise ValueError('Unit belongs to another deployment: ' + unit)

    def require_free_unit_names(self):
        for unit in self.units.values():
            if (Path('/etc/systemd/system') / unit).exists():
                raise ValueError('Unit name is already in use: ' + unit)

    def systemctl(self, action, *kinds):
        self.owned_units()
        run('systemctl', action, *(self.units[kind] for kind in kinds))

    def active(self, kind):
        return subprocess.run(['systemctl', 'is-active', '--quiet', self.units[kind]],
                              check=False).returncode == 0

    def activate(self, release):
        temporary = self.root / ('current-' + secrets.token_hex(6))
        temporary.symlink_to(release)
        os.replace(temporary, self.current)

    def write_units(self):
        args = self.args
        common_args = (f'--root {self.root} --data-root {self.data} '
                       f'--unit-prefix {args.unit_prefix} --user {args.user}')
        self.require_free_unit_names()
        for kind in self.units:
            dependency = (f'Requires={self.units["surrealdb"]}\nAfter={self.units["surrealdb"]}\n'
                          if kind == 'center' else '')
            path = Path('/etc/systemd/system') / self.units[kind]
            path.write_text(
                f'{MARKER}\n[Unit]\nDescription=Isolated Otoha private {kind}\n'
                f'After=network.target\n{dependency}\n'
                f'[Service]\nType=simple\nUser={args.user}\nGroup={args.user}\n'
                f'WorkingDirectory={self.data}\n'
                f'ExecStart=/usr/bin/python3 -B {self.current}/libexec/manage.py '
                f'{common_args} run-{kind}\n'
                'UMask=0077\nRestart=on-failure\nRestartSec=3\nTimeoutStopSec=45\n'
                'NoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\n'
                f'ProtectHome=true\nReadWritePaths={self.data}\n'
                '\n[Install]\nWantedBy=multi-user.target\n', encoding='utf-8')
            path.chmod(0o644)
        run('systemctl', 'daemon-reload')

    def initialize(self):
        self.require_root()
        release = self.release(self.args.version)
        read_json(release / 'private-team-release.json')
        if self.current.exists() or self.current.is_symlink() or self.data.exists():
            raise ValueError('Existing data/current found; refusing initialization or credential replacement')
        self.require_free_unit_names()
        for number in (self.args.center_port, self.args.surreal_port):
            with socket.socket() as probe:
                probe.bind(('127.0.0.1', number))
        for value in (self.args.owner, self.args.project, self.args.channel,
                      self.args.namespace, self.args.database, self.args.graph_id):
            if not IDENTIFIER.fullmatch(value):
                raise ValueError('Invalid project, owner or graph identifier')
        public = urllib.parse.urlsplit(self.args.public_url)
        if public.scheme != 'https' or not public.netloc or public.username or public.password or public.query or public.fragment:
            raise ValueError('public-url must be an HTTPS URL without credentials, query or fragment')
        try:
            account = pwd.getpwnam(self.args.user)
        except KeyError:
            run('useradd', '--system', '--user-group', '--home-dir', str(self.data),
                '--shell', '/usr/sbin/nologin', self.args.user)
            account = pwd.getpwnam(self.args.user)
        self.data.mkdir(mode=0o700, parents=True)
        for name in ('config', 'graph-data', 'backups'):
            (self.data / name).mkdir(mode=0o700)
        settings = {'owner': self.args.owner, 'project': self.args.project,
                    'channel': self.args.channel, 'center_port': self.args.center_port,
                    'surreal_port': self.args.surreal_port, 'public_url': self.args.public_url.rstrip('/')}
        private_json(self.settings_path, settings)
        run('/usr/bin/python3', '-B', str(release / 'services/collaboration/otoha_server.py'),
            'init', '--state', str(self.state), '--owner', self.args.owner,
            '--url', settings['public_url'], '--credentials', str(self.owner_path))
        private_json(self.state / 'graph.json', {
            'url': f'http://127.0.0.1:{self.args.surreal_port}', 'username': 'otoha',
            'password': secrets.token_urlsafe(36), 'namespace': self.args.namespace,
            'projects': {self.args.project: {'database': self.args.database, 'graph_id': self.args.graph_id}}})
        os.chown(self.data, account.pw_uid, account.pw_gid)
        for directory in (self.config, self.state, self.data / 'graph-data'):
            for item in [directory, *directory.rglob('*')]:
                os.chown(item, account.pw_uid, account.pw_gid)
        # The service needs its settings and DB configuration, not the owner
        # bearer credential. Health/bootstrap are root-only operator actions.
        os.chown(self.owner_path, 0, 0)
        self.activate(release)
        self.write_units()
        self.systemctl('enable', 'surrealdb', 'center')
        self.start(check_graph=False)
        result = self.health()
        emit({'initialized': True, 'owner_credentials_file': str(self.owner_path), **result})

    def request(self, endpoint, payload=None):
        token = read_json(self.owner_path)['token']
        data = None if payload is None else json.dumps(payload).encode()
        request = urllib.request.Request(self.center_url() + endpoint, data=data, headers={
            'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
        with LOCAL_HTTP.open(request, timeout=10) as response:
            return json.load(response)

    def center_url(self):
        return 'http://127.0.0.1:' + str(self.settings()['center_port'])

    def bootstrap(self):
        config = self.settings()
        projects = self.request('/v1/projects')['projects']
        if not any(p['id'] == config['project'] for p in projects):
            self.request('/v1/projects', {'id': config['project'], 'name': config['project']})
        channels = self.request('/v1/channels')['channels']
        if not any(c['id'] == config['channel'] for c in channels):
            self.request('/v1/channels', {'id': config['channel'], 'project': config['project'], 'name': config['channel']})

    def wait_http(self, url):
        deadline = time.monotonic() + 30
        while True:
            try:
                with LOCAL_HTTP.open(url, timeout=1) as response:
                    if response.status == 200:
                        return
            except OSError:
                pass
            if time.monotonic() >= deadline:
                raise RuntimeError('Health timeout: ' + url)
            time.sleep(0.5)

    def start(self, check_graph=True):
        self.systemctl('start', 'surrealdb')
        graph = read_json(self.state / 'graph.json')
        self.wait_http(graph['url'] + '/health')
        self.systemctl('start', 'center')
        self.wait_http(self.center_url() + '/health')
        self.bootstrap()
        if check_graph:
            return self.health()

    def health(self):
        config = self.settings()
        graph = read_json(self.state / 'graph.json')
        self.wait_http(graph['url'] + '/health')
        self.wait_http(self.center_url() + '/health')
        identity = self.request('/v1/manifest')
        manifest = self.request('/v1/graph/manifest?' + urllib.parse.urlencode({'project': config['project']}))
        if not identity['actor']['owner'] or not manifest.get('ready'):
            raise RuntimeError('Authenticated center/graph readiness check failed')
        expected = graph['projects'][config['project']]['graph_id']
        if manifest.get('graph_id') != expected:
            raise RuntimeError('Graph binding differs from private configuration')
        return {'release': self.selected_release().name, 'center': 'ok', 'graph': 'ok',
                'project': config['project'], 'graph_id': expected,
                'source_version': manifest.get('source_version'),
                'scope': 'loopback + owner-authenticated graph; public ingress/member access are separate checks'}

    def backup(self, resume=True):
        self.require_root()
        self.owned_units()
        previous = {kind: self.active(kind) for kind in self.units}
        stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
        target = self.data / 'backups' / stamp
        target.mkdir(mode=0o700)
        (target / 'INCOMPLETE').touch(mode=0o600)
        completed = False
        try:
            # Stop clients first, then cleanly close the datastore. The copy is
            # of this isolated deployment only, never any legacy service data.
            self.systemctl('stop', 'center')
            self.systemctl('stop', 'surrealdb')
            for name in ('state', 'config', 'graph-data'):
                shutil.copytree(self.data / name, target / name)
            database = sqlite3.connect((target / 'state/messages.sqlite3').as_uri() + '?mode=ro', uri=True)
            try:
                if database.execute('PRAGMA integrity_check').fetchone()[0] != 'ok':
                    raise RuntimeError('Backup SQLite integrity check failed')
            finally:
                database.close()
            private_json(target / 'backup.json', {
                'created_at': stamp, 'release': self.selected_release().name,
                'method': 'both managed services stopped; state/config/graph-data copied',
                'sqlite_integrity': 'ok', 'restore_tested': False})
            (target / 'INCOMPLETE').unlink()
            completed = True
            return target
        finally:
            if resume or not completed:
                for kind in ('surrealdb', 'center'):
                    if previous[kind]:
                        self.systemctl('start', kind)

    def switch(self):
        self.require_root()
        target = self.release(self.args.version)
        record = read_json(target / 'private-team-release.json')
        current = self.selected_release()
        if target == current:
            raise ValueError('This version is already current')
        if record['surreal_version'] != read_json(current / 'private-team-release.json')['surreal_version']:
            raise ValueError('Backend version change requires a separate migration plan')
        backup = self.backup(resume=False)
        self.activate(target)
        try:
            result = self.start()
        except BaseException:
            self.systemctl('stop', 'center')
            print('New release did not become healthy. Center stopped; data kept. '
                  'No automatic code or database rollback. Backup: ' + str(backup), file=sys.stderr)
            raise
        emit({'switched_from': current.name, 'backup': str(backup), **result})

    def run_service(self, kind):
        release = self.selected_release()
        if kind == 'center':
            config = self.settings()
            argv = ['/usr/bin/python3', '-B', str(release / 'services/collaboration/otoha_server.py'),
                    'serve', '--state', str(self.state), '--host', '127.0.0.1',
                    '--port', str(config['center_port'])]
            os.execv(argv[0], argv)
        graph = read_json(self.state / 'graph.json')
        endpoint = urllib.parse.urlsplit(graph['url'])
        if endpoint.scheme != 'http' or endpoint.hostname != '127.0.0.1' or not endpoint.port:
            raise ValueError('Managed SurrealDB must remain on an explicit loopback port')
        os.environ['SURREAL_USER'] = graph['username']
        os.environ['SURREAL_PASS'] = graph['password']
        argv = [str(release / 'bin/surreal'), 'start', '--no-banner', '--log', 'warn',
                '--bind', f'127.0.0.1:{endpoint.port}', 'surrealkv://' + str(self.data / 'graph-data')]
        os.execv(argv[0], argv)


def port(value):
    number = int(value)
    if not 1024 <= number <= 65535:
        raise argparse.ArgumentTypeError('Use a port between 1024 and 65535')
    return number


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', default='/opt/otoha-private')
    parser.add_argument('--data-root', default='/var/lib/otoha-private')
    parser.add_argument('--unit-prefix', default='otoha-private')
    parser.add_argument('--user', default='otoha-private')
    commands = parser.add_subparsers(dest='command', required=True)
    install = commands.add_parser('install', help='stage an immutable unpacked CLI release + pinned SurrealDB binary')
    install.add_argument('--source', required=True)
    install.add_argument('--version', required=True)
    install.add_argument('--surreal-bin', required=True)
    init = commands.add_parser('init', help='initialize NEW isolated data/config and start the first release')
    init.add_argument('--version', required=True)
    init.add_argument('--public-url', required=True)
    init.add_argument('--owner', default='owner')
    init.add_argument('--project', default='team')
    init.add_argument('--channel', default='general')
    init.add_argument('--namespace', default='otoha')
    init.add_argument('--database', default='otoha_team')
    init.add_argument('--graph-id', default='team')
    init.add_argument('--center-port', type=port, default=28788)
    init.add_argument('--surreal-port', type=port, default=28000)
    commands.add_parser('health', help='loopback health + owner-authenticated graph readiness')
    commands.add_parser('start', help='start existing managed units; no new credentials')
    commands.add_parser('backup', help='stop both services, copy stable data, then resume previous units')
    switch = commands.add_parser('switch', help='backup, select a staged version, restart and check')
    switch.add_argument('--version', required=True)
    for kind in ('center', 'surrealdb'):
        commands.add_parser('run-' + kind, help=argparse.SUPPRESS)
    args = parser.parse_args()
    deployment = Deployment(args)
    if args.command.startswith('run-'):
        deployment.run_service(args.command[4:])
    elif args.command == 'install':
        deployment.install()
    elif args.command == 'init':
        if args.center_port == args.surreal_port:
            raise ValueError('Center and SurrealDB need different ports')
        deployment.initialize()
    elif args.command == 'health':
        deployment.require_root()
        emit(deployment.health())
    elif args.command == 'start':
        deployment.require_root()
        emit(deployment.start())
    elif args.command == 'backup':
        emit({'backup': str(deployment.backup())})
    elif args.command == 'switch':
        deployment.switch()


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print('private-team: ' + str(error), file=sys.stderr)
        raise SystemExit(1)

#!/usr/bin/env python3
"""Captain's CLI: provision/revoke messaging, Gitea and restricted SSH access."""
import argparse
import base64
import hashlib
import json
import pathlib
import re
import secrets
import shlex
import subprocess
import tempfile
import urllib.error
import urllib.request

from otoha import Client, load, write_private

class Gitea:
    def __init__(self, config):
        self.config = config
        self.client = Client(config)
    def call(self, method, path, data=None):
        headers = {'Accept': 'application/json', 'Content-Type': 'application/json'}
        if self.config.get('token'):
            headers['Authorization'] = 'token ' + self.config['token']
        else:
            value = self.config['username'] + ':' + self.config['password']
            headers['Authorization'] = 'Basic ' + base64.b64encode(value.encode()).decode()
        request = urllib.request.Request(self.client.url + '/api/v1' + path, data=json.dumps(data).encode() if data is not None else None, headers=headers, method=method)
        try:
            with self.client.opener.open(request, timeout=30) as result:
                raw = result.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as err:
            raise SystemExit(json.dumps({'component': 'gitea', 'status': err.code, 'path': path}))

def gateway(action, actor, admin, key=None):
    if not re.fullmatch(r'[A-Za-z0-9_.-]+', admin) or admin.startswith('-'):
        raise SystemExit('Use an existing trusted SSH host alias')
    command = ['sudo', '-n', 'python3', '/usr/local/libexec/otoha-collaboration/gateway_members.py', action, actor]
    if key: command += ['--public-key-base64', base64.b64encode(key.encode()).decode()]
    subprocess.run(['ssh', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=10', admin, shlex.join(command)], check=True, capture_output=True)

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--profile', required=True, help='Owner messaging identity')
    p.add_argument('--gitea-profile', required=True, help='Owner Gitea identity; token or username/password')
    p.add_argument('--gateway-admin', default='lightsail-proxy')
    p.add_argument('--gateway-info', required=True, help='Public host, host key, port and forwards JSON')
    p.add_argument('--state', required=True, help='Private captain registry, outside Git')
    sub = p.add_subparsers(dest='command', required=True)
    create = sub.add_parser('invite'); create.add_argument('actor'); create.add_argument('--repo', required=True); create.add_argument('--role', choices=['reader', 'member'], default='member'); create.add_argument('--out', required=True)
    create.add_argument('--name', help='Confirmed human display name')
    create.add_argument('--ttl-days', type=int, choices=range(1, 8), default=1, help='One-time enrollment lifetime, 1–7 days; does not expire an enrolled identity')
    grant = sub.add_parser('grant'); grant.add_argument('actor'); grant.add_argument('--role', choices=['reader', 'member'], required=True)
    revoke = sub.add_parser('revoke'); revoke.add_argument('actor')
    a = p.parse_args()
    if not re.fullmatch(r'[a-z][a-z0-9_-]{0,39}', a.actor):
        raise SystemExit('Member ID must use lowercase letters, digits, underscore or hyphen')
    config = load(a.profile); client = Client(config)
    if not client.call('GET', '/v1/manifest')['actor']['owner']:
        raise SystemExit('Captain owner identity required')
    git = Gitea(load(a.gitea_profile))
    state = pathlib.Path(a.state).expanduser().resolve(); state.mkdir(parents=True, exist_ok=True, mode=0o700)
    if state.is_relative_to(pathlib.Path(__file__).resolve().parent):
        raise SystemExit('Captain registry must be outside the source checkout')
    record_file = state / (a.actor + '.json')
    if a.command == 'invite':
        if record_file.exists() or pathlib.Path(a.out).exists():
            raise SystemExit('Member record or invitation already exists; no implicit replacement')
        if not re.fullmatch(r'[\w.-]+/[\w.-]+', a.repo):
            raise SystemExit('Use owner/repository for the repository')
        # Confirm an existing private repository before creating any identity.
        repository = git.call('GET', '/repos/' + a.repo)
        if not repository.get('private'):
            raise SystemExit('Onboarding requires an existing private repository')
        display_name = a.name or a.actor
        if not display_name.strip() or len(display_name) > 200:
            raise SystemExit('Display name must contain 1–200 characters')
        password = secrets.token_urlsafe(32)
        record = {'actor': a.actor, 'name': display_name, 'repo': a.repo, 'role': a.role, 'status': 'provisioning'}
        write_private(record_file, record)
        user = git.call('POST', '/admin/users', {'username': a.actor, 'full_name': display_name, 'email': a.actor + '@otoha-collaboration.invalid', 'password': password, 'must_change_password': False, 'send_notify': False})
        if user.get('full_name') != display_name:
            # Gitea versions that omit full_name from CreateUserOption require
            # the existing user's profile to be updated explicitly.
            git.call('PATCH', '/admin/users/' + a.actor, {'login_name': a.actor, 'full_name': display_name})
        git.call('PUT', '/repos/' + a.repo + '/collaborators/' + a.actor, {'permission': 'read' if a.role == 'reader' else 'write'})
        with tempfile.TemporaryDirectory() as directory:
            key_path = pathlib.Path(directory) / 'gateway'
            subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-C', 'otoha-collaboration-member-' + a.actor, '-f', str(key_path)], check=True, capture_output=True)
            public = key_path.with_suffix('.pub').read_text().strip()
            gateway('add', a.actor, a.gateway_admin, public)
            invitation = client.call('POST', '/v1/invites', {'actor': a.actor, 'name': display_name, 'grants': {'team': a.role}, 'ttl_seconds': a.ttl_days * 86400})
            raw_info = json.loads(pathlib.Path(a.gateway_info).read_text())
            info = {key: raw_info[key] for key in ('host', 'port', 'user', 'host_public_key', 'forwards', 'gitea_url') if key in raw_info}
            invitation['gateway'] = {**info, 'private_key': key_path.read_text()}
        invitation['name'] = display_name
        invitation['gitea'] = {'url': info.get('gitea_url', 'http://127.0.0.1:13000'), 'username': a.actor, 'password': password, 'repository': a.repo}
        files = {}
        for filename in ('otoha.py', 'onboard.py', 'otoha_mcp.py', 'otoha_codex_bridge.py'):
            text = pathlib.Path(__file__).with_name(filename).read_text()
            files[filename] = {'text': text, 'sha256': hashlib.sha256(text.encode()).hexdigest()}
        guide = pathlib.Path(__file__).with_name('docs') / '接入指南.md'
        text = guide.read_text()
        files['接入指南.md'] = {'text': text, 'sha256': hashlib.sha256(text.encode()).hexdigest()}
        text = guide.with_name('协作协议.md').read_text()
        files['协作协议.md'] = {'text': text, 'sha256': hashlib.sha256(text.encode()).hexdigest()}
        invitation['client_files'] = files
        if config.get('ca_pem'): invitation['ca_pem'] = config['ca_pem']
        write_private(a.out, invitation)
        record['status'] = 'invited'; record['public_key'] = public; record['expires'] = invitation['expires']
        write_private(record_file, record, replace=True)
        print(json.dumps({'actor': a.actor, 'role': a.role, 'repository': a.repo, 'invitation_file': str(pathlib.Path(a.out).resolve())}))
    else:
        record = load(record_file)
        if a.command == 'grant':
            # Revoke Git write before enabling communication write, so a partial
            # failure never silently expands repository authority.
            git.call('PUT', '/repos/' + record['repo'] + '/collaborators/' + a.actor, {'permission': 'read' if a.role == 'reader' else 'write'})
            client.call('POST', '/v1/grants', {'actor': a.actor, 'project': 'team', 'role': a.role})
            record['role'] = a.role
        else:
            # Disabling Otoha and Gitea blocks even previously opened forwards.
            actors = client.call('GET', '/v1/actors')['actors']
            if any(actor['id'] == a.actor for actor in actors):
                client.call('POST', '/v1/actors/disable', {'actor': a.actor})
            client.call('POST', '/v1/invites/revoke', {'actor': a.actor})
            git.call('PATCH', '/admin/users/' + a.actor, {'login_name': a.actor, 'prohibit_login': True})
            git.call('DELETE', '/repos/' + record['repo'] + '/collaborators/' + a.actor)
            gateway('remove', a.actor, a.gateway_admin)
            record['status'] = 'revoked'
        write_private(record_file, record, replace=True)
        print(json.dumps({'actor': a.actor, 'status': record['status'], 'role': record['role']}))

if __name__ == '__main__':
    main()

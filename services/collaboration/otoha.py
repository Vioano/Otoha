#!/usr/bin/env python3
"""CLI for humans and Agents. JSON output, private profiles, resumable events."""
import argparse
import json
import os
import pathlib
import plistlib
import re
import secrets
import socket
import ssl
import subprocess
import sys
import time
import hashlib
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_PROFILE = pathlib.Path.home() / '.config/otoha-collaboration/identity.json'

def protocol_guide():
    directory = pathlib.Path(__file__).resolve().parent
    for path in (directory / '协作协议.md', directory / 'docs/协作协议.md'):
        if path.is_file():
            return {'protocol': 'otoha-collaboration/1', 'guide': path.read_text(),
                    'authority': 'Local human authorization and current server grants'}
    raise SystemExit('Protocol guide is missing; ask the captain for the complete client package')

def write_private(path, data, replace=False):
    p = pathlib.Path(path).expanduser()
    p.parent.mkdir(parents=True, exist_ok=True)
    if p.exists() and not replace:
        raise SystemExit(f'Refusing to replace {p}')
    temp = p.with_name(p.name + '.' + secrets.token_hex(6) + '.tmp')
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as f:
        f.write(json.dumps(data, ensure_ascii=False, indent=2) + '\n')
        f.flush(); os.fsync(f.fileno())
    os.replace(temp, p)

class Client:
    def __init__(self, config):
        self.config = config
        self.url = config['url'].rstrip('/')
        u = urllib.parse.urlsplit(self.url)
        if u.username or u.password or u.query or u.fragment:
            raise SystemExit('Credentials must not be embedded in URLs')
        if u.scheme != 'https' and not (u.scheme == 'http' and u.hostname in ('127.0.0.1', 'localhost', '::1')):
            raise SystemExit('Use HTTPS or a localhost SSH tunnel')
        context = ssl.create_default_context()
        if config.get('ca_pem'):
            context.load_verify_locations(cadata=config['ca_pem'])
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context))

    def call(self, method, path, body=None):
        headers = {'Accept': 'application/json'}
        if self.config.get('token'):
            headers['Authorization'] = 'Bearer ' + self.config['token']
        data = None
        if body is not None:
            headers['Content-Type'] = 'application/json'
            data = json.dumps(body, ensure_ascii=False).encode()
        req = urllib.request.Request(self.url + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=60) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            try:
                result = json.load(e)
            except ValueError:
                result = {'error': 'http_error'}
            raise SystemExit(json.dumps({'status': e.code, **result}))

def load(path):
    p = pathlib.Path(path).expanduser()
    if os.name == 'posix' and p.stat().st_mode & 0o077:
        raise SystemExit(f'Identity file must be private: chmod 600 {p}')
    return json.loads(p.read_text())

def enroll(args):
    invitation = load(args.invite)
    profile = pathlib.Path(args.profile).expanduser()
    pending = profile.with_name(profile.name + '.enrollment')
    if profile.exists():
        raise SystemExit('Identity already exists; use a separate profile for another identity')
    if pending.exists():
        state = load(pending)
        if state['invitation'] != invitation['invitation']:
            raise SystemExit('An enrollment is already pending for a different invitation')
    else:
        state = {'invitation': invitation['invitation'], 'credential': secrets.token_urlsafe(32), 'enrollment_id': secrets.token_hex(16), 'device': args.device or socket.gethostname().split('.')[0]}
        write_private(pending, state)
    config = {k: invitation[k] for k in ('url', 'ca_pem') if k in invitation}
    result = Client(config).call('POST', '/v1/enroll', state)
    config.update({'protocol': 'otoha-collaboration/1', 'actor': result['actor'], 'device': result['device'], 'token': state['credential']})
    for key in ('gateway', 'gitea'):
        if key in invitation:
            config[key] = invitation[key]
    write_private(profile, config)
    pending.unlink()
    return {'enrolled': True, 'actor': result['actor'], 'profile': str(profile)}

def tunnel(args, config):
    gateway = config.get('gateway')
    if not gateway:
        raise SystemExit('This profile has no SSH gateway')
    # Key and host-key paths come from the locally accepted invitation/profile.
    # Pass each argument separately; never turn messages into shell commands.
    key = pathlib.Path(gateway['key_file']).expanduser().resolve()
    known = pathlib.Path(gateway['known_hosts_file']).expanduser().resolve()
    if not key.exists() or not known.exists():
        raise SystemExit('Gateway private key or pinned host key is missing')
    if os.name == 'posix' and key.stat().st_mode & 0o077:
        raise SystemExit('Gateway private key must have mode 600')
    host = gateway['host']; user = gateway.get('user', 'otoha-access')
    if any(x.startswith('-') or any(c.isspace() for c in x) for x in (host, user)):
        raise SystemExit('Invalid SSH host/user')
    forwards = gateway.get('forwards', [{'local': 18788, 'remote': 13001}, {'local': 13000, 'remote': 13000}])
    cmd = ['ssh', '-N', '-T', '-o', 'BatchMode=yes', '-o', 'ExitOnForwardFailure=yes', '-o', 'ServerAliveInterval=20', '-o', 'ServerAliveCountMax=3', '-o', 'StrictHostKeyChecking=yes', '-o', f'UserKnownHostsFile={known}', '-o', 'IdentitiesOnly=yes', '-i', str(key), '-p', str(int(gateway.get('port', 22)))]
    for forward in forwards:
        cmd += ['-L', f"127.0.0.1:{int(forward['local'])}:127.0.0.1:{int(forward['remote'])}"]
    cmd.append(user + '@' + host)
    if os.name == 'posix':
        os.execvp(cmd[0], cmd)
    return subprocess.call(cmd)

def gateway_service(profile, action):
    """Manage only this identity's local reconnecting SSH service."""
    profile = pathlib.Path(profile).expanduser().resolve()
    config = load(profile)
    if not config.get('gateway'):
        raise SystemExit('This identity has no SSH gateway')
    identifier = 'otoha-collaboration-' + hashlib.sha256(str(profile).encode()).hexdigest()[:16]
    command = [str(pathlib.Path(sys.executable).resolve()), str(pathlib.Path(__file__).resolve()), '--profile', str(profile), 'connect']
    logs = profile.parent / 'service-logs'
    logs.mkdir(mode=0o700, exist_ok=True)
    def save(path, content):
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        if path.exists():
            if path.read_bytes() != content:
                raise SystemExit('Existing service differs; refusing to replace it')
        else:
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, 'wb') as output: output.write(content)
    def run(args, check=True):
        result = subprocess.run(args, capture_output=True, text=True)
        if check and result.returncode:
            raise SystemExit(json.dumps({'service_error': args[0], 'exit_code': result.returncode, 'detail': result.stderr.strip()[:500]}))
        return result
    if sys.platform == 'darwin':
        label = 'local.' + identifier
        path = pathlib.Path.home() / 'Library/LaunchAgents' / (label + '.plist')
        domain = 'gui/' + str(os.getuid())
        spec = {'Label': label, 'ProgramArguments': command, 'RunAtLoad': True, 'KeepAlive': True, 'ThrottleInterval': 5, 'ProcessType': 'Background', 'StandardOutPath': str(logs / 'gateway.out.log'), 'StandardErrorPath': str(logs / 'gateway.err.log')}
        content = plistlib.dumps(spec)
        if action == 'install':
            save(path, content)
            if run(['launchctl', 'print', domain + '/' + label], False).returncode:
                run(['launchctl', 'bootstrap', domain, str(path)])
        elif action == 'remove':
            if path.exists() and path.read_bytes() != content:
                raise SystemExit('Existing service differs; refusing to remove it')
            run(['launchctl', 'bootout', domain + '/' + label], False)
            path.unlink(missing_ok=True)
        else:
            result = run(['launchctl', 'print', domain + '/' + label], False)
            return {'service': label, 'loaded': result.returncode == 0, 'detail': result.stdout[:3000], 'unit': str(path)}
    elif sys.platform.startswith('linux'):
        label = identifier + '.service'
        path = pathlib.Path.home() / '.config/systemd/user' / label
        def quote(value):
            if '\n' in value or '\r' in value: raise SystemExit('Newlines are not supported in service paths')
            return '"' + value.replace('\\', '\\\\').replace('"', '\\"').replace('%', '%%').replace('$', '$$') + '"'
        content = ('[Unit]\nDescription=Otoha Collaboration private SSH gateway\n'
                   '[Service]\nExecStart=' + ' '.join(quote(x) for x in command) +
                   '\nRestart=always\nRestartSec=5\nUMask=0077\nNoNewPrivileges=true\n'
                   '[Install]\nWantedBy=default.target\n').encode()
        if action == 'install':
            save(path, content)
            run(['systemctl', '--user', 'daemon-reload'])
            run(['systemctl', '--user', 'enable', '--now', label])
        elif action == 'remove':
            if path.exists() and path.read_bytes() != content:
                raise SystemExit('Existing service differs; refusing to remove it')
            run(['systemctl', '--user', 'disable', '--now', label], False)
            path.unlink(missing_ok=True)
            run(['systemctl', '--user', 'daemon-reload'])
        else:
            result = run(['systemctl', '--user', 'is-active', label], False)
            return {'service': label, 'active': result.returncode == 0, 'state': result.stdout.strip(), 'unit': str(path), 'lifetime': 'user login session; boot without login requires administrator-managed lingering'}
    else:
        raise SystemExit('Use the foreground connect command with a local supervisor on this OS')
    return {'service': label, 'action': action, 'unit': str(path)}

def main():
    os.umask(0o077)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--profile', default=str(DEFAULT_PROFILE))
    sub = p.add_subparsers(dest='command', required=True)
    e = sub.add_parser('enroll'); e.add_argument('--invite', required=True); e.add_argument('--device')
    sub.add_parser('manifest'); sub.add_parser('projects'); sub.add_parser('channels'); sub.add_parser('actors'); sub.add_parser('credentials'); sub.add_parser('protocol')
    agents = sub.add_parser('agents'); agents.add_argument('--project', default='team')
    connect = sub.add_parser('connect', help='Keep private SSH forwards running in foreground; supervisor can restart')
    connect.add_argument('--invite', help='Connect before enrolling with the gateway in an invitation')
    service = sub.add_parser('gateway-service', help='Install/status/remove a local reconnecting gateway service')
    service.add_argument('action', choices=['install', 'status', 'remove'])
    project = sub.add_parser('project-create'); project.add_argument('id'); project.add_argument('--name')
    channel = sub.add_parser('channel-create'); channel.add_argument('id'); channel.add_argument('--project', default='team'); channel.add_argument('--name'); channel.add_argument('--members', nargs='*')
    session = sub.add_parser('session'); session.add_argument('id'); session.add_argument('--agent', default='human'); session.add_argument('--task', help='Optional public task description for this session')
    send = sub.add_parser('send'); send.add_argument('--channel', default='general'); send.add_argument('--session')
    text = send.add_mutually_exclusive_group(required=True); text.add_argument('--text'); text.add_argument('--file')
    send.add_argument('--id', help='Keep the same id when retrying an unknown outcome'); send.add_argument('--kind', default='message'); send.add_argument('--to', nargs='*', default=[]); send.add_argument('--reply-to', type=int); send.add_argument('--refs', help='JSON file with repo, full commit SHA, path references')
    send.add_argument('--delivery', choices=['tool', 'chat', 'both'], default='tool')
    send.add_argument('--source', choices=['human', 'agent', 'unspecified'], default='human'); send.add_argument('--agent'); send.add_argument('--task')
    projection = sub.add_parser('query', help='Read a preset or custom SQL over authorized state')
    strategy = projection.add_mutually_exclusive_group()
    strategy.add_argument('--preset', choices=['recent', 'incremental', 'to-me', 'tasks'])
    strategy.add_argument('--sql'); strategy.add_argument('--sql-file'); strategy.add_argument('--request', help='JSON request file')
    projection.add_argument('--project'); projection.add_argument('--channel'); projection.add_argument('--session')
    projection.add_argument('--after', type=int); projection.add_argument('--before', type=int); projection.add_argument('--limit', type=int)
    projection.add_argument('--params', help='JSON array or object of SQL bind parameters')
    history = sub.add_parser('history'); history.add_argument('--channel', default='general'); history.add_argument('--before', type=int); history.add_argument('--limit', type=int, default=100)
    read = sub.add_parser('read'); read.add_argument('--after', type=int, default=0); read.add_argument('--wait', type=int, default=0); read.add_argument('--channel')
    watch = sub.add_parser('watch'); watch.add_argument('--cursor-file', required=True); watch.add_argument('--channel'); watch.add_argument('--once', action='store_true')
    receipt = sub.add_parser('receipt'); receipt.add_argument('--channel', default='general'); receipt.add_argument('--session', required=True); receipt.add_argument('--received', required=True, type=int); receipt.add_argument('--read', type=int, default=0)
    receipts = sub.add_parser('receipts'); receipts.add_argument('--channel', default='general')
    invite = sub.add_parser('invite'); invite.add_argument('actor'); invite.add_argument('--name'); invite.add_argument('--project', default='team'); invite.add_argument('--role', choices=['reader', 'member'], default='member'); invite.add_argument('--ttl', type=int, default=86400); invite.add_argument('--out', required=True)
    ir = sub.add_parser('invite-revoke'); ir.add_argument('actor')
    grant = sub.add_parser('grant'); grant.add_argument('actor'); grant.add_argument('--project', default='team'); grant.add_argument('--role', choices=['reader', 'member', 'none'], required=True)
    disable = sub.add_parser('disable'); disable.add_argument('actor'); disable.add_argument('--enable', action='store_true')
    cr = sub.add_parser('credential-revoke'); cr.add_argument('device'); cr.add_argument('--actor')
    add = sub.add_parser('device-add'); add.add_argument('device'); add.add_argument('--out', required=True)
    args = p.parse_args()
    if args.command == 'protocol':
        print(json.dumps(protocol_guide(), ensure_ascii=False))
        return
    if args.command == 'enroll':
        print(json.dumps(enroll(args), ensure_ascii=False))
        return
    config = load(args.invite if args.command == 'connect' and args.invite else args.profile)
    if args.command == 'connect':
        raise SystemExit(tunnel(args, config))
    if args.command == 'gateway-service':
        print(json.dumps(gateway_service(args.profile, args.action)))
        return
    client = Client(config)
    c = args.command
    if c in ('manifest', 'projects', 'channels', 'actors', 'credentials'):
        result = client.call('GET', '/v1/' + c)
    elif c == 'agents':
        result = client.call('GET', '/v1/agents?project=' + urllib.parse.quote(args.project))
    elif c == 'project-create':
        result = client.call('POST', '/v1/projects', {'id': args.id, 'name': args.name or args.id})
    elif c == 'channel-create':
        result = client.call('POST', '/v1/channels', {'id': args.id, 'project': args.project, 'name': args.name or args.id, 'members': args.members})
    elif c == 'session':
        payload = {'id': args.id, 'agent': args.agent}
        if args.task is not None: payload['task'] = args.task
        result = client.call('POST', '/v1/sessions', payload)
    elif c == 'send':
        payload = {'channel': args.channel, 'session': args.session or 'human-' + config['device'][:60], 'client_id': args.id or secrets.token_hex(16), 'text': args.text if args.text is not None else pathlib.Path(args.file).read_text(), 'kind': args.kind, 'to_sessions': args.to, 'reply_to': args.reply_to, 'refs': json.loads(pathlib.Path(args.refs).read_text()) if args.refs else [], 'delivery': args.delivery, 'source': args.source}
        for field in ('agent', 'task'):
            if getattr(args, field) is not None: payload[field] = getattr(args, field)
        if not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}', payload['client_id']):
            raise SystemExit('Invalid request id')
        # Persist the exact request before transmission for safe retry/recovery.
        outbox = pathlib.Path(args.profile).expanduser().parent / 'outbox' / (payload['client_id'] + '.json')
        if outbox.exists():
            previous = json.loads(outbox.read_text())
            comparable = dict(payload)
            if 'source' not in previous['payload']:
                comparable.pop('source', None)  # retry a pre-0.2 outbox verbatim
            if previous['payload'] != comparable:
                raise SystemExit('Request id already has different content in the local outbox')
            payload = previous['payload']
        else:
            write_private(outbox, {'payload': payload, 'result': None})
        result = client.call('POST', '/v1/messages', payload)
        write_private(outbox, {'payload': payload, 'result': result}, replace=True)
        result['client_id'] = payload['client_id']
    elif c == 'query':
        payload = json.loads(pathlib.Path(args.request).read_text()) if args.request else {}
        if args.preset: payload['preset'] = args.preset
        if args.sql is not None: payload['sql'] = args.sql
        if args.sql_file: payload['sql'] = pathlib.Path(args.sql_file).read_text()
        if args.params is not None: payload['params'] = json.loads(args.params)
        for field in ('project', 'channel', 'session', 'after', 'before', 'limit'):
            if getattr(args, field) is not None: payload[field] = getattr(args, field)
        result = client.call('POST', '/v1/query', payload)
    elif c == 'history':
        query = {'channel': args.channel, 'limit': args.limit}
        if args.before is not None: query['before'] = args.before
        result = client.call('GET', '/v1/messages?' + urllib.parse.urlencode(query))
    elif c == 'read':
        query = {'after': args.after, 'wait': min(args.wait, 45)}
        if args.channel: query['channel'] = args.channel
        result = client.call('GET', '/v1/events?' + urllib.parse.urlencode(query))
    elif c == 'watch':
        cursor_file = pathlib.Path(args.cursor_file).expanduser()
        state = json.loads(cursor_file.read_text()) if cursor_file.exists() else {'cursor': 0}
        scope = {'url': config['url'], 'actor': config['actor'], 'channel': args.channel}
        if state.get('scope', scope) != scope:
            raise SystemExit('Cursor belongs to another identity, endpoint or channel')
        while True:
            query = {'after': state['cursor'], 'wait': 45}
            if args.channel: query['channel'] = args.channel
            try:
                result = client.call('GET', '/v1/events?' + urllib.parse.urlencode(query))
                for event in result['events']:
                    print(json.dumps(event, ensure_ascii=False), flush=True)
                state = {'cursor': result['cursor'], 'scope': scope}
                write_private(cursor_file, state, replace=True)
                if args.once:
                    return
            except (urllib.error.URLError, TimeoutError, ConnectionError) as err:
                print(json.dumps({'transport_error': str(err), 'retry_seconds': 3}), file=sys.stderr, flush=True)
                time.sleep(3)
    elif c == 'receipt':
        result = client.call('POST', '/v1/receipts', {'channel': args.channel, 'session': args.session, 'received': args.received, 'read': args.read})
    elif c == 'receipts':
        result = client.call('GET', '/v1/receipts?channel=' + urllib.parse.quote(args.channel))
    elif c == 'invite':
        result = client.call('POST', '/v1/invites', {'actor': args.actor, 'name': args.name or args.actor, 'grants': {args.project: args.role}, 'ttl_seconds': args.ttl})
        if config.get('ca_pem'): result['ca_pem'] = config['ca_pem']
        write_private(args.out, result)
        result = {'invitation_file': str(pathlib.Path(args.out).resolve()), 'actor': args.actor, 'expires': result['expires']}
    elif c == 'invite-revoke':
        result = client.call('POST', '/v1/invites/revoke', {'actor': args.actor})
    elif c == 'grant':
        result = client.call('POST', '/v1/grants', {'actor': args.actor, 'project': args.project, 'role': None if args.role == 'none' else args.role})
    elif c == 'disable':
        result = client.call('POST', '/v1/actors/disable', {'actor': args.actor, 'disabled': not args.enable})
    elif c == 'credential-revoke':
        payload = {'device': args.device}
        if args.actor: payload['actor'] = args.actor
        result = client.call('POST', '/v1/credentials/revoke', payload)
    elif c == 'device-add':
        new = {**config, 'device': args.device, 'token': secrets.token_urlsafe(32)}
        write_private(args.out, new)
        result = client.call('POST', '/v1/credentials', {'device': args.device, 'credential': new['token']})
        result['identity_file'] = str(pathlib.Path(args.out).resolve())
    else:
        raise SystemExit('Unhandled command')
    print(json.dumps(result, ensure_ascii=False))

if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        raise SystemExit(130)

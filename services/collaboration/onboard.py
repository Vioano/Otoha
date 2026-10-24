#!/usr/bin/env python3
"""Unpack a trusted captain invitation and enroll; no model/runtime dependency."""
import argparse
import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys
import time

from otoha import Client, enroll, gateway_service, load, write_private

def main():
    os.umask(0o077)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--bundle', required=True)
    p.add_argument('--directory', default=str(pathlib.Path.home() / '.local/share/otoha-collaboration'))
    p.add_argument('--device'); p.add_argument('--session', default='human')
    a = p.parse_args(); bundle = load(a.bundle)
    actor = bundle['actor']
    if not re.fullmatch(r'[a-z][a-z0-9_-]{0,39}', actor):
        raise SystemExit('Invalid member ID')
    directory = pathlib.Path(a.directory).expanduser().resolve() / actor
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    profile = directory / 'identity.json'
    previous = load(profile) if profile.exists() else None
    if previous and (previous['actor'] != actor or previous['url'] != bundle['url']):
        raise SystemExit('Existing identity belongs to a different member or endpoint')
    for filename, item in bundle.get('client_files', {}).items():
        if filename not in ('otoha.py', 'onboard.py', 'otoha_mcp.py', 'otoha_codex_bridge.py', '接入指南.md', '协作协议.md') or hashlib.sha256(item['text'].encode()).hexdigest() != item['sha256']:
            raise SystemExit('Client manifest invalid')
        target = directory / filename
        if target.exists() and target.read_text() != item['text']:
            raise SystemExit('Existing client source differs')
        target.write_text(item['text']); target.chmod(0o700 if filename.endswith('.py') else 0o600)
    gateway = bundle['gateway']
    key = directory / 'gateway-key'
    if key.exists():
        if key.stat().st_mode & 0o077 or key.read_text() != gateway['private_key']:
            raise SystemExit('Existing gateway key differs or has unsafe permissions')
    else:
        fd = os.open(key, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as f: f.write(gateway['private_key'])
    gateway.pop('private_key')
    known = directory / 'known_hosts'
    pin = gateway['host'] + ' ' + gateway['host_public_key'].strip() + '\n'
    if known.exists() and known.read_text() != pin:
        raise SystemExit('Existing pinned gateway host key differs')
    known.write_text(pin)
    gateway['key_file'] = str(key); gateway['known_hosts_file'] = str(known)
    invite = directory / 'invitation.json'
    if invite.exists() and load(invite) != bundle:
        raise SystemExit('A different invitation is already pending')
    if not invite.exists(): write_private(invite, bundle)
    proc = None
    already_connected = False
    if previous:
        try:
            already_connected = Client(previous).call('GET', '/v1/manifest')['actor']['id'] == actor
        except Exception:
            pass
    if not already_connected:
        with (directory / 'gateway.log').open('ab') as log:
            proc = subprocess.Popen([sys.executable, str(directory / 'otoha.py'), 'connect', '--invite', str(invite)], stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
        write_private(directory / 'gateway-process.json', {'pid': proc.pid}, replace=True)
        client = Client(bundle)
        for attempt in range(30):
            if proc.poll() is not None:
                raise SystemExit('SSH gateway failed; see private gateway.log')
            try:
                client.call('GET', '/health')
                break
            except Exception:
                time.sleep(.5)
        else:
            proc.terminate()
            raise SystemExit('Gateway health did not become ready')
    args = argparse.Namespace(invite=str(invite), profile=str(profile), device=a.device)
    result = enroll(args) if not previous else {'profile': str(profile), 'recovered': True}
    identity = load(profile)
    authenticated = Client(identity)
    manifest = authenticated.call('GET', '/v1/manifest')
    if manifest['actor']['id'] != actor:
        raise SystemExit('Authenticated identity does not match invitation')
    authenticated.call('POST', '/v1/sessions', {'id': a.session, 'agent': 'human'})
    # Replace only this bootstrap process with a native reconnecting service.
    if proc:
        proc.terminate()
        proc.wait(timeout=5)
    service = gateway_service(profile, 'install')
    for attempt in range(30):
        try:
            authenticated.call('GET', '/v1/manifest')
            break
        except Exception:
            time.sleep(.5)
    else:
        raise SystemExit('Enrollment saved; reconnecting service is not ready. Retry the same trusted bundle to recover')
    # Remove the no-longer-needed invitation code; the owner's delivered bundle
    # should also be deleted by the member after secure acceptance.
    invite.unlink()
    print(json.dumps({'actor': actor, 'grants': manifest['grants'], 'profile': result['profile'], 'gitea_url': identity['gitea']['url'], 'repository': identity['gitea']['repository'], 'gateway_service': service}))

if __name__ == '__main__':
    main()

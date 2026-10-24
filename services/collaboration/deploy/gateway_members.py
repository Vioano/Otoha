#!/usr/bin/env python3
"""Root-only public-key registry for the existing restricted otoha-access user."""
import argparse
import base64
import fcntl
import json
import os
import pathlib
import re
import subprocess
import tempfile

def main():
    p = argparse.ArgumentParser()
    p.add_argument('action', choices=['add', 'remove'])
    p.add_argument('actor'); p.add_argument('--public-key-base64')
    a = p.parse_args()
    if os.geteuid() != 0 or not re.fullmatch(r'[a-z][a-z0-9_-]{0,39}', a.actor):
        raise SystemExit('Root and a valid member ID are required')
    directory = pathlib.Path('/etc/otoha-collaboration')
    keyfile = directory / 'authorized_keys/otoha-access'
    registry = directory / 'members.json'
    with (directory / 'members.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        members = json.loads(registry.read_text()) if registry.exists() else {}
        key = members.get(a.actor)
        existing = keyfile.read_text().splitlines()
        if a.action == 'add':
            proposed = base64.b64decode(a.public_key_base64, validate=True).decode().strip()
            if '\n' in proposed or not proposed.startswith('ssh-ed25519 '):
                raise SystemExit('Invalid public key')
            with tempfile.NamedTemporaryFile(mode='w') as f:
                f.write(proposed); f.flush()
                subprocess.run(['ssh-keygen', '-lf', f.name], check=True, capture_output=True)
            if key and key != proposed:
                raise SystemExit('Member already has another key; explicitly revoke before replacement')
            members[a.actor] = proposed
            if proposed not in existing: existing.append(proposed)
        elif key:
            existing = [line for line in existing if line != key]
            del members[a.actor]
        # Existing administrator keys are preserved. Member data cannot edit SSH
        # policy, create users, choose forwarding destinations or execute commands.
        temp = keyfile.with_suffix('.tmp')
        temp.write_text('\n'.join(existing) + '\n'); temp.chmod(0o644)
        os.replace(temp, keyfile)
        temp = registry.with_suffix('.tmp')
        temp.write_text(json.dumps(members, indent=2)); temp.chmod(0o600)
        os.replace(temp, registry)
    print(json.dumps({'actor': a.actor, 'gateway_key': 'registered' if a.action == 'add' else 'revoked'}))

if __name__ == '__main__':
    main()

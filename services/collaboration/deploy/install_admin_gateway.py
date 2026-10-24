#!/usr/bin/env python3
"""Add a captain-only SSH jump channel to the existing private reverse tunnel."""
import argparse
import os
import pathlib
import subprocess

from install_gateway import CONFIG as ORIGINAL, public_key, run

ADMIN = '''Match User server-admin-access
    AuthorizedKeysFile /etc/otoha-collaboration/authorized_keys/%u
    AuthenticationMethods publickey
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowTcpForwarding local
    PermitOpen 127.0.0.1:13002
    PermitListen none
    GatewayPorts no
    AllowStreamLocalForwarding no
    AllowAgentForwarding no
    X11Forwarding no
    PermitTTY no
    PermitUserRC no
    MaxSessions 0
'''
UPDATED = ORIGINAL.replace('PermitListen 127.0.0.1:13000 127.0.0.1:13001',
                           'PermitListen 127.0.0.1:13000 127.0.0.1:13001 127.0.0.1:13002')
UPDATED = UPDATED.replace('Match all\n', ADMIN + 'Match all\n')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--captain-public-key', required=True)
    a = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit('Run as root')
    key = public_key(a.captain_public_key)
    config = pathlib.Path('/etc/ssh/sshd_config.d/80-otoha-collaboration.conf')
    prior = config.read_text()
    if prior not in (ORIGINAL, UPDATED):
        raise SystemExit('Unexpected existing gateway policy; refusing replacement')
    before = {u: run('sshd', '-T', '-C', f'user={u},host=localhost,addr=127.0.0.1')
              for u in ('ubuntu', 'otoha-access')}
    folder = pathlib.Path('/etc/otoha-collaboration/authorized_keys')
    path = folder / 'server-admin-access'
    if path.exists() and path.read_text().strip() != key:
        raise SystemExit('Existing captain key differs; refusing replacement')
    if subprocess.run(['getent', 'passwd', 'server-admin-access'], capture_output=True).returncode:
        run('useradd', '--system', '--no-create-home', '--shell', '/usr/sbin/nologin', 'server-admin-access')
    path.write_text(key + '\n')
    path.chmod(0o644)
    config.write_text(UPDATED)
    try:
        run('sshd', '-t')
        for user, effective in before.items():
            if effective != run('sshd', '-T', '-C', f'user={user},host=localhost,addr=127.0.0.1'):
                raise RuntimeError(f'Existing {user} policy changed')
        run('systemctl', 'reload', 'ssh')
    except BaseException:
        config.write_text(prior)
        raise
    print('Captain jump channel installed; ubuntu and teammate forwarding policies unchanged')


if __name__ == '__main__':
    main()

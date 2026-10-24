#!/usr/bin/env python3
"""Install isolated SSH forwarding identities; leave existing users/services intact."""
import argparse
import hashlib
import os
import pathlib
import shutil
import subprocess

CONFIG = '''# Otoha Collaboration: only the two new forwarding identities are affected.
Match User otoha-tunnel
    AuthorizedKeysFile /etc/otoha-collaboration/authorized_keys/%u
    AuthenticationMethods publickey
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowTcpForwarding remote
    PermitListen 127.0.0.1:13000 127.0.0.1:13001
    PermitOpen none
    GatewayPorts no
    AllowStreamLocalForwarding no
    AllowAgentForwarding no
    X11Forwarding no
    PermitTTY no
    PermitUserRC no
    MaxSessions 0
Match User otoha-access
    AuthorizedKeysFile /etc/otoha-collaboration/authorized_keys/%u
    AuthenticationMethods publickey
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowTcpForwarding local
    PermitOpen 127.0.0.1:13000 127.0.0.1:13001
    PermitListen none
    GatewayPorts no
    AllowStreamLocalForwarding no
    AllowAgentForwarding no
    X11Forwarding no
    PermitTTY no
    PermitUserRC no
    MaxSessions 0
Match all
'''

def run(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout

def public_key(path):
    value = pathlib.Path(path).read_text().strip()
    parts = value.split()
    if len(parts) < 2 or parts[0] != 'ssh-ed25519' or '\n' in value:
        raise SystemExit('Only a single Ed25519 public key is accepted')
    run('ssh-keygen', '-lf', str(path))
    return value

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--tunnel-public-key', required=True)
    p.add_argument('--access-public-key', required=True)
    a = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit('Run as root')
    tunnel, access = public_key(a.tunnel_public_key), public_key(a.access_public_key)
    before = run('sshd', '-T', '-C', 'user=ubuntu,host=localhost,addr=127.0.0.1')
    folder = pathlib.Path('/etc/otoha-collaboration/authorized_keys')
    folder.mkdir(parents=True, exist_ok=True, mode=0o755)
    for user, key in [('otoha-tunnel', tunnel), ('otoha-access', access)]:
        test = subprocess.run(['getent', 'passwd', user], capture_output=True)
        if test.returncode:
            run('useradd', '--system', '--no-create-home', '--shell', '/usr/sbin/nologin', user)
        path = folder / user
        if path.exists() and path.read_text().strip() != key:
            raise SystemExit(f'{path} already differs; refusing to replace registered keys')
        path.write_text(key + '\n'); path.chmod(0o644)
    config = pathlib.Path('/etc/ssh/sshd_config.d/80-otoha-collaboration.conf')
    if config.exists() and config.read_text() != CONFIG:
        raise SystemExit('Existing Otoha SSH configuration differs; refusing overwrite')
    config.write_text(CONFIG); config.chmod(0o644)
    try:
        run('sshd', '-t')
        after = run('sshd', '-T', '-C', 'user=ubuntu,host=localhost,addr=127.0.0.1')
        if before != after:
            raise RuntimeError('Existing ubuntu SSH effective configuration changed')
        run('systemctl', 'reload', 'ssh')
    except BaseException:
        config.unlink(missing_ok=True)
        raise
    print('Installed isolated otoha-tunnel/otoha-access SSH identities; ubuntu effective settings unchanged')

if __name__ == '__main__':
    main()

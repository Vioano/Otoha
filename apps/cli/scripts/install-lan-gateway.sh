#!/bin/sh
# Add an independent restricted LAN SSH gateway. Existing SSH policies and
# identities are untouched. Run as root on the private Linux laptop.
set -eu
test "$(id -u)" = 0
directory=/etc/otoha-cli/lan
install -d -m 700 "$directory"
if ! id otoha-fast >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/bin/nologin otoha-fast
  # OpenSSH rejects locked accounts before public-key authentication. This
  # invalid password hash unlocks the account; password auth is disabled below.
  usermod -p '*' otoha-fast
fi
test -f "$directory/host_key" || ssh-keygen -q -t ed25519 -N '' -f "$directory/host_key"
test -f "$directory/authorized_keys" || install -m 644 /dev/null "$directory/authorized_keys"
cat > "$directory/sshd_config" <<'EOF'
Port 53318
ListenAddress 0.0.0.0
AddressFamily inet
HostKey /etc/otoha-cli/lan/host_key
PidFile /run/otoha-cli-lan.pid
AuthorizedKeysFile /etc/otoha-cli/lan/authorized_keys
AllowUsers otoha-fast
AuthenticationMethods publickey
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
UsePAM no
StrictModes yes
AllowTcpForwarding local
AllowStreamLocalForwarding no
PermitOpen 127.0.0.1:8788 127.0.0.1:3000
AllowAgentForwarding no
X11Forwarding no
PermitTTY no
PermitTunnel no
PermitUserEnvironment no
GatewayPorts no
MaxSessions 0
MaxAuthTries 3
LoginGraceTime 10
LogLevel VERBOSE
EOF
/usr/bin/sshd -t -f "$directory/sshd_config"
cat > /etc/systemd/system/otoha-cli-lan.service <<'EOF'
[Unit]
Description=Otoha CLI restricted LAN forwarding
After=network.target
[Service]
Type=simple
ExecStart=/usr/bin/sshd -D -e -f /etc/otoha-cli/lan/sshd_config
Restart=always
RestartSec=2
[Install]
WantedBy=multi-user.target
EOF
install -d /usr/local/libexec/otoha-cli
cat > /usr/local/libexec/otoha-cli/add-key.py <<'EOF'
#!/usr/bin/env python3
import base64,fcntl,os,pathlib,re,sys
if os.geteuid()!=0 or len(sys.argv)!=3 or not re.fullmatch(r'[a-z][a-z0-9_-]{0,39}',sys.argv[1]):raise SystemExit('root and valid actor required')
key=base64.b64decode(sys.argv[2],validate=True).decode().strip()
if '\n' in key or not re.fullmatch(r'ssh-ed25519 [A-Za-z0-9+/=]+(?: .*)?',key):raise SystemExit('ed25519 public key required')
p=pathlib.Path('/etc/otoha-cli/lan/authorized_keys')
with p.with_suffix('.lock').open('a') as lock:
 fcntl.flock(lock,fcntl.LOCK_EX)
 lines=p.read_text().splitlines();blob=key.split()[1]
 if not any(blob==v.split()[1] for v in lines if len(v.split())>1):
  lines.append(key);tmp=p.with_suffix('.tmp');tmp.write_text('\n'.join(lines)+'\n');tmp.chmod(0o644);os.replace(tmp,p)
print('registered')
EOF
chmod 755 /usr/local/libexec/otoha-cli/add-key.py
# Public keys must be readable by the restricted account, parent traversal only.
chmod 755 /etc/otoha-cli "$directory"
systemctl daemon-reload
systemctl enable --now otoha-cli-lan.service
cat "$directory/host_key.pub"

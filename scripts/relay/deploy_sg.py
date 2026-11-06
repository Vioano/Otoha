#!/usr/bin/env python3
"""Install the pinned SG frps and add its routes to the existing HTTPS vhost."""

import argparse
import grp
import hashlib
import json
import os
from pathlib import Path
import platform
import pwd
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import time


VERSION = "0.72.0"
ARCHIVE_NAME = "frp_0.72.0_linux_amd64.tar.gz"
SOURCE_URL = "https://github.com/fatedier/frp/releases/download/v0.72.0/" + ARCHIVE_NAME
SHA256 = "daf719b32ca2778770bb92955e231a07414d754378b9af328aa2642862479804"
INSTALL = Path("/opt/otoha-relay/frp-0.72.0")
CONFIG = Path("/etc/otoha-relay/frps.toml")
UNIT = Path("/etc/systemd/system/otoha-relay-frps.service")
SNIPPET = Path("/etc/nginx/otoha-relay-locations.conf")
SITE = Path("/etc/nginx/sites-available/otoha-private")
SERVICE = "otoha-relay-frps.service"
USER = "otoha-relay-frps"

FRPS_CONFIG = '''# Managed by Otoha scripts/relay/deploy_sg.py.
bindAddr = "127.0.0.1"
bindPort = 28880
proxyBindAddr = "127.0.0.1"
vhostHTTPPort = 28881
transport.tls.force = false
auth.method = "token"
auth.token = ""
log.to = "console"
log.level = "warn"
log.disablePrintColor = true

[[httpPlugins]]
name = "otoha-route-auth"
addr = "127.0.0.1:28882"
path = "/frp"
ops = ["Login", "NewProxy"]
'''

NGINX_LOCATIONS = '''# Managed by Otoha scripts/relay/deploy_sg.py.
location = /~!frp {
    proxy_pass http://127.0.0.1:28880;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    proxy_buffering off;
}

location ^~ /t/ {
    if ($uri ~ "^/t/(r[0-9a-f]{24})$") {
        return 308 /t/$1/$is_args$args;
    }
    proxy_pass http://127.0.0.1:28881;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_read_timeout 75s;
}

location ^~ /relay/v1/ {
    proxy_pass http://127.0.0.1:28882;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_buffering off;
    proxy_read_timeout 75s;
}
'''


class DeploymentError(Exception):
    pass


def command(*args, check=True):
    result = subprocess.run(args, text=True, capture_output=True)
    if check and result.returncode:
        raise DeploymentError("{} failed: {}".format(" ".join(map(str, args)),
                              (result.stderr or result.stdout).strip()[-2000:]))
    return result


def checked_archive(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    if digest.hexdigest() != SHA256:
        raise DeploymentError("archive SHA-256 mismatch; expected " + SHA256)
    with tarfile.open(path, "r:gz") as archive:
        for name in ("frps", "LICENSE"):
            member = archive.getmember("frp_0.72.0_linux_amd64/" + name)
            if not member.isfile():
                raise DeploymentError("archive entry is not a regular file: " + name)


# Keep quoted strings, escaped characters and ${variables} intact while finding
# braces. This edits one server block, without reformatting its old directives.
TOKEN = re.compile(r'''\s+|\#[^\n]*|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\\.|[{};]|(?:\$\{[^}\s]+\}|[^\s{};#"'\\])+''')


def nginx_tokens(text):
    tokens, position = [], 0
    while position < len(text):
        match = TOKEN.match(text, position)
        if not match:
            raise DeploymentError("cannot parse nginx configuration at offset " + str(position))
        raw = match.group()
        if not raw.isspace() and not raw.startswith("#"):
            tokens.append((raw, match.start(), match.end()))
        position = match.end()
    return tokens


def add_https_include(text):
    tokens = nginx_tokens(text)
    pairs, stack = {}, []
    for index, (value, _, _) in enumerate(tokens):
        if value == "{":
            stack.append(index)
        elif value == "}":
            if not stack:
                raise DeploymentError("unbalanced nginx braces")
            pairs[stack.pop()] = index
    if stack:
        raise DeploymentError("unbalanced nginx braces")
    targets = []
    for index in range(len(tokens) - 1):
        if tokens[index][0] != "server" or tokens[index + 1][0] != "{":
            continue
        opening, closing = index + 1, pairs[index + 1]
        statements, current, cursor = [], [], opening + 1
        while cursor < closing:
            value = tokens[cursor][0]
            if value == "{":
                current = []
                cursor = pairs[cursor] + 1
                continue
            if value == ";":
                statements.append(current)
                current = []
            else:
                current.append(value[1:-1] if value[:1] in ("\"", "'") else value)
            cursor += 1
        if any(row and row[0] == "listen" and
               any(v == "443" or v.endswith(":443") for v in row[1:])
               for row in statements):
            targets.append((closing, statements))
    if len(targets) != 1:
        raise DeploymentError("expected exactly one server listening on 443; found " + str(len(targets)))
    closing, statements = targets[0]
    includes = sum(row == ["include", str(SNIPPET)] for row in statements)
    if includes > 1:
        raise DeploymentError("duplicate relay includes in HTTPS server")
    if includes:
        return text
    position = tokens[closing][1]
    line_start = text.rfind("\n", 0, position) + 1
    indent = text[line_start:position]
    if not indent.strip():
        position = line_start
        insertion = indent + "    include " + str(SNIPPET) + ";\n"
    else:
        insertion = "\n    include " + str(SNIPPET) + ";\n"
    return text[:position] + insertion + text[position:]


class FileChanges:
    """Snapshots only the files this deployment writes."""

    def __init__(self):
        self.files, self.directories = {}, []

    def directory(self, path):
        if not path.exists():
            self.directory(path.parent)
            path.mkdir(mode=0o755)
            self.directories.append(path)

    @staticmethod
    def replace(path, content, mode, uid, gid):
        # Atomic replacement also permits upgrading a currently running frps.
        with tempfile.NamedTemporaryFile(dir=path.parent, prefix=".otoha-relay-", delete=False) as stream:
            temporary = Path(stream.name)
            try:
                stream.write(content)
                stream.flush()
                os.chown(temporary, uid, gid)
                temporary.chmod(mode)
                os.replace(temporary, path)
            finally:
                temporary.unlink(missing_ok=True)

    def write(self, path, content, mode, gid=0, uid=0):
        if path.is_symlink():
            raise DeploymentError("refusing to replace a symlink: " + str(path))
        if path not in self.files:
            info = path.stat() if path.exists() else None
            self.files[path] = (path.read_bytes(), info) if info else None
        self.directory(path.parent)
        self.replace(path, content, mode, uid, gid)

    def restore(self):
        for path, previous in reversed(list(self.files.items())):
            if previous is None:
                path.unlink(missing_ok=True)
            else:
                data, info = previous
                self.replace(path, data, stat.S_IMODE(info.st_mode), info.st_uid, info.st_gid)
        for path in reversed(self.directories):
            try:
                path.rmdir()
            except OSError:
                pass  # Never remove files that were not created by this run.


def service_unit():
    return '''[Unit]
Description=Otoha relay frps
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=otoha-relay-frps
Group=otoha-relay-frps
ExecStart={binary} -c {config}
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6

[Install]
WantedBy=multi-user.target
'''.format(binary=INSTALL / "frps", config=CONFIG)


def apply(archive_path, site, candidate_site):
    for name in ("nginx", "systemctl", "useradd", "userdel"):
        if not shutil.which(name):
            raise DeploymentError("required command not found: " + name)
    was_active = command("systemctl", "is-active", "--quiet", SERVICE, check=False).returncode == 0
    was_enabled = command("systemctl", "is-enabled", "--quiet", SERVICE, check=False).returncode == 0
    changes, created_user, changed_services, nginx_attempted = FileChanges(), False, False, False
    try:
        with tempfile.TemporaryDirectory(prefix="otoha-relay-") as temporary:
            stage = Path(temporary)
            with tarfile.open(archive_path, "r:gz") as archive:
                for name in ("frps", "LICENSE"):
                    source = archive.extractfile("frp_0.72.0_linux_amd64/" + name)
                    if source is None:
                        raise DeploymentError("missing archive entry: " + name)
                    (stage / name).write_bytes(source.read())
            (stage / "frps").chmod(0o755)
            (stage / "frps.toml").write_text(FRPS_CONFIG)
            command(str(stage / "frps"), "verify", "-c", str(stage / "frps.toml"))
            try:
                account = pwd.getpwnam(USER)
            except KeyError:
                command("useradd", "--system", "--user-group", "--no-create-home",
                        "--home-dir", "/nonexistent", "--shell",
                        shutil.which("nologin") or "/usr/sbin/nologin", USER)
                created_user, account = True, pwd.getpwnam(USER)
            if account.pw_uid == 0 or not account.pw_shell.endswith(("nologin", "/false")):
                raise DeploymentError("existing relay user must be unprivileged and non-login")
            if grp.getgrnam(USER).gr_gid != account.pw_gid:
                raise DeploymentError("relay user must have its dedicated primary group")
            for name, mode in (("frps", 0o755), ("LICENSE", 0o644)):
                changes.write(INSTALL / name, (stage / name).read_bytes(), mode)
        changes.write(CONFIG, FRPS_CONFIG.encode(), 0o640, account.pw_gid)
        changes.write(UNIT, service_unit().encode(), 0o644)
        changes.write(SNIPPET, NGINX_LOCATIONS.encode(), 0o644)
        site_info = site.stat()
        changes.write(site, candidate_site.encode(), stat.S_IMODE(site_info.st_mode), site_info.st_gid, site_info.st_uid)
        command(str(INSTALL / "frps"), "verify", "-c", str(CONFIG))
        command("nginx", "-t")
        changed_services = True
        command("systemctl", "daemon-reload")
        command("systemctl", "enable", SERVICE)
        command("systemctl", "restart" if was_active else "start", SERVICE)
        time.sleep(1)
        command("systemctl", "is-active", "--quiet", SERVICE)
        nginx_attempted = True
        command("systemctl", "reload", "nginx")
    except Exception as original:
        rollback_errors = []
        def recover(action):
            try:
                action()
            except Exception as error:
                rollback_errors.append(str(error))
        if changed_services:
            if not was_enabled:
                recover(lambda: command("systemctl", "disable", SERVICE))
            recover(lambda: command("systemctl", "stop", SERVICE))
        recover(changes.restore)
        if changed_services:
            recover(lambda: command("systemctl", "daemon-reload"))
            if was_active:
                recover(lambda: command("systemctl", "start", SERVICE))
        if nginx_attempted:
            recover(lambda: command("nginx", "-t"))
        if created_user and not rollback_errors:
            recover(lambda: command("userdel", USER))
        suffix = "; rollback incomplete: " + "; ".join(rollback_errors) if rollback_errors else "; own files restored"
        if nginx_attempted:
            suffix += "; nginx runtime requires verification after attempted reload (not reloaded again)"
        raise DeploymentError(str(original) + suffix) from original


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--frp-archive", required=True, type=Path, help="official pinned linux_amd64 archive")
    parser.add_argument("--apply", action="store_true", help="apply on the SG host as root; otherwise dry-run")
    parser.add_argument("--nginx-site", type=Path, default=SITE,
                        help="alternate input for dry-run only; apply uses the fixed Otoha vhost")
    args = parser.parse_args()
    try:
        if args.apply and (os.geteuid() != 0 or platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64")):
            raise DeploymentError("--apply requires root on Linux amd64")
        if args.apply and args.nginx_site != SITE:
            raise DeploymentError("--apply only changes " + str(SITE))
        checked_archive(args.frp_archive)
        site = args.nginx_site.resolve(strict=True)
        candidate = add_https_include(site.read_text())
        if args.apply:
            apply(args.frp_archive, site, candidate)
        print(json.dumps({"mode": "applied" if args.apply else "dry-run", "version": VERSION,
                          "archive_sha256": SHA256, "source": SOURCE_URL,
                          "files": [str(INSTALL / "frps"), str(INSTALL / "LICENSE"), str(CONFIG),
                                    str(UNIT), str(SNIPPET), str(site)],
                          "https_include_changes": candidate != site.read_text() if not args.apply else None,
                          "bootstrap_managed": False}, indent=2))
        return 0
    except (DeploymentError, OSError, tarfile.TarError, KeyError) as error:
        print("Deployment failed: " + str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())

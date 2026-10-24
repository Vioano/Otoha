#!/usr/bin/env python3
"""SSH ProxyCommand: physical interface first, then the unchanged default route.

Only carries bytes. OpenSSH retains identity, host-key checking and forwarding
permissions. No DNS, global route, firewall, key or client configuration writes.
"""
import ipaddress
import json
import os
import socket
import subprocess
import sys
import threading


def main_interface():
    rows = json.loads(subprocess.check_output(
        ['ip', '-j', 'route', 'show', 'table', 'main', 'default'], timeout=1))
    rows.sort(key=lambda row: row.get('metric', 0))
    return next((row['dev'] for row in rows if row.get('dev')
                 and row['dev'] != 'lo' and not row['dev'].lower().startswith(
                     ('tun', 'wg', 'mihomo'))), None)


def connect(host, port):
    address = ipaddress.ip_address(host)
    family = socket.AF_INET6 if address.version == 6 else socket.AF_INET
    interface = None
    if not address.is_loopback:
        try:
            interface = main_interface()
        except (OSError, ValueError, subprocess.SubprocessError):
            pass
    modes = [interface, None] if interface else [None]
    for mode in modes:
        sock = socket.socket(family, socket.SOCK_STREAM)
        try:
            sock.settimeout(2)
            sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
            if mode:
                sock.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE,
                                mode.encode() + b'\0')
            sock.connect((host, port))
            # A successful TCP connect alone does not establish usable SSH.
            # Probe before sending any caller bytes so banner EOF can fall back.
            sock.settimeout(1)
            banner = bytearray()
            while b'\n' not in banner and len(banner) < 1024:
                block = sock.recv(1024 - len(banner))
                if not block:
                    raise ConnectionError('SSH banner EOF')
                banner.extend(block)
            if b'SSH-' not in banner:
                raise ConnectionError('SSH banner unavailable')
            sock.settimeout(None)
            print('SSH byte route: ' + ('physical ' + mode if mode else 'default'),
                  file=sys.stderr, flush=True)
            return sock, bytes(banner)
        except (OSError, ConnectionError):
            sock.close()
    raise ConnectionError('Both SSH connection routes failed')


def write_all(fd, data):
    view = memoryview(data)
    while view:
        written = os.write(fd, view)
        if not written:
            raise BrokenPipeError('Output closed')
        view = view[written:]


def run(host, port):
    if not 1 <= port <= 65535:
        raise ValueError('Invalid port')
    sock, banner = connect(host, port)
    write_all(1, banner)

    def upstream():
        try:
            while data := os.read(0, 65536):
                sock.sendall(data)
            sock.shutdown(socket.SHUT_WR)
        except OSError:
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass

    threading.Thread(target=upstream, daemon=True).start()
    try:
        while data := sock.recv(65536):
            write_all(1, data)
    finally:
        sock.close()


if __name__ == '__main__':
    try:
        if len(sys.argv) != 3:
            raise ValueError('Pass numeric SSH host and port')
        run(sys.argv[1], int(sys.argv[2]))
    except (OSError, ValueError, ConnectionError) as error:
        print('SSH byte route failed: ' + type(error).__name__, file=sys.stderr)
        raise SystemExit(1)

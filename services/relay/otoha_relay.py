#!/usr/bin/env python3
"""Small Otoha route registry and frps authorization plugin (stdlib only)."""

import argparse
import base64
import contextlib
import hashlib
import hmac
import http.server
import ipaddress
import json
import os
import pathlib
import re
import secrets
import sqlite3
import time
import urllib.parse


SCHEMA = 'otoha-relay/1'
NAME = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$')
ROUTE = re.compile(r'^r[0-9a-f]{24}$')
MAX_BODY = 8192
DATABASE_SCHEMA = '''
CREATE TABLE IF NOT EXISTS routes (
 id TEXT PRIMARY KEY,
 request_id TEXT NOT NULL UNIQUE,
 enrollment_hash TEXT NOT NULL UNIQUE,
 intent_hash TEXT NOT NULL,
 token_hash TEXT NOT NULL,
 label TEXT NOT NULL,
 descriptor TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 revoked_at INTEGER
);
CREATE INDEX IF NOT EXISTS route_expiration ON routes(expires_at, revoked_at);
'''


class Error(Exception):
    def __init__(self, status, code):
        self.status, self.code = status, code


def encoded(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'), allow_nan=False)


def digest(value):
    return hashlib.sha256(value.encode()).hexdigest()


def enrollment_key(value):
    if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9_-]{43}', value):
        raise Error(400, 'invalid_enrollment_secret')
    raw = base64.urlsafe_b64decode(value + '=')
    if len(raw) != 32 or base64.urlsafe_b64encode(raw).decode().rstrip('=') != value:
        raise Error(400, 'invalid_enrollment_secret')
    return raw


def route_token(secret, route_id):
    raw = hmac.new(enrollment_key(secret), b'otoha-relay-frp/1\0' + route_id.encode(), hashlib.sha256).digest()
    return base64.urlsafe_b64encode(raw).decode().rstrip('=')


def route_state(row, now):
    if row['revoked_at'] is not None:
        return 'revoked'
    return 'active' if row['expires_at'] > now else 'expired'


def private_file(path, value):
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(json.dumps(value, ensure_ascii=False, indent=2) + '\n')


def private_state(path):
    path = pathlib.Path(path).resolve()
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    if path.is_symlink() or (os.name != 'nt' and path.stat().st_mode & 0o077):
        raise ValueError('relay state must be a private directory (0700)')
    return path


def validate_config(config):
    url = urllib.parse.urlsplit(config['public_url'])
    if url.scheme != 'https' or not url.hostname or url.username or url.password or url.path not in ('', '/') or url.query or url.fragment:
        raise ValueError('public_url must be an HTTPS origin')
    if config['server_port'] != 443 or config['protocol'] != 'wss':
        raise ValueError('the initial relay supports WSS on port 443')
    for key in ('server_addr', 'server_name'):
        if not isinstance(config[key], str) or not config[key] or any(x in config[key] for x in '/?#@\r\n\t '):
            raise ValueError('invalid ' + key)
    for key, low, high in [('max_active_routes', 1, 10000), ('max_retained_routes', 1, 100000), ('route_ttl_seconds', 60, 30 * 86400)]:
        if type(config[key]) is not int or not low <= config[key] <= high:
            raise ValueError('invalid ' + key)
    if config['max_retained_routes'] < config['max_active_routes']:
        raise ValueError('retained route capacity must cover active capacity')
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', config['frp_version']):
        raise ValueError('frp_version must be a pinned version')
    config['public_url'] = config['public_url'].rstrip('/')
    return config


class Store:
    def __init__(self, state, clock=time.time):
        self.state = private_state(state)
        config_path = self.state / 'relay.json'
        if os.name != 'nt' and config_path.stat().st_mode & 0o077:
            raise ValueError('relay.json must be private (0600)')
        self.config = validate_config(json.loads(config_path.read_text()))
        self.clock = clock
        with self.database() as db:
            db.execute('PRAGMA journal_mode=WAL')
            db.executescript(DATABASE_SCHEMA)

    @contextlib.contextmanager
    def database(self):
        db = sqlite3.connect(self.state / 'routes.sqlite3', timeout=5)
        db.row_factory = sqlite3.Row
        db.execute('PRAGMA busy_timeout=5000')
        try:
            with db:
                yield db
        finally:
            db.close()

    def manifest(self):
        config = self.config
        return {'schema': SCHEMA, 'default_endpoint': config['public_url'],
                'registration_url': config['public_url'] + '/relay/v1/routes',
                'server_addr': config['server_addr'], 'server_port': config['server_port'],
                'server_name': config['server_name'], 'protocol': config['protocol'],
                'frp_version': config['frp_version'],
                'limits': {'request_bytes': MAX_BODY, 'active_routes': config['max_active_routes'],
                           'retained_routes': config['max_retained_routes'], 'route_ttl_seconds': config['route_ttl_seconds']},
                'capabilities': ['idempotent_registration', 'route_status', 'renew', 'revoke'],
                'revoke_scope': 'prevents subsequent frp authorization; established connections are not forcibly closed'}

    def descriptor(self, route_id):
        config = self.config
        return {'id': route_id, 'public_url': config['public_url'] + '/t/' + route_id,
                'server_addr': config['server_addr'], 'server_port': config['server_port'],
                'server_name': config['server_name'], 'protocol': config['protocol'],
                'proxy_name': 'otoha-center', 'custom_domain': urllib.parse.urlsplit(config['public_url']).hostname,
                'location': '/t/' + route_id + '/', 'frp_version': config['frp_version']}

    def response(self, row, now, token=None, recovered=None):
        route = json.loads(row['descriptor'])
        route.update({'created_at': row['created_at'], 'expires_at': row['expires_at'], 'state': route_state(row, now)})
        if token is not None:
            route['token'] = token
        result = {'schema': SCHEMA, 'route': route}
        if recovered is not None:
            result['recovered'] = recovered
        return result

    def capacity(self, db, now):
        active = db.execute('SELECT count(*) FROM routes WHERE revoked_at IS NULL AND expires_at>?', (now,)).fetchone()[0]
        if active >= self.config['max_active_routes']:
            raise Error(429, 'relay_capacity_reached')

    def register(self, body):
        if not isinstance(body, dict) or set(body) != {'request_id', 'enrollment_secret', 'label'}:
            raise Error(400, 'invalid_registration_fields')
        request_id, secret, label = body['request_id'], body['enrollment_secret'], body['label']
        if not isinstance(request_id, str) or not NAME.fullmatch(request_id):
            raise Error(400, 'invalid_request_id')
        enrollment_key(secret)
        if not isinstance(label, str):
            raise Error(400, 'invalid_label')
        label = label.strip()
        try:
            label_bytes = label.encode()
        except UnicodeError as error:
            raise Error(400, 'invalid_label') from error
        if not 1 <= len(label) <= 120 or len(label_bytes) > 256 or any(ord(c) < 32 for c in label):
            raise Error(400, 'invalid_label')
        secret_hash, intent_hash = digest(secret), digest(encoded({'label': label}))
        now = int(self.clock())
        with self.database() as db:
            db.execute('BEGIN IMMEDIATE')
            previous = db.execute('SELECT * FROM routes WHERE request_id=? OR enrollment_hash=?', (request_id, secret_hash)).fetchone()
            if previous:
                if previous['request_id'] != request_id or not hmac.compare_digest(previous['enrollment_hash'], secret_hash) or previous['intent_hash'] != intent_hash:
                    raise Error(409, 'request_conflict')
                return self.response(previous, now, route_token(secret, previous['id']), recovered=True)
            self.capacity(db, now)
            retained = db.execute('SELECT count(*) FROM routes').fetchone()[0]
            if retained >= self.config['max_retained_routes']:
                raise Error(429, 'relay_registration_limit_reached')
            route_id = 'r' + secrets.token_hex(12)
            token = route_token(secret, route_id)
            db.execute('INSERT INTO routes VALUES(?,?,?,?,?,?,?,?,?,NULL)',
                       (route_id, request_id, secret_hash, intent_hash, digest(token), label,
                        encoded(self.descriptor(route_id)), now, now + self.config['route_ttl_seconds']))
            row = db.execute('SELECT * FROM routes WHERE id=?', (route_id,)).fetchone()
            return self.response(row, now, token, recovered=False)

    def authenticated(self, db, route_id, token, active=False):
        if not isinstance(route_id, str) or not ROUTE.fullmatch(route_id):
            raise Error(404, 'route_not_found')
        row = db.execute('SELECT * FROM routes WHERE id=?', (route_id,)).fetchone()
        if not row:
            raise Error(404, 'route_not_found')
        if not isinstance(token, str) or not re.fullmatch(r'[A-Za-z0-9_-]{43}', token) or not hmac.compare_digest(row['token_hash'], digest(token)):
            raise Error(401, 'route_unauthorized')
        if active:
            state = route_state(row, int(self.clock()))
            if state != 'active':
                raise Error(403, 'route_' + state)
        return row

    def manage(self, route_id, token, action='status'):
        now = int(self.clock())
        with self.database() as db:
            if action != 'status':
                db.execute('BEGIN IMMEDIATE')
            row = self.authenticated(db, route_id, token)
            if action == 'renew':
                if row['revoked_at'] is not None:
                    raise Error(403, 'route_revoked')
                if row['expires_at'] <= now:
                    self.capacity(db, now)
                db.execute('UPDATE routes SET expires_at=? WHERE id=?', (now + self.config['route_ttl_seconds'], route_id))
            elif action == 'revoke':
                db.execute('UPDATE routes SET revoked_at=coalesce(revoked_at,?) WHERE id=?', (now, route_id))
            elif action != 'status':
                raise Error(404, 'not_found')
            row = db.execute('SELECT * FROM routes WHERE id=?', (route_id,)).fetchone()
            return self.response(row, now)

    def plugin(self, operation, body):
        try:
            if operation not in ('Login', 'NewProxy') or not isinstance(body, dict) or not isinstance(body.get('content'), dict):
                raise Error(400, 'invalid_plugin_request')
            content = body['content']
            user = content if operation == 'Login' else content.get('user')
            if not isinstance(user, dict) or not isinstance(user.get('metas'), dict):
                raise Error(403, 'route_unauthorized')
            route_id, token = user.get('user'), user['metas'].get('otoha_token')
            with self.database() as db:
                row = self.authenticated(db, route_id, token, active=True)
                descriptor = json.loads(row['descriptor'])
            if operation == 'NewProxy':
                if content.get('proxy_name') != route_id + '.' + descriptor['proxy_name']:
                    raise Error(403, 'proxy_name_forbidden')
                if content.get('proxy_type') != 'http' or content.get('custom_domains') != [descriptor['custom_domain']] or content.get('locations') != [descriptor['location']]:
                    raise Error(403, 'proxy_scope_forbidden')
                if any(content.get(key) for key in ('remote_port', 'subdomain', 'group', 'group_key', 'sk', 'multiplexer')):
                    raise Error(403, 'proxy_scope_forbidden')
            return {'reject': False, 'unchange': True}
        except Error as error:
            return {'reject': True, 'reject_reason': error.code}


class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address, store):
        super().__init__(address, Handler)
        self.store = store


class Handler(http.server.BaseHTTPRequestHandler):
    def setup(self):
        super().setup()
        self.connection.settimeout(10)

    def log_message(self, *_args):
        # Request bodies and headers include recovery/route capabilities.
        pass

    def send_json(self, status, body):
        raw = encoded(body).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(raw)))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.end_headers()
        self.wfile.write(raw)

    def body(self):
        if self.headers.get('Transfer-Encoding'):
            raise Error(400, 'content_length_required')
        length = self.headers.get('Content-Length', '')
        if not length.isdigit():
            raise Error(411, 'content_length_required')
        length = int(length)
        if length > MAX_BODY:
            raise Error(413, 'request_too_large')
        if self.headers.get_content_type() != 'application/json':
            raise Error(415, 'json_required')
        raw = self.rfile.read(length)
        if len(raw) != length:
            raise Error(400, 'invalid_json')
        try:
            def invalid_constant(_value):
                raise ValueError('non-finite JSON')
            body = json.loads(raw, parse_constant=invalid_constant)
            if not isinstance(body, dict):
                raise ValueError('object required')
            return body
        except (ValueError, UnicodeError, RecursionError) as error:
            raise Error(400, 'invalid_json') from error

    def dispatch(self, method):
        try:
            url = urllib.parse.urlsplit(self.path)
            path, query = url.path, urllib.parse.parse_qs(url.query)
            store = self.server.store
            if method == 'GET' and path == '/health':
                with store.database() as db:
                    db.execute('SELECT 1 FROM routes LIMIT 1').fetchone()
                return self.send_json(200, {'status': 'ok', 'schema': SCHEMA})
            if method == 'GET' and path == '/relay/v1/manifest' and not query:
                return self.send_json(200, store.manifest())
            if method == 'POST' and path == '/frp':
                if not ipaddress.ip_address(self.client_address[0]).is_loopback:
                    raise Error(403, 'plugin_loopback_only')
                body = self.body()
                operation = query.get('op', [body.get('op')])[0]
                version = query.get('version', [body.get('version', '0.1.0')])[0]
                if version != '0.1.0' or ('op' in body and body['op'] != operation):
                    return self.send_json(200, {'reject': True, 'reject_reason': 'invalid_plugin_request'})
                return self.send_json(200, store.plugin(operation, body))
            if query:
                raise Error(400, 'unexpected_query')
            if method == 'POST' and path == '/relay/v1/routes':
                return self.send_json(200, store.register(self.body()))
            match = re.fullmatch(r'/relay/v1/routes/(r[0-9a-f]{24})(?:/(renew|revoke))?', path)
            if match:
                route_id, action = match.groups()
                if method == 'GET' and action is None or method == 'POST' and action in ('renew', 'revoke'):
                    authorization = self.headers.get('Authorization', '')
                    token = authorization[7:] if authorization.startswith('Bearer ') else ''
                    if method == 'POST':
                        if self.headers.get('Transfer-Encoding'):
                            raise Error(400, 'content_length_required')
                        if self.headers.get('Content-Length', '0') != '0' and self.body() != {}:
                            raise Error(400, 'unexpected_management_fields')
                    return self.send_json(200, store.manage(route_id, token, action or 'status'))
            raise Error(404, 'not_found')
        except Error as error:
            self.send_json(error.status, {'error': error.code})
        except (TimeoutError, ConnectionError, BrokenPipeError):
            self.close_connection = True
        except sqlite3.OperationalError:
            self.send_json(503, {'error': 'relay_storage_unavailable'})

    def do_GET(self):
        self.dispatch('GET')

    def do_POST(self):
        self.dispatch('POST')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    init = sub.add_parser('init')
    init.add_argument('--state', required=True)
    init.add_argument('--public-url', default='https://otoha.kessokuu.top')
    init.add_argument('--server-addr', default='otoha.kessokuu.top')
    init.add_argument('--server-name', default='otoha.kessokuu.top')
    init.add_argument('--frp-version', default='0.72.0')
    init.add_argument('--max-active-routes', type=int, default=64)
    init.add_argument('--max-retained-routes', type=int, default=4096)
    init.add_argument('--route-ttl-seconds', type=int, default=7 * 86400)
    serve = sub.add_parser('serve')
    serve.add_argument('--state', required=True)
    serve.add_argument('--host', default='127.0.0.1')
    serve.add_argument('--port', type=int, default=28882)
    args = parser.parse_args(argv)
    if args.command == 'init':
        state = private_state(args.state)
        config = validate_config({'public_url': args.public_url, 'server_addr': args.server_addr,
                                  'server_port': 443, 'server_name': args.server_name, 'protocol': 'wss',
                                  'frp_version': args.frp_version, 'max_active_routes': args.max_active_routes,
                                  'max_retained_routes': args.max_retained_routes, 'route_ttl_seconds': args.route_ttl_seconds})
        private_file(state / 'relay.json', config)
        store = Store(state)
        print(encoded({'initialized': True, 'state': str(state), 'schema': SCHEMA, 'manifest': store.manifest()}))
        return
    if not ipaddress.ip_address(args.host).is_loopback:
        parser.error('serve binds only to a loopback IP; expose /relay/v1/ through the existing HTTPS edge')
    server = Server((args.host, args.port), Store(args.state))
    print(encoded({'listening': args.host, 'port': server.server_port, 'schema': SCHEMA}), flush=True)
    try:
        server.serve_forever()
    finally:
        server.server_close()


if __name__ == '__main__':
    main()

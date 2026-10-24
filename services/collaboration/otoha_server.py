#!/usr/bin/env python3
"""Otoha Collaboration: a private, repository-independent message service (stdlib)."""
import argparse
import hashlib
import http.server
import json
import os
import pathlib
import re
import secrets
import shutil
import socket
import sqlite3
import ssl
import threading
import time
import urllib.parse

from otoha_message import normalize
from otoha_projection import MAX_SNAPSHOT_BYTES, PRESETS, ProjectionError, query as project_query

VERSION = '0.2.0'
NAME = re.compile(r'^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$')
ROLES = {'reader', 'member', 'admin'}
SCHEMA = '''
CREATE TABLE IF NOT EXISTS actors(id TEXT PRIMARY KEY, name TEXT NOT NULL, owner INTEGER NOT NULL DEFAULT 0, disabled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS credentials(hash TEXT PRIMARY KEY, actor TEXT NOT NULL REFERENCES actors(id), device TEXT NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, created REAL NOT NULL);
CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS grants(actor TEXT REFERENCES actors(id), project TEXT REFERENCES projects(id), role TEXT NOT NULL, PRIMARY KEY(actor,project));
CREATE TABLE IF NOT EXISTS invites(hash TEXT PRIMARY KEY, actor TEXT NOT NULL, name TEXT NOT NULL, grants TEXT NOT NULL, expires REAL NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, claimed TEXT, credential_hash TEXT);
CREATE TABLE IF NOT EXISTS channels(id TEXT PRIMARY KEY, project TEXT NOT NULL REFERENCES projects(id), name TEXT NOT NULL, members TEXT);
CREATE TABLE IF NOT EXISTS sessions(id TEXT NOT NULL, actor TEXT NOT NULL REFERENCES actors(id), agent TEXT NOT NULL, device TEXT NOT NULL, updated REAL NOT NULL, PRIMARY KEY(actor,id));
CREATE TABLE IF NOT EXISTS session_context(actor TEXT NOT NULL, session TEXT NOT NULL, task TEXT NOT NULL, PRIMARY KEY(actor,session), FOREIGN KEY(actor,session) REFERENCES sessions(actor,id));
CREATE TABLE IF NOT EXISTS messages(id INTEGER PRIMARY KEY AUTOINCREMENT, channel TEXT NOT NULL REFERENCES channels(id), actor TEXT NOT NULL REFERENCES actors(id), session TEXT NOT NULL, client_id TEXT NOT NULL, payload TEXT NOT NULL, created REAL NOT NULL, UNIQUE(actor,client_id));
CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, project TEXT, channel TEXT, payload TEXT NOT NULL, created REAL NOT NULL);
CREATE TABLE IF NOT EXISTS receipts(actor TEXT REFERENCES actors(id), session TEXT, channel TEXT REFERENCES channels(id), received INTEGER NOT NULL DEFAULT 0, read INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(actor,session,channel));
CREATE INDEX IF NOT EXISTS message_channel ON messages(channel,id);
CREATE INDEX IF NOT EXISTS event_channel ON events(channel,id);
'''

class Error(Exception):
    def __init__(self, status, code):
        self.status, self.code = status, code

def digest(value):
    return hashlib.sha256(value.encode()).hexdigest()

def name(value):
    if not isinstance(value, str) or not NAME.fullmatch(value):
        raise Error(400, 'invalid_identifier')
    return value

def encoded(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))

def private_write(path, value):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as f:
        f.write(json.dumps(value, ensure_ascii=False, indent=2) + '\n')

class Store:
    def __init__(self, state):
        self.state = pathlib.Path(state).resolve()
        self.config = json.loads((self.state / 'server.json').read_text())
        self.lock = threading.RLock()
        self.changed = threading.Condition()
        self.guard()
        self.db = sqlite3.connect(self.state / 'messages.sqlite3', check_same_thread=False)
        self.db.row_factory = sqlite3.Row
        self.db.execute('PRAGMA foreign_keys=ON')
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('PRAGMA busy_timeout=5000')
        self.db.execute('PRAGMA max_page_count=131072')  # 512 MiB at default page size
        self.db.executescript(SCHEMA)
        # Additive migration: old named message INSERTs and five-column session
        # INSERTs remain valid. Never invent provenance for existing messages.
        if 'origin' not in {r['name'] for r in self.db.execute('PRAGMA table_info(messages)')}:
            self.db.execute('ALTER TABLE messages ADD COLUMN origin TEXT')
            self.db.commit()

    def guard(self):
        mount = self.config.get('mount')
        if mount:
            root = pathlib.Path(mount)
            if not os.path.ismount(root) or self.state.stat().st_dev != root.stat().st_dev:
                raise Error(503, 'external_volume_unavailable')
            if (root / '.otoha-collaboration-volume-id').read_text().strip() != self.config['volume_id']:
                raise Error(503, 'external_volume_changed')
        if shutil.disk_usage(self.state).free < self.config.get('min_free_bytes', 2 * 1024**3):
            raise Error(507, 'storage_reserve_reached')

    def row(self, sql, args=()):
        return self.db.execute(sql, args).fetchone()

    def rows(self, sql, args=()):
        return [dict(x) for x in self.db.execute(sql, args)]

    def authenticate(self, token):
        self.guard()
        actor = self.row('SELECT a.*, c.device, c.hash credential_hash FROM actors a JOIN credentials c ON a.id=c.actor WHERE c.hash=? AND c.revoked=0 AND a.disabled=0', (digest(token),))
        if not actor:
            raise Error(401, 'unauthorized')
        return dict(actor)

    def owner(self, actor):
        if not actor['owner']:
            raise Error(403, 'owner_required')

    def role(self, actor, project):
        if actor['owner']:
            return 'owner'
        r = self.row('SELECT role FROM grants WHERE actor=? AND project=?', (actor['id'], project))
        return r['role'] if r else None

    def channel(self, actor, channel, write=False):
        r = self.row('SELECT * FROM channels WHERE id=?', (channel,))
        if not r:
            raise Error(404, 'channel_not_found')
        r = dict(r)
        role = self.role(actor, r['project'])
        members = json.loads(r['members']) if r['members'] else None
        if not role or (members is not None and actor['id'] not in members and not actor['owner']):
            raise Error(403, 'channel_forbidden')
        if write and role == 'reader':
            raise Error(403, 'read_only')
        return r

    def emit(self, kind, payload, project=None, channel=None):
        cur = self.db.execute('INSERT INTO events(kind,project,channel,payload,created) VALUES(?,?,?,?,?)', (kind, project, channel, encoded(payload), time.time()))
        return cur.lastrowid

    def event_visible(self, actor, event):
        if event['channel']:
            try:
                self.channel(actor, event['channel'])
                return True
            except Error:
                return False
        if event['project']:
            return bool(self.role(actor, event['project']))
        return bool(actor['owner'])

    def events(self, actor, after, channel=None, limit=100):
        if after < 0:
            raise Error(400, 'invalid_cursor')
        if channel:
            self.channel(actor, channel)
        high = self.row('SELECT COALESCE(MAX(id),0) n FROM events')['n']
        if after > high:
            raise Error(409, 'cursor_ahead_of_server')
        found, cursor = [], after
        for e in self.rows('SELECT * FROM events WHERE id>? ORDER BY id LIMIT 1000', (after,)):
            cursor = e['id']
            if (not channel or e['channel'] == channel) and self.event_visible(actor, e):
                e['payload'] = json.loads(e['payload'])
                found.append(e)
                if len(found) >= limit:
                    break
        return {'events': found, 'cursor': cursor, 'has_more': cursor < high}

    def message(self, row):
        result = dict(row)
        result['payload'] = json.loads(result['payload'])
        result['origin'] = json.loads(result['origin']) if result.get('origin') else {
            'recorded': False, 'actor': result['actor'], 'session': result['session']}
        return result

    def projection(self, actor, request):
        project = name(request.get('project', 'team'))
        if not self.role(actor, project):
            raise Error(403, 'project_forbidden')
        selected = request.get('channel')
        if selected is not None and self.channel(actor, name(selected))['project'] != project:
            raise Error(400, 'channel_project_mismatch')
        after, before = request.get('after', 0), request.get('before', 2**63 - 1)
        if type(after) is not int or type(before) is not int or not 0 <= after < before <= 2**63 - 1:
            raise Error(400, 'invalid_query_range')
        snapshot = {t: [] for t in ('projects', 'channels', 'messages', 'sessions', 'receipts')}
        size, count = 0, 0
        def add(table, row):
            nonlocal size, count
            row = dict(row)
            size += len(encoded(row).encode())
            count += 1
            if size > MAX_SNAPSHOT_BYTES or count > 50000:
                raise Error(400, 'snapshot_too_large_narrow_scope')
            snapshot[table].append(row)
        for row in self.db.execute('SELECT * FROM projects WHERE id=?', (project,)):
            add('projects', row)
        for row in self.db.execute('SELECT * FROM channels WHERE project=? ORDER BY id', (project,)):
            if selected is not None and row['id'] != selected:
                continue
            try:
                self.channel(actor, row['id'])
            except Error:
                continue
            add('channels', row)
            for m in self.db.execute('SELECT * FROM messages WHERE channel=? AND id>? AND id<? ORDER BY id', (row['id'], after, before)):
                message = self.message(m)
                payload = message['payload']
                add('messages', {**dict(m), 'text': payload['text'], 'kind': payload['kind'], 'reply_to': payload['reply_to'], 'origin': encoded(message['origin'])})
            for receipt in self.db.execute('SELECT * FROM receipts WHERE channel=?', (row['id'],)):
                add('receipts', receipt)
        # The same public session descriptors as /agents; tasks are voluntarily
        # shared declarations, never private message contents or credentials.
        for row in self.db.execute('SELECT s.*,coalesce(c.task,\'\') task FROM sessions s JOIN actors a ON a.id=s.actor LEFT JOIN session_context c ON c.actor=s.actor AND c.session=s.id WHERE a.disabled=0 AND (a.owner=1 OR EXISTS(SELECT 1 FROM grants g WHERE g.actor=a.id AND g.project=?))', (project,)):
            add('sessions', row)
        target = actor['id'] + '/' + str(request.get('session', ''))
        try:
            result = project_query(snapshot, request, target)
        except ProjectionError as error:
            raise Error(400, str(error)) from error
        result['scope'] = {'project': project, 'channel': selected, 'after': after, 'before': before,
                           'message_high_water': max((m['id'] for m in snapshot['messages']), default=0),
                           'authorization': 'captain' if actor['owner'] else 'member',
                           'input_complete': True, 'snapshot_messages': len(snapshot['messages'])}
        return result

    def handle(self, method, path, query, body, token):
        with self.lock, self.db:
            if method == 'POST' and path == '/v1/enroll':
                self.guard()
                code = body.get('invitation', '')
                credential = body.get('credential', '')
                enrollment = name(body.get('enrollment_id'))
                device = name(body.get('device'))
                if not isinstance(credential, str) or len(credential) < 40 or len(credential) > 200:
                    raise Error(400, 'invalid_credential')
                invite = self.row('SELECT * FROM invites WHERE hash=?', (digest(code),))
                if not invite or invite['revoked'] or invite['expires'] < time.time():
                    raise Error(403, 'invitation_invalid_or_expired')
                h = digest(credential)
                if invite['claimed']:
                    if invite['claimed'] != enrollment or invite['credential_hash'] != h:
                        raise Error(409, 'invitation_already_used')
                    self.authenticate(credential)
                    return {'actor': invite['actor'], 'device': device, 'recovered': True}
                if self.row('SELECT 1 FROM actors WHERE id=?', (invite['actor'],)):
                    raise Error(409, 'actor_already_exists')
                self.db.execute('INSERT INTO actors(id,name) VALUES(?,?)', (invite['actor'], invite['name']))
                self.db.execute('INSERT INTO credentials(hash,actor,device,created) VALUES(?,?,?,?)', (h, invite['actor'], device, time.time()))
                for project, role in json.loads(invite['grants']).items():
                    self.db.execute('INSERT INTO grants VALUES(?,?,?)', (invite['actor'], project, role))
                self.db.execute('UPDATE invites SET claimed=?,credential_hash=? WHERE hash=?', (enrollment, h, digest(code)))
                self.emit('actor.enrolled', {'actor': invite['actor'], 'device': device})
                return {'actor': invite['actor'], 'device': device, 'recovered': False}
            actor = self.authenticate(token)
            aid = actor['id']
            if method == 'GET' and path == '/v1/manifest':
                return {'protocol': 'otoha-collaboration/1', 'version': VERSION, 'actor': {k: actor[k] for k in ('id', 'name', 'owner', 'device')}, 'grants': self.rows('SELECT project,role FROM grants WHERE actor=?', (aid,)), 'capabilities': ['channels', 'messages', 'sessions', 'history', 'events', 'sse', 'receipts', 'invitations', 'git_refs', 'projections', 'message_origin'], 'read_presets': list(PRESETS), 'projection_tables': ['projects','channels','messages','sessions','receipts'], 'repository_required': False, 'max_message_bytes': 65536}
            if method == 'POST' and path == '/v1/query':
                return self.projection(actor, body)
            if path == '/v1/projects':
                if method == 'GET':
                    return {'projects': self.rows('SELECT * FROM projects') if actor['owner'] else self.rows('SELECT p.* FROM projects p JOIN grants g ON p.id=g.project WHERE g.actor=?', (aid,))}
                self.owner(actor)
                project = name(body.get('id'))
                self.db.execute('INSERT INTO projects VALUES(?,?)', (project, str(body.get('name', project))[:200]))
                self.emit('project.created', {'id': project}, project)
                return {'id': project}
            if path == '/v1/channels':
                if method == 'GET':
                    result = []
                    for c in self.rows('SELECT * FROM channels'):
                        try:
                            self.channel(actor, c['id'])
                            c['members'] = json.loads(c['members']) if c['members'] else None
                            result.append(c)
                        except Error:
                            pass
                    return {'channels': result}
                project = name(body.get('project'))
                if self.role(actor, project) not in ('owner', 'admin', 'member'):
                    raise Error(403, 'project_write_required')
                members = body.get('members')
                if members is not None:
                    if not isinstance(members, list) or not members or len(members) > 100:
                        raise Error(400, 'invalid_members')
                    members = sorted(set([aid] + [name(x) for x in members]))
                    if any(not self.row('SELECT 1 FROM actors WHERE id=? AND disabled=0', (m,)) or not self.role({'id': m, 'owner': bool(self.row('SELECT owner FROM actors WHERE id=?', (m,))['owner'])}, project) for m in members):
                        raise Error(403, 'member_not_in_project')
                channel = name(body.get('id'))
                self.db.execute('INSERT INTO channels VALUES(?,?,?,?)', (channel, project, str(body.get('name', channel))[:200], encoded(members) if members else None))
                self.emit('channel.created', {'id': channel}, project, channel)
                return {'id': channel}
            if method == 'POST' and path == '/v1/invites':
                self.owner(actor)
                invited = name(body.get('actor'))
                if self.row('SELECT 1 FROM actors WHERE id=?', (invited,)):
                    raise Error(409, 'actor_already_exists')
                grants = body.get('grants', {})
                if not isinstance(grants, dict) or not grants:
                    raise Error(400, 'grants_required')
                for project, role in grants.items():
                    if role not in ROLES or not self.row('SELECT 1 FROM projects WHERE id=?', (project,)):
                        raise Error(400, 'invalid_grant')
                code = secrets.token_urlsafe(32)
                expires = time.time() + min(max(int(body.get('ttl_seconds', 86400)), 60), 7 * 86400)
                self.db.execute('INSERT INTO invites(hash,actor,name,grants,expires) VALUES(?,?,?,?,?)', (digest(code), invited, str(body.get('name', invited))[:200], encoded(grants), expires))
                self.emit('invitation.created', {'actor': invited, 'expires': expires})
                return {'protocol': 'otoha-collaboration/1', 'url': self.config['public_url'], 'actor': invited, 'invitation': code, 'expires': expires, 'grants': grants}
            if method == 'POST' and path == '/v1/invites/revoke':
                self.owner(actor)
                invited = name(body.get('actor'))
                self.db.execute('UPDATE invites SET revoked=1 WHERE actor=?', (invited,))
                self.emit('invitation.revoked', {'actor': invited})
                return {'actor': invited, 'revoked': True}
            if path == '/v1/actors':
                self.owner(actor)
                return {'actors': self.rows('SELECT * FROM actors ORDER BY id')}
            if method == 'POST' and path == '/v1/grants':
                self.owner(actor)
                target, project, role = name(body.get('actor')), name(body.get('project')), body.get('role')
                target_row = self.row('SELECT * FROM actors WHERE id=?', (target,))
                if not target_row or target_row['owner']:
                    raise Error(400, 'invalid_grant_target')
                if role is None:
                    self.db.execute('DELETE FROM grants WHERE actor=? AND project=?', (target, project))
                elif role in ROLES:
                    self.db.execute('INSERT INTO grants VALUES(?,?,?) ON CONFLICT(actor,project) DO UPDATE SET role=excluded.role', (target, project, role))
                else:
                    raise Error(400, 'invalid_role')
                self.emit('grant.changed', {'actor': target, 'project': project, 'role': role})
                return {'actor': target, 'project': project, 'role': role}
            if method == 'POST' and path == '/v1/actors/disable':
                self.owner(actor)
                target = name(body.get('actor'))
                target_row = self.row('SELECT * FROM actors WHERE id=?', (target,))
                if not target_row or target_row['owner']:
                    raise Error(400, 'owner_cannot_be_disabled')
                self.db.execute('UPDATE actors SET disabled=? WHERE id=?', (int(bool(body.get('disabled', True))), target))
                self.emit('actor.disabled', {'actor': target, 'disabled': bool(body.get('disabled', True))})
                return {'actor': target, 'disabled': bool(body.get('disabled', True))}
            if path == '/v1/credentials':
                if method == 'GET':
                    return {'credentials': self.rows('SELECT device,created,revoked FROM credentials WHERE actor=?', (aid,))}
                device, credential = name(body.get('device')), body.get('credential', '')
                if not isinstance(credential, str) or len(credential) < 40 or len(credential) > 200:
                    raise Error(400, 'invalid_credential')
                self.db.execute('INSERT INTO credentials(hash,actor,device,created) VALUES(?,?,?,?)', (digest(credential), aid, device, time.time()))
                self.emit('credential.created', {'actor': aid, 'device': device})
                return {'actor': aid, 'device': device}
            if method == 'POST' and path == '/v1/credentials/revoke':
                target = body.get('actor', aid)
                if target != aid:
                    self.owner(actor)
                device = name(body.get('device'))
                self.db.execute('UPDATE credentials SET revoked=1 WHERE actor=? AND device=?', (target, device))
                self.emit('credential.revoked', {'actor': target, 'device': device})
                return {'actor': target, 'device': device, 'revoked': True}
            if path == '/v1/sessions':
                if method == 'GET':
                    return {'sessions': self.rows('SELECT s.*,coalesce(c.task,\'\') task FROM sessions s LEFT JOIN session_context c ON c.actor=s.actor AND c.session=s.id WHERE s.actor=?', (aid,))}
                sid = name(body.get('id'))
                self.db.execute('INSERT INTO sessions VALUES(?,?,?,?,?) ON CONFLICT(actor,id) DO UPDATE SET agent=excluded.agent,device=excluded.device,updated=excluded.updated', (sid, aid, str(body.get('agent', 'human'))[:80], actor['device'], time.time()))
                if 'task' in body:
                    if not isinstance(body['task'], str) or len(body['task']) > 500:
                        raise Error(400, 'invalid_task_description')
                    self.db.execute('INSERT INTO session_context VALUES(?,?,?) ON CONFLICT(actor,session) DO UPDATE SET task=excluded.task', (aid, sid, body['task']))
                return {'id': sid, 'actor': aid, 'agent': body.get('agent', 'human')}
            if method == 'GET' and path == '/v1/agents':
                project = name(query.get('project', ['team'])[0])
                if not self.role(actor, project):
                    raise Error(403, 'project_forbidden')
                return {'agents': self.rows('SELECT s.*,a.name,a.owner,coalesce(c.task,\'\') task FROM sessions s JOIN actors a ON a.id=s.actor LEFT JOIN session_context c ON c.actor=s.actor AND c.session=s.id WHERE a.disabled=0 AND (a.owner=1 OR EXISTS(SELECT 1 FROM grants g WHERE g.actor=a.id AND g.project=?)) ORDER BY s.actor,s.id', (project,)), 'presence': 'registration-and-last-contact; not proof of active generation'}
            if path == '/v1/messages':
                channel = name(query.get('channel', [None])[0] if method == 'GET' else body.get('channel'))
                c = self.channel(actor, channel, write=method != 'GET')
                if method == 'GET':
                    limit = min(max(int(query.get('limit', ['100'])[0]), 1), 500)
                    before = int(query.get('before', [str(2**63 - 1)])[0])
                    messages = self.rows('SELECT * FROM messages WHERE channel=? AND id<? ORDER BY id DESC LIMIT ?', (channel, before, limit + 1))
                    more = len(messages) > limit
                    messages = messages[:limit]
                    return {'messages': [self.message(m) for m in reversed(messages)], 'before': messages[-1]['id'] if messages else None, 'has_more': more}
                requested_sid = body.get('session')
                sid = requested_sid if isinstance(requested_sid, str) and NAME.fullmatch(requested_sid) else 'device-' + actor['device'][:60]
                client_id = name(body.get('client_id', secrets.token_hex(16)))
                def valid_target(value):
                    if not isinstance(value, str) or '/' not in value:
                        return False
                    target_actor, target_session = value.split('/', 1)
                    return bool(NAME.fullmatch(target_actor) and NAME.fullmatch(target_session) and self.row('SELECT 1 FROM sessions WHERE actor=? AND id=?', (target_actor, target_session)))
                payload = normalize(body, valid_target, lambda v: type(v) is int and bool(self.row('SELECT 1 FROM messages WHERE id=? AND channel=?', (v, channel))))
                if requested_sid is not None and requested_sid != sid:
                    payload.setdefault('warnings', []).append('session_normalized')
                    payload.setdefault('declared_metadata', {})['session'] = requested_sid
                if not isinstance(payload['text'], str) or len(payload['text'].encode()) > 60000 or not payload['text'].strip():
                    raise Error(400, 'invalid_message_text')
                data = encoded(payload)
                old = self.row('SELECT * FROM messages WHERE actor=? AND client_id=?', (aid, client_id))
                if old:
                    if old['channel'] != channel or old['session'] != sid or old['payload'] != data:
                        raise Error(409, 'idempotency_conflict')
                    return {'message_id': old['id'], 'client_id': client_id, 'duplicate': True, 'warnings': payload.get('warnings', [])}
                registered = self.row('SELECT s.*,coalesce(c.task,\'\') task FROM sessions s LEFT JOIN session_context c ON c.actor=s.actor AND c.session=s.id WHERE s.actor=? AND s.id=?', (aid, sid))
                if registered is None:
                    self.db.execute('INSERT INTO sessions VALUES(?,?,?,?,?)', (sid, aid, payload.get('agent', 'unspecified'), actor['device'], time.time()))
                origin = {'recorded': True, 'actor': aid, 'device': actor['device'], 'session': sid,
                          'source': payload.get('source', 'unspecified'),
                          'agent': payload.get('agent', registered['agent'] if registered else ''),
                          'task': payload.get('task', registered['task'] if registered else ''),
                          'source_is': 'sender-declared; not physical-human or model attestation'}
                cur = self.db.execute('INSERT INTO messages(channel,actor,session,client_id,payload,created,origin) VALUES(?,?,?,?,?,?,?)', (channel, aid, sid, client_id, data, time.time(), encoded(origin)))
                message = self.message(self.row('SELECT * FROM messages WHERE id=?', (cur.lastrowid,)))
                event_id = self.emit('message.created', message, c['project'], channel)
                return {'message_id': cur.lastrowid, 'client_id': client_id, 'event_id': event_id, 'duplicate': False, 'warnings': payload.get('warnings', [])}
            if path == '/v1/receipts':
                channel = name(query.get('channel', [None])[0] if method == 'GET' else body.get('channel'))
                c = self.channel(actor, channel)
                if method == 'GET':
                    return {'receipts': self.rows('SELECT * FROM receipts WHERE channel=?', (channel,))}
                sid = name(body.get('session'))
                if not self.row('SELECT 1 FROM sessions WHERE actor=? AND id=?', (aid, sid)):
                    raise Error(400, 'session_not_registered')
                received, read = int(body.get('received', 0)), int(body.get('read', 0))
                high = self.row('SELECT COALESCE(MAX(id),0) n FROM messages WHERE channel=?', (channel,))['n']
                if received < 0 or read < 0 or read > received or received > high:
                    raise Error(400, 'invalid_receipt')
                self.db.execute('INSERT INTO receipts VALUES(?,?,?,?,?) ON CONFLICT(actor,session,channel) DO UPDATE SET received=MAX(received,excluded.received),read=MAX(read,excluded.read)', (aid, sid, channel, received, read))
                receipt = dict(self.row('SELECT * FROM receipts WHERE actor=? AND session=? AND channel=?', (aid, sid, channel)))
                self.emit('receipt.changed', receipt, c['project'], channel)
                return receipt
            raise Error(404, 'route_not_found')

class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True
    request_queue_size = 32
    def __init__(self, address, store):
        self.store = store
        self.slots = threading.BoundedSemaphore(64)
        super().__init__(address, Handler)
    def process_request(self, request, address):
        if not self.slots.acquire(blocking=False):
            request.close()
            return
        try:
            super().process_request(request, address)
        except BaseException:
            self.slots.release()
            raise
    def process_request_thread(self, request, address):
        try:
            super().process_request_thread(request, address)
        finally:
            self.slots.release()

class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def setup(self):
        super().setup()
        self.connection.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        self.connection.settimeout(65)
    def log_message(self, fmt, *args):
        pass  # supervisor manages bounded process logs; never log credentials/queries
    def send_json(self, status, data):
        raw = encoded(data).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(raw)))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.end_headers()
        self.wfile.write(raw)
    def do_GET(self):
        self.dispatch('GET')
    def do_POST(self):
        self.dispatch('POST')
    def dispatch(self, method):
        try:
            # Local gateways and future same-origin web UI use the same API. Never
            # allow an arbitrary website to drive an authenticated local service.
            origin = self.headers.get('Origin')
            if origin:
                expected = urllib.parse.urlsplit(origin)
                if expected.netloc != self.headers.get('Host') or expected.scheme not in ('http', 'https'):
                    raise Error(403, 'cross_origin_forbidden')
            u = urllib.parse.urlsplit(self.path)
            query = urllib.parse.parse_qs(u.query)
            if method == 'GET' and u.path == '/health':
                self.server.store.guard()
                self.send_json(200, {'status': 'ok', 'version': VERSION})
                return
            header = self.headers.get('Authorization', '')
            token = header[7:] if header.startswith('Bearer ') else ''
            if method == 'GET' and u.path == '/v1/events':
                self.stream(token, query)
                return
            body = {}
            if method == 'POST':
                if self.headers.get('Transfer-Encoding') or self.headers.get_content_type() != 'application/json':
                    raise Error(400, 'json_content_length_required')
                n = int(self.headers.get('Content-Length', '0'))
                if not 0 < n <= 65536:
                    raise Error(413, 'body_too_large')
                body = json.loads(self.rfile.read(n))
                if not isinstance(body, dict):
                    raise Error(400, 'json_object_required')
            result = self.server.store.handle(method, u.path, query, body, token)
            if method == 'POST':
                with self.server.store.changed:
                    self.server.store.changed.notify_all()
            self.send_json(200, result)
        except Error as e:
            self.close_connection = True
            self.send_json(e.status, {'error': e.code})
        except (ValueError, TypeError, KeyError, OverflowError):
            self.close_connection = True
            self.send_json(400, {'error': 'invalid_request'})
        except sqlite3.IntegrityError:
            self.send_json(409, {'error': 'conflict'})
        except sqlite3.DatabaseError:
            self.send_json(503, {'error': 'database_unavailable'})
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            self.close_connection = True
        except OSError:
            self.send_json(503, {'error': 'storage_unavailable'})
    def stream(self, token, query):
        store = self.server.store
        after = int(query.get('after', [self.headers.get('Last-Event-ID', '0')])[0])
        wait = min(max(float(query.get('wait', ['45'])[0]), 0), 45)
        channel = query.get('channel', [None])[0]
        sse = self.headers.get('Accept', '') == 'text/event-stream'
        deadline = time.monotonic() + (55 if sse else wait)
        with store.lock:
            actor = store.authenticate(token)
            page = store.events(actor, after, channel)
        if sse:
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Cache-Control', 'no-store')
            self.send_header('Connection', 'close')
            self.send_header('X-Accel-Buffering', 'no')
            self.end_headers()
            self.close_connection = True
        while True:
            after = page['cursor']
            if sse:
                for event in page['events']:
                    self.wfile.write(f"id: {event['id']}\nevent: {event['kind']}\ndata: {encoded(event)}\n\n".encode())
                self.wfile.write(f"event: cursor\ndata: {encoded({'cursor': after})}\n\n".encode())
                self.wfile.flush()
            elif page['events'] or page['has_more'] or time.monotonic() >= deadline:
                self.send_json(200, page)
                return
            if time.monotonic() >= deadline:
                return
            if not page['has_more']:
                with store.changed:
                    store.changed.wait(min(1, max(0, deadline - time.monotonic())))
            try:
                with store.lock:
                    actor = store.authenticate(token)  # immediate revocation on live subscriptions
                    page = store.events(actor, after, channel)
            except Error as e:
                if sse:
                    self.wfile.write(f'event: error\ndata: {encoded({"error": e.code})}\n\n'.encode())
                    self.wfile.flush()
                    return
                raise

def initialize(args):
    state = pathlib.Path(args.state).resolve()
    if state.exists() and (state / 'server.json').exists():
        raise SystemExit('Already initialized; refusing to replace authority or credentials')
    mount = pathlib.Path(args.mount).resolve() if args.mount else None
    if mount and (not os.path.ismount(mount) or not state.is_relative_to(mount)):
        raise SystemExit('State must be below a mounted external volume')
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    state.chmod(0o700)
    volume_id = None
    if mount:
        marker = mount / '.otoha-collaboration-volume-id'
        if not marker.exists():
            fd = os.open(marker, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, 'w') as f:
                f.write(secrets.token_hex(32) + '\n')
                os.fchmod(f.fileno(), 0o644)  # non-secret volume identity; separate service UIDs may read
        volume_id = marker.read_text().strip()
    config = {'public_url': args.url.rstrip('/'), 'mount': str(mount) if mount else None, 'volume_id': volume_id, 'min_free_bytes': args.min_free_bytes}
    private_write(state / 'server.json', config)
    store = Store(state)
    token = secrets.token_urlsafe(32)
    with store.db:
        store.db.execute('INSERT INTO actors(id,name,owner) VALUES(?,?,1)', (name(args.owner), args.owner))
        store.db.execute('INSERT INTO credentials(hash,actor,device,created) VALUES(?,?,?,?)', (digest(token), args.owner, 'owner', time.time()))
        store.db.execute('INSERT INTO projects VALUES(?,?)', ('team', 'Team'))
        store.db.execute('INSERT INTO channels VALUES(?,?,?,NULL)', ('general', 'team', 'General'))
        store.emit('server.initialized', {'owner': args.owner})
    private_write(args.credentials, {'protocol': 'otoha-collaboration/1', 'url': config['public_url'], 'actor': args.owner, 'device': 'owner', 'token': token})
    store.db.close()
    print(encoded({'initialized': True, 'state': str(state), 'credentials_file': str(pathlib.Path(args.credentials).resolve())}))

def main():
    os.umask(0o077)
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest='command', required=True)
    init = sub.add_parser('init')
    init.add_argument('--state', required=True); init.add_argument('--owner', default='owner')
    init.add_argument('--url', required=True); init.add_argument('--credentials', required=True)
    init.add_argument('--mount'); init.add_argument('--min-free-bytes', type=int, default=2*1024**3)
    serve = sub.add_parser('serve')
    serve.add_argument('--state', required=True); serve.add_argument('--host', default='127.0.0.1')
    serve.add_argument('--port', type=int, default=8788); serve.add_argument('--cert'); serve.add_argument('--key')
    serve.add_argument('--tls-host', default='0.0.0.0')
    serve.add_argument('--tls-port', type=int, help='Optional second TLS listener, sharing the same state with the loopback SSH endpoint')
    backup = sub.add_parser('backup')
    backup.add_argument('--state', required=True); backup.add_argument('--out', required=True)
    check = sub.add_parser('check'); check.add_argument('--state', required=True)
    args = p.parse_args()
    if args.command == 'init':
        initialize(args)
        return
    if args.command in ('backup', 'check'):
        store = Store(args.state)
        if args.command == 'check':
            result = store.row('PRAGMA integrity_check')[0]
            print(encoded({'integrity': result, 'storage_guard': 'ok'}))
            raise SystemExit(0 if result == 'ok' else 1)
        dest = pathlib.Path(args.out).resolve()
        if dest.exists():
            raise SystemExit('Backup target exists; refusing overwrite')
        dest.parent.mkdir(parents=True, exist_ok=True)
        connection = sqlite3.connect(dest)
        try:
            store.db.backup(connection)
            if connection.execute('PRAGMA integrity_check').fetchone()[0] != 'ok':
                raise SystemExit('Backup integrity check failed')
        finally:
            connection.close(); store.db.close()
        dest.chmod(0o600)
        print(encoded({'backup': str(dest), 'sha256': hashlib.sha256(dest.read_bytes()).hexdigest()}))
        return
    extra_tls = args.tls_port is not None
    primary_tls = bool(args.cert and args.key and not extra_tls)
    if args.host not in ('127.0.0.1', '::1') and not primary_tls:
        raise SystemExit('Non-loopback listeners require TLS; use SSH forwarding for private access')
    if extra_tls and not (args.cert and args.key):
        raise SystemExit('The second TLS listener requires its certificate and key')
    store = Store(args.state)
    server = Server((args.host, args.port), store)
    listeners = [{'host': args.host, 'port': server.server_port, 'tls': primary_tls}]
    if primary_tls or extra_tls:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(args.cert, args.key)
        if primary_tls:
            server.socket = context.wrap_socket(server.socket, server_side=True, do_handshake_on_connect=False)
        else:
            lan = Server((args.tls_host, args.tls_port), store)
            lan.socket = context.wrap_socket(lan.socket, server_side=True, do_handshake_on_connect=False)
            threading.Thread(target=lan.serve_forever, daemon=True).start()
            listeners.append({'host': args.tls_host, 'port': lan.server_port, 'tls': True})
    print(encoded({'service': 'otoha-collaboration', 'version': VERSION, 'listeners': listeners}), flush=True)
    server.serve_forever()

if __name__ == '__main__':
    main()

"""Project-authenticated free property graph over a private SurrealDB 3.3.5.

This module never exposes SQL or database credentials to a member. Graph network
work runs outside the collaboration store's SQLite lock/transaction.
"""
import base64
import hashlib
import json
import math
import os
import re
import threading
import urllib.error
import urllib.parse
import urllib.request


SCHEMA = 'otoha-graph/1'
NAME = re.compile(r'^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$')
MAX_BACKEND_BYTES = 16 * 1024 * 1024
MAX_RESULT_BYTES = 512 * 1024


class GraphError(Exception):
    def __init__(self, status, code):
        self.status, self.code = status, code
        super().__init__(code)


def identifier(value):
    if not isinstance(value, str) or not NAME.fullmatch(value):
        raise GraphError(400, 'invalid_identifier')
    return value


def integer(value, maximum=2**53 - 1):
    if type(value) is not int or not 0 <= value <= maximum:
        raise GraphError(400, 'invalid_graph_integer')
    return value


def json_value(value, depth=0):
    if depth > 32:
        raise GraphError(400, 'graph_value_too_deep')
    if value is None or isinstance(value, (str, bool)):
        return
    if type(value) is int:
        if abs(value) > 2**53 - 1:
            raise GraphError(400, 'graph_number_out_of_range')
        return
    if type(value) is float and math.isfinite(value):
        return
    if isinstance(value, list):
        for item in value:
            json_value(item, depth + 1)
        return
    if isinstance(value, dict) and all(isinstance(k, str) for k in value):
        for key, item in value.items():
            if not key or len(key.encode('utf-8')) > 256:
                raise GraphError(400, 'invalid_graph_property_key')
            json_value(item, depth + 1)
        return
    raise GraphError(400, 'invalid_graph_value')


def encoded(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True,
                      separators=(',', ':'), allow_nan=False)


def properties(value):
    if not isinstance(value, dict):
        raise GraphError(400, 'invalid_graph_properties')
    for key, item in value.items():
        if not isinstance(key, str) or not key or len(key.encode()) > 256:
            raise GraphError(400, 'invalid_graph_property_key')
        # Depth is a property-value contract, independent of HTTP envelopes and
        # the backend's storage wrapper. Every accepted value stays readable.
        json_value(item)
    return value


def shape(value, allowed, required=()):
    if not isinstance(value, dict) or set(value) - set(allowed) or set(required) - set(value):
        raise GraphError(400, 'invalid_graph_request')


def record(kind, value):
    # NAME excludes quoting/control characters. Quoted keys preserve dots and
    # hyphens as one stable logical identity instead of interpreting paths.
    return kind + ':`' + identifier(value) + '`'


def binding_for(store, project):
    path = store.state / 'graph.json'
    try:
        if os.name != 'nt' and path.stat().st_mode & 0o077:
            raise GraphError(503, 'graph_configuration_not_private')
        config = json.loads(path.read_text())
    except FileNotFoundError:
        raise GraphError(404, 'graph_not_bound') from None
    except (OSError, ValueError, TypeError):
        raise GraphError(503, 'graph_configuration_invalid') from None
    try:
        binding = config.get('projects', {}).get(project)
        if binding is None:
            raise GraphError(404, 'graph_not_bound')
        url = urllib.parse.urlsplit(config['url'])
        if (url.scheme != 'http' or url.hostname not in ('127.0.0.1', 'localhost', '::1')
                or url.username or url.password or url.query or url.fragment
                or url.path not in ('', '/') or not url.port):
            raise ValueError('private loopback endpoint required')
        result = {k: config[k] for k in ('username', 'password', 'namespace')}
        if not all(isinstance(result[k], str) and result[k] for k in result):
            raise ValueError('credentials and namespace required')
        identifier(result['namespace'])
        result.update(project=project, url=config['url'].rstrip('/'),
                      database=identifier(binding['database']),
                      graph_id=identifier(binding['graph_id']))
        # The first implementation gives each project its own database.
        if sum(b.get('database') == result['database'] for b in config['projects'].values()) != 1:
            raise ValueError('project databases must be distinct')
        return result
    except GraphError:
        raise
    except (KeyError, ValueError, TypeError, AttributeError):
        raise GraphError(503, 'graph_configuration_invalid') from None


def handle_graph(store, method, path, query, body, token):
    # Authentication, revocation and project grants use the existing authority.
    # No remote request or backend initialization is made while holding its lock.
    with store.lock:
        actor = store.authenticate(token)
        project = identifier(query.get('project', [None])[0] if method == 'GET' else body.get('project'))
        if not store.row('SELECT 1 FROM projects WHERE id=?', (project,)):
            raise GraphError(404, 'project_not_found')
        role = store.role(actor, project)
        if not role:
            raise GraphError(403, 'project_forbidden')
        if method == 'POST' and path == '/v1/graph/mutations' and role not in ('owner', 'admin', 'member'):
            raise GraphError(403, 'graph_write_required')
        binding = binding_for(store, project)
        key = hashlib.sha256(encoded(binding).encode()).hexdigest()
        backends = getattr(store, '_graph_backends', None)
        if backends is None:
            backends = store._graph_backends = {}
        if key not in backends:
            backends[key] = GraphBackend(binding)
        backend = backends[key]
    if method == 'GET' and path == '/v1/graph/manifest':
        return backend.manifest()
    if method == 'POST' and path == '/v1/graph/query':
        return backend.query(body)
    if method == 'POST' and path == '/v1/graph/mutations':
        return backend.mutate(actor['id'], body)
    prefix = '/v1/graph/requests/'
    if method == 'GET' and path.startswith(prefix):
        return backend.receipt(actor['id'], identifier(path[len(prefix):]))
    raise GraphError(404, 'graph_route_not_found')


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class GraphBackend:
    def __init__(self, binding):
        self.binding = binding
        self.lock = threading.RLock()
        self.initialized = False
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def sql(self, text):
        b = self.binding
        authorization = base64.b64encode((b['username'] + ':' + b['password']).encode()).decode()
        request = urllib.request.Request(b['url'] + '/sql', data=text.encode(), headers={
            'Authorization': 'Basic ' + authorization, 'Accept': 'application/json',
            'Content-Type': 'text/plain', 'surreal-ns': b['namespace'], 'surreal-db': b['database']})
        try:
            with self.opener.open(request, timeout=10) as response:
                raw = response.read(MAX_BACKEND_BYTES + 1)
            if len(raw) > MAX_BACKEND_BYTES:
                raise GraphError(413, 'graph_result_too_large')
            rows = json.loads(raw)
        except (OSError, ValueError, urllib.error.URLError):
            raise GraphError(503, 'graph_backend_unavailable') from None
        if not isinstance(rows, list) or not rows or any(not isinstance(r, dict) for r in rows):
            raise GraphError(503, 'graph_backend_invalid_response')
        errors = [str(r.get('result', '')) for r in rows if r.get('status') != 'OK']
        if errors:
            joined = ' '.join(errors)
            for code, status in [('graph_request_id_conflict', 409), ('graph_revision_conflict', 409),
                                 ('graph_object_exists', 409), ('graph_object_not_found', 404),
                                 ('graph_incident_edges', 409), ('graph_endpoint_not_found', 409),
                                 ('graph_identity_conflict', 409)]:
                if code in joined:
                    raise GraphError(status, code)
            # Backend details can contain queries or credentials. Never echo them.
            raise GraphError(503, 'graph_backend_query_failed')
        return [r.get('result') for r in rows]

    def ensure(self):
        if self.initialized:
            return
        b = self.binding
        self.sql('DEFINE NAMESPACE IF NOT EXISTS `' + b['namespace'] + '`; '
                 'DEFINE DATABASE IF NOT EXISTS `' + b['database'] + '`; '
                 'DEFINE TABLE IF NOT EXISTS node TYPE NORMAL SCHEMALESS; '
                 'DEFINE TABLE IF NOT EXISTS edge TYPE RELATION IN node OUT node ENFORCED SCHEMALESS; '
                 'DEFINE TABLE IF NOT EXISTS meta TYPE NORMAL SCHEMALESS; '
                 'DEFINE TABLE IF NOT EXISTS request TYPE NORMAL SCHEMALESS; '
                 'IF !record::exists(meta:head) { CREATE meta:head SET revision=0, graph_id='
                 + encoded(b['graph_id']) + '; }; '
                 'IF meta:head.graph_id != ' + encoded(b['graph_id'])
                 + " { THROW 'graph_identity_conflict'; };")
        self.initialized = True

    def envelope(self, version, mode, **data):
        return {'schema': SCHEMA, 'graph_id': self.binding['graph_id'], 'source_version': version,
                'scope': {'project': self.binding['project'], 'mode': mode},
                'complete': True, 'truncated': False, **data}

    def manifest(self):
        with self.lock:
            self.ensure()
            result = self.sql('BEGIN; SELECT * FROM ONLY meta:head; '
                              'SELECT count() AS count FROM node GROUP ALL; '
                              'SELECT count() AS count FROM edge GROUP ALL; COMMIT;')
            head, nodes, edges = result[1:4]
            return self.envelope(head['revision'], 'manifest', ready=True,
                                 counts={'nodes': nodes[0]['count'] if nodes else 0,
                                         'edges': edges[0]['count'] if edges else 0},
                                 capabilities={'queries': ['inspect', 'filter', 'neighborhood'],
                                               'mutations': ['node.add', 'node.remove', 'edge.add',
                                                             'edge.remove', 'prop.set', 'prop.unset'],
                                               'templates': False, 'history_query': False},
                                 limits={'query_limit': 500, 'operations': 100,
                                         'request_bytes': 65536, 'result_bytes': MAX_RESULT_BYTES})

    @staticmethod
    def columns(kind):
        return ('record::id(id) AS id, p AS properties' if kind == 'node' else
                'record::id(id) AS id, record::id(in) AS source, record::id(out) AS target, p AS properties')

    def query(self, body):
        shape(body, ['project', 'mode', 'kind', 'id', 'where', 'direction', 'limit', 'after', 'source_version'], ['project', 'mode'])
        mode, kind = body['mode'], body.get('kind', 'node')
        if mode not in ('inspect', 'filter', 'neighborhood') or kind not in ('node', 'edge'):
            raise GraphError(400, 'invalid_graph_query')
        common = ['project', 'mode', 'source_version']
        fields = {'inspect': ['kind', 'id'], 'filter': ['kind', 'where', 'limit', 'after'],
                  'neighborhood': ['kind', 'id', 'direction', 'limit', 'after']}[mode]
        shape(body, common + fields)
        if mode == 'neighborhood' and kind != 'node':
            raise GraphError(400, 'invalid_graph_query')
        limit = body.get('limit', 100)
        integer(limit, 500)
        if limit == 0:
            raise GraphError(400, 'invalid_graph_query')
        after = body.get('after')
        if after is not None:
            identifier(after)
        version = body.get('source_version')
        if version is not None:
            integer(version)
        sql = ['BEGIN;', 'SELECT * FROM ONLY meta:head;']
        if version is not None:
            sql.append(f"IF meta:head.revision != {version} {{ THROW 'graph_revision_conflict'; }};")
        if mode == 'inspect':
            rid = record(kind, body.get('id'))
            sql.append(f"IF !record::exists({rid}) {{ THROW 'graph_object_not_found'; }};")
            sql.append(f'SELECT {self.columns(kind)} FROM {rid};')
        elif mode == 'filter':
            where = body.get('where', {})
            if not isinstance(where, dict):
                raise GraphError(400, 'invalid_graph_query')
            properties(where)
            conditions = [f'{encoded(key)} IN object::keys(p) AND p[{encoded(key)}] = {encoded(value)}'
                          for key, value in where.items()]
            if after is not None:
                conditions.append('record::id(id) > ' + encoded(after))
            sql.append(f'SELECT {self.columns(kind)} FROM {kind}'
                       + (' WHERE ' + ' AND '.join('(' + c + ')' for c in conditions) if conditions else '')
                       + f' ORDER BY id LIMIT {limit + 1};')
        else:
            rid = record('node', body.get('id'))
            direction = body.get('direction', 'both')
            if direction not in ('in', 'out', 'both'):
                raise GraphError(400, 'invalid_graph_query')
            sql.append(f"IF !record::exists({rid}) {{ THROW 'graph_object_not_found'; }};")
            condition = {'in': f'out = {rid}', 'out': f'in = {rid}',
                         'both': f'(in = {rid} OR out = {rid})'}[direction]
            if after is not None:
                condition += ' AND record::id(id) > ' + encoded(after)
            sql.append(f'LET $selected = (SELECT * FROM edge WHERE {condition} ORDER BY id LIMIT {limit + 1});')
            sql.append('SELECT ' + self.columns('edge') + ' FROM $selected;')
            sql.append('SELECT ' + self.columns('node') + ' FROM node WHERE id IN array::distinct(array::concat('
                       '$selected.in, $selected.out, [' + rid + ']));')
        sql.append('COMMIT;')
        with self.lock:
            self.ensure()
            result = self.sql(' '.join(sql))
        head = result[1]
        rows = result[-3] if mode == 'neighborhood' else result[-2]
        result_nodes = result[-2] if mode == 'neighborhood' else []
        if not isinstance(rows, list):
            raise GraphError(503, 'graph_backend_invalid_response')
        truncated = len(rows) > limit
        rows = rows[:limit]
        response = self.envelope(head['revision'], mode, nodes=[], edges=[], next_after=None)
        response['scope'].update(kind='edge' if mode == 'neighborhood' else kind,
                                 after=after, limit=limit)
        if mode == 'inspect':
            response['scope']['id'] = body['id']
        elif mode == 'filter':
            response['scope']['where'] = body.get('where', {})
        if mode == 'neighborhood':
            response['scope'].update(id=body['id'], direction=body.get('direction', 'both'), depth=1)
        # Bound bytes without changing properties or returning a partial object.
        while True:
            if mode == 'neighborhood':
                members = {body['id']} | {e[k] for e in rows for k in ('source', 'target')}
                response.update(edges=rows, nodes=[n for n in result_nodes if n['id'] in members])
            else:
                response['nodes' if kind == 'node' else 'edges'] = rows
            response.update(complete=not truncated, truncated=truncated,
                            next_after=rows[-1]['id'] if truncated and rows else None)
            if len(encoded(response).encode()) <= MAX_RESULT_BYTES:
                break
            if len(rows) <= 1:
                raise GraphError(413, 'graph_result_too_large')
            rows = rows[:-1]
            truncated = True
        return response

    def request_record(self, actor, request_id):
        key = hashlib.sha256((actor + '\0' + request_id).encode()).hexdigest()
        return record('request', key)

    def public_receipt(self, stored, duplicate):
        return self.envelope(stored['source_version'], 'mutation', state='accepted',
                             request_id=stored['request_id'], expected_revision=stored['expected_revision'],
                             request_sha256=stored['request_sha256'], duplicate=duplicate)

    def receipt(self, actor, request_id):
        with self.lock:
            self.ensure()
            result = self.sql('SELECT * FROM ' + self.request_record(actor, request_id) + ';')[-1]
        if not result:
            raise GraphError(404, 'graph_request_not_found')
        return self.public_receipt(result[0], True)

    def mutate(self, actor, body):
        shape(body, ['project', 'request_id', 'expected_revision', 'operations'],
              ['project', 'request_id', 'expected_revision', 'operations'])
        request_id = identifier(body['request_id'])
        expected = integer(body['expected_revision'])
        operations = body['operations']
        if not isinstance(operations, list) or not 1 <= len(operations) <= 100:
            raise GraphError(400, 'invalid_graph_operations')
        changes = [self.operation(op) for op in operations]
        text = encoded(body)
        if len(text.encode()) > 65536:
            raise GraphError(413, 'body_too_large')
        request_hash = hashlib.sha256(text.encode()).hexdigest()
        rid = self.request_record(actor, request_id)
        sql = ('BEGIN; LET $duplicate = record::exists(' + rid + '); '
               'IF $duplicate { IF ' + rid + '.request_sha256 != ' + encoded(request_hash)
               + " { THROW 'graph_request_id_conflict'; }; } ELSE { "
               + f"IF meta:head.revision != {expected} {{ THROW 'graph_revision_conflict'; }}; "
               + ' '.join(changes) + ' UPDATE meta:head SET revision += 1; CREATE ' + rid
               + ' CONTENT ' + encoded({'actor': actor, 'request_id': request_id,
                                       'expected_revision': expected, 'request_sha256': request_hash,
                                       'operations': operations, 'source_version': expected + 1})
               + '; }; '
               + 'SELECT *, $duplicate AS duplicate FROM ONLY ' + rid + '; COMMIT;')
        with self.lock:
            self.ensure()
            result = self.sql(sql)[-2]
        return self.public_receipt(result, result['duplicate'])

    def operation(self, op):
        if not isinstance(op, dict):
            raise GraphError(400, 'invalid_graph_operation')
        name = op.get('op')
        if name in ('node.add', 'edge.add'):
            kind = name.split('.')[0]
            allowed = ['op', 'id', 'properties'] + (['source', 'target'] if kind == 'edge' else [])
            shape(op, allowed, ['op', 'id'] + (['source', 'target'] if kind == 'edge' else []))
            rid = record(kind, op['id'])
            content = encoded({'p': properties(op.get('properties', {}))})
            guard = f"IF record::exists({rid}) {{ THROW 'graph_object_exists'; }}; "
            if kind == 'node':
                return guard + f'CREATE {rid} CONTENT {content};'
            source, target = record('node', op['source']), record('node', op['target'])
            return (guard + f"IF !record::exists({source}) OR !record::exists({target}) {{ THROW 'graph_endpoint_not_found'; }}; "
                    + f'RELATE {source}->{rid}->{target} CONTENT {content};')
        if name in ('node.remove', 'edge.remove'):
            shape(op, ['op', 'id'], ['op', 'id'])
            kind = name.split('.')[0]
            rid = record(kind, op['id'])
            guard = f"IF !record::exists({rid}) {{ THROW 'graph_object_not_found'; }}; "
            if kind == 'node':
                guard += f"IF array::len((SELECT id FROM edge WHERE in={rid} OR out={rid})) > 0 {{ THROW 'graph_incident_edges'; }}; "
            return guard + f'DELETE {rid};'
        if name in ('prop.set', 'prop.unset'):
            fields = ['op', 'kind', 'id', 'key'] + (['value'] if name == 'prop.set' else [])
            shape(op, fields, fields)
            if op['kind'] not in ('node', 'edge'):
                raise GraphError(400, 'invalid_graph_kind')
            key = op['key']
            if not isinstance(key, str) or not key or len(key.encode()) > 256:
                raise GraphError(400, 'invalid_graph_property_key')
            if name == 'prop.set':
                json_value(op['value'])
            rid = record(op['kind'], op['id'])
            update = ('object::extend(p, object::from_entries(' + encoded([[key, op['value']]]) + '))'
                      if name == 'prop.set' else 'object::remove(p, ' + encoded(key) + ')')
            return f"IF !record::exists({rid}) {{ THROW 'graph_object_not_found'; }}; UPDATE {rid} SET p={update};"
        raise GraphError(400, 'invalid_graph_operation')

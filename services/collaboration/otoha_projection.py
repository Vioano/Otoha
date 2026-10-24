"""Read projections over an already authorized state snapshot, never the store."""
import json
import sqlite3
import time
from contextlib import closing

TABLES = {
    'projects': ('id', 'name'),
    'channels': ('id', 'project', 'name', 'members'),
    'messages': ('id', 'channel', 'actor', 'session', 'client_id', 'text', 'kind',
                 'reply_to', 'created', 'payload', 'origin'),
    'sessions': ('actor', 'id', 'agent', 'device', 'updated', 'task'),
    'receipts': ('actor', 'session', 'channel', 'received', 'read'),
}
PRESETS = {
    'recent': 'SELECT * FROM messages ORDER BY id DESC',
    'incremental': 'SELECT * FROM messages ORDER BY id',
    'to-me': "SELECT DISTINCT m.* FROM messages m, json_each(m.payload, '$.to_sessions') t WHERE t.value=:target ORDER BY m.id DESC",
    'tasks': "SELECT * FROM messages WHERE kind IN ('task','result','question') OR reply_to IS NOT NULL OR coalesce(json_extract(payload,'$.task'),'')<>'' ORDER BY id DESC",
}
MAX_SNAPSHOT_BYTES = 16 * 1024**2


class ProjectionError(ValueError):
    pass


def query(snapshot, request, target):
    """One read-only SQL statement, bounded input, execution and output."""
    sql = request.get('sql')
    params = request.get('params', [])
    if sql is None:
        preset = request.get('preset', 'recent')
        if preset not in PRESETS:
            raise ProjectionError('unknown_preset')
        sql, params = PRESETS[preset], {'target': target}
    if not isinstance(sql, str) or not 0 < len(sql.encode()) <= 8192:
        raise ProjectionError('invalid_query')
    if not isinstance(params, (list, dict)) or len(params) > 100:
        raise ProjectionError('invalid_query_params')
    if any(not isinstance(v, (str, int, float, type(None))) for v in (params.values() if isinstance(params, dict) else params)):
        raise ProjectionError('invalid_query_params')
    limit = request.get('limit', 100)
    if type(limit) is not int or not 1 <= limit <= 500:
        raise ProjectionError('invalid_query_limit')
    size = len(json.dumps(snapshot, ensure_ascii=False).encode())
    if size > MAX_SNAPSHOT_BYTES:
        raise ProjectionError('snapshot_too_large_narrow_scope')
    with closing(sqlite3.connect(':memory:')) as db:
        for table, columns in TABLES.items():
            db.execute(f'CREATE TABLE {table} ({", ".join(columns)})')
            db.executemany(f'INSERT INTO {table} VALUES ({", ".join("?" for _ in columns)})',
                           ([row.get(c) for c in columns] for row in snapshot[table]))
        db.commit()
        db.execute('PRAGMA query_only=ON')
        db.setlimit(sqlite3.SQLITE_LIMIT_LENGTH, 1024**2)
        db.setlimit(sqlite3.SQLITE_LIMIT_COLUMN, 64)
        db.setlimit(sqlite3.SQLITE_LIMIT_VDBE_OP, 25000)
        allowed = {sqlite3.SQLITE_SELECT, sqlite3.SQLITE_READ,
                   sqlite3.SQLITE_FUNCTION, sqlite3.SQLITE_RECURSIVE}
        def authorize(action, arg1, arg2, database, trigger):
            if action not in allowed or (action == sqlite3.SQLITE_FUNCTION and str(arg2).lower() == 'load_extension'):
                return sqlite3.SQLITE_DENY
            return sqlite3.SQLITE_OK
        db.set_authorizer(authorize)
        deadline = time.monotonic() + .25
        steps = 0
        def progress():
            nonlocal steps
            steps += 1000
            return int(steps > 1_000_000 or time.monotonic() > deadline)
        db.set_progress_handler(progress, 1000)
        try:
            cursor = db.execute(sql, params)
            if cursor.description is None:
                raise ProjectionError('query_must_return_rows')
            rows = cursor.fetchmany(limit + 1)
            result = {'columns': [c[0] for c in cursor.description],
                      'rows': rows[:limit], 'truncated': len(rows) > limit}
            if len(json.dumps(result, ensure_ascii=False).encode()) > 1024**2:
                raise ProjectionError('query_result_too_large')
            return result
        except (sqlite3.Error, MemoryError, TypeError) as error:
            raise ProjectionError('query_rejected_or_budget_exceeded') from error

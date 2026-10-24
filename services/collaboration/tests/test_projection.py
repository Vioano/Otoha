"""Closed HTTP/CLI/MCP tests of the authorization-before-projection contract."""
import importlib.util
import json
import pathlib
import sqlite3
import subprocess
import sys
import unittest
from unittest.mock import patch

import test_service as fixture
from otoha_mcp import Adapter
from otoha import write_private


class ProjectionTest(unittest.TestCase):
    setUp = fixture.ServiceTest.setUp
    tearDown = fixture.ServiceTest.tearDown
    call = fixture.ServiceTest.call
    member = fixture.ServiceTest.member
    send = fixture.ServiceTest.send

    def read_query(self, token=None, **request):
        return self.call('POST', '/v1/query', request, token or self.owner)

    def private_team(self):
        a = self.member('alice')[0]
        b = self.member('bob')[0]
        c = self.member('carol')[0]
        self.call('POST', '/v1/channels', {'id': 'a-b', 'project': 'team', 'members': ['alice', 'bob']}, a)
        self.send(token=a, actor='alice', channel='a-b', identifier='secret', text='PRIVATE_A_B_SENTINEL')
        self.send(token=c, actor='carol', identifier='public', text='Public team message')
        return a, b, c

    def test_private_history_events_receipts_and_all_sql_shapes(self):
        a, b, c = self.private_team()
        for token in (a, b, self.owner):
            result = self.read_query(token, sql='SELECT count(*) FROM messages')
            self.assertEqual(result['rows'], [[2]])
        for route in ('messages', 'receipts'):
            self.call('GET', f'/v1/{route}?channel=a-b', token=c, status=403)
        events = self.call('GET', '/v1/events?after=0&wait=0', token=c)
        self.assertNotIn('PRIVATE_A_B_SENTINEL', json.dumps(events))
        for sql in ('SELECT * FROM messages', 'SELECT count(*) FROM messages',
                    'SELECT m.text FROM messages m JOIN channels c ON m.channel=c.id',
                    'WITH x AS (SELECT * FROM messages) SELECT * FROM x',
                    "SELECT * FROM messages WHERE channel='a-b' OR 1=1"):
            result = self.read_query(c, sql=sql)
            self.assertEqual(result['scope']['snapshot_messages'], 1)
            self.assertNotIn('PRIVATE_A_B_SENTINEL', json.dumps(result))
        self.call('POST', '/v1/query', {'channel': 'a-b', 'sql': 'SELECT * FROM messages'}, c, status=403)

    def test_credentials_role_claims_and_cross_project_scope(self):
        c = self.private_team()[2]
        self.call('POST', '/v1/projects', {'id': 'other'}, self.owner)
        self.call('POST', '/v1/channels', {'id': 'other', 'project': 'other'}, self.owner)
        self.send(channel='other', identifier='other', text='OTHER_PROJECT_SENTINEL')
        self.call('POST', '/v1/query', {'project': 'other'}, c, status=403)
        claimed = self.read_query(c, sql='SELECT * FROM messages', owner=True, actor='owner')
        self.assertEqual(claimed['scope']['authorization'], 'member')
        self.assertNotIn('OTHER_PROJECT_SENTINEL', json.dumps(claimed))
        for sql in ('SELECT * FROM credentials', 'SELECT * FROM invites', 'SELECT * FROM events'):
            self.call('POST', '/v1/query', {'sql': sql}, self.owner, status=400)
        self.call('POST', '/v1/grants', {'actor': 'carol', 'project': 'team', 'role': None}, self.owner)
        self.call('POST', '/v1/query', {}, c, status=403)

    def test_sql_writes_file_access_and_cpu_bombs_cannot_change_store(self):
        self.send(text='Keep me')
        for sql in ('DELETE FROM messages', "UPDATE messages SET text='oops'",
                    'CREATE TABLE x(a)', "ATTACH '/tmp/otoha-evil.db' AS evil",
                    'PRAGMA table_info(messages)', "SELECT load_extension('/tmp/x')",
                    'SELECT 1; SELECT 2', 'SELECT randomblob(1000000000)',
                    'WITH RECURSIVE x(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM x) SELECT sum(n) FROM x'):
            self.call('POST', '/v1/query', {'sql': sql}, self.owner, status=400)
        self.assertEqual(self.read_query(sql='SELECT text FROM messages')['rows'], [['Keep me']])
        self.assertEqual(self.call('GET', '/health')['status'], 'ok')
        self.assertEqual(self.store.row('SELECT count(*) n FROM messages')['n'], 1)

    def test_presets_parameters_scopes_and_truthful_truncation(self):
        for i in range(4):
            self.send(identifier=f'p-{i}', kind='task' if i == 1 else 'message', text=f'item {i}', to_sessions=['owner/owner-session'] if i == 2 else [])
        result = self.read_query(preset='incremental', after=1, limit=1)
        self.assertTrue(result['truncated'])
        self.assertTrue(result['scope']['input_complete'])
        self.assertEqual(result['scope']['snapshot_messages'], 3)
        self.assertEqual(len(self.read_query(preset='tasks')['rows']), 1)
        self.assertEqual(len(self.read_query(preset='to-me', session='owner-session')['rows']), 1)
        result = self.read_query(sql='SELECT text FROM messages WHERE id=:id', params={'id': 3})
        self.assertEqual(result['rows'], [['item 2']])
        self.assertEqual(self.read_query(sql='SELECT id AS same,id AS same FROM messages LIMIT 1')['columns'], ['same', 'same'])

    def test_tolerant_descriptions_keep_text_without_guessing_privacy(self):
        c = self.private_team()[2]
        body = {'channel': 'general', 'client_id': 'imperfect', 'text': 'Please check the player jump',
                'session': ['bad'], 'kind': {'not': 'an enum'}, 'source': ['robot'], 'task': 7,
                'refs': [{'repo': 'game/repo', 'commit': 'main', 'path': '../secret'}],
                'reply_to': {'id': 1}, 'to_sessions': ['missing/nope'], 'delivery': ['chat']}
        response = self.call('POST', '/v1/messages', body, c)
        self.assertGreaterEqual(len(response['warnings']), 7)
        repeat = self.call('POST', '/v1/messages', body, c)
        self.assertTrue(repeat['duplicate'])
        history = self.call('GET', '/v1/messages?channel=general', token=c)['messages'][-1]
        self.assertEqual(history['payload']['text'], body['text'])
        self.assertEqual(history['payload']['kind'], 'message')
        self.assertEqual(history['payload']['refs'], [])
        self.assertEqual(history['payload']['declared_metadata']['refs'], body['refs'])
        self.assertEqual(history['origin']['source'], 'unspecified')
        self.assertEqual(history['origin']['actor'], 'carol')
        self.assertIn('Please check', json.dumps(self.read_query(c, preset='recent')))
        self.call('POST', '/v1/messages', {'text': 'no channel'}, c, status=400)
        self.call('POST', '/v1/messages', {**body, 'channel': 'a-b'}, c, status=403)
        self.call('POST', '/v1/messages', {**body, 'text': ' '}, c, status=400)
        self.assertEqual(self.call('GET', '/health')['status'], 'ok')

    def test_origin_is_immutable_and_authenticated_but_source_is_claimed(self):
        a = self.member('alice')[0]
        self.call('POST', '/v1/sessions', {'id': 'alice-session', 'agent': 'codex', 'task': 'Implement movement'}, a)
        self.send(token=a, actor='alice', source='agent', identifier='source', owner=True)
        self.call('POST', '/v1/sessions', {'id': 'alice-session', 'agent': 'different-agent', 'task': 'Different task'}, a)
        self.assertTrue(self.send(token=a, actor='alice', source='agent', identifier='source', owner=True)['duplicate'])
        old = self.call('GET', '/v1/messages?channel=general', token=a)['messages'][0]['origin']
        self.assertEqual((old['actor'], old['device'], old['agent'], old['task']), ('alice', 'device', 'codex', 'Implement movement'))
        self.send(token=a, actor='alice', identifier='claimed-human', source='human')
        latest = self.call('GET', '/v1/messages?channel=general', token=a)['messages'][-1]['origin']
        self.assertIn('sender-declared', latest['source_is'])

    def test_human_cli_no_session_custom_sql_and_mcp_projection(self):
        profile = json.loads(self.owner_file.read_text()); profile['url'] = self.url
        write_private(self.owner_file, profile, replace=True)
        def cli(*args):
            return json.loads(subprocess.run([sys.executable, 'otoha.py', '--profile', str(self.owner_file), *args], capture_output=True, text=True, check=True).stdout)
        cli('send', '--text', 'Human without session', '--id', 'human')
        self.assertIn('共享状态', cli('protocol')['guide'])
        self.assertEqual(cli('query', '--sql', 'SELECT text FROM messages')['rows'], [['Human without session']])
        self.assertEqual(cli('history')['messages'][0]['origin']['source'], 'human')
        adapter = Adapter(self.owner_file, 'actual-test-conversation')
        self.assertEqual(adapter.call('protocol', {})['manifest']['actor']['id'], 'owner')
        adapter.call('send', {'channel': 'general', 'text': 'Agent message', 'client_id': 'agent', 'task': 'Review movement'})
        result = adapter.call('query', {'sql': "SELECT json_extract(origin,'$.task') FROM messages WHERE kind='message' ORDER BY id DESC LIMIT 1"})
        self.assertEqual(result['rows'], [['Review movement']])
        self.assertEqual(cli('history')['messages'][-1]['origin']['source'], 'agent')

    def test_additive_migration_old_clients_and_code_only_rollback(self):
        oldpath = pathlib.Path('tests/fixtures/server-v0.1.2.py')
        spec = importlib.util.spec_from_file_location('old_otoha_server', oldpath)
        old = importlib.util.module_from_spec(spec); spec.loader.exec_module(old)
        # Build a real old schema with existing credentials and history.
        oldstate = self.root / 'old-state'; oldstate.mkdir()
        (oldstate / 'server.json').write_text(json.dumps({'min_free_bytes': 0, 'public_url': self.url}))
        db = sqlite3.connect(oldstate / 'messages.sqlite3'); db.executescript(old.SCHEMA)
        db.execute('INSERT INTO actors VALUES(?,?,1,0)', ('owner', 'Captain'))
        db.execute('INSERT INTO credentials VALUES(?,?,?,0,?)', (old.digest(self.owner), 'owner', 'owner', 0))
        db.execute('INSERT INTO projects VALUES(?,?)', ('team', 'Team'))
        db.execute('INSERT INTO channels VALUES(?,?,?,NULL)', ('general', 'team', 'General'))
        db.commit(); db.close()
        legacy = old.Store(oldstate)
        request = {'channel': 'general', 'session': 'old-session', 'client_id': 'old-message', 'text': 'Old message'}
        legacy.handle('POST', '/v1/sessions', {}, {'id': 'old-session'}, self.owner)
        legacy.handle('POST', '/v1/messages', {}, request, self.owner); legacy.db.close()
        from otoha_server import Store
        migrated = Store(oldstate)
        history = migrated.handle('GET', '/v1/messages', {'channel': ['general']}, {}, self.owner)
        self.assertFalse(history['messages'][0]['origin']['recorded'])
        self.assertTrue(migrated.handle('POST', '/v1/messages', {}, request, self.owner)['duplicate'])
        migrated.handle('POST', '/v1/messages', {}, {**request, 'client_id': 'new-message', 'text': 'New message'}, self.owner)
        migrated.db.close()
        rollback = old.Store(oldstate)
        rollback.handle('POST', '/v1/sessions', {}, {'id': 'rollback-session'}, self.owner)
        rollback.handle('POST', '/v1/messages', {}, {**request, 'client_id': 'rollback-message'}, self.owner)
        self.assertEqual(len(rollback.handle('GET', '/v1/messages', {'channel': ['general']}, {}, self.owner)['messages']), 3)
        rollback.db.close()

    def test_pre_upgrade_outbox_is_retried_verbatim(self):
        profile = json.loads(self.owner_file.read_text()); profile['url'] = self.url
        write_private(self.owner_file, profile, replace=True)
        payload = {'channel': 'general', 'session': 'owner-session', 'client_id': 'old-outbox', 'text': 'A request with unknown outcome'}
        accepted = self.call('POST', '/v1/messages', payload, self.owner)
        write_private(self.root / 'mcp-outbox/old-outbox.json', {'payload': payload, 'result': None})
        adapter = Adapter(self.owner_file, 'owner-session')
        result = adapter.call('send', {k: v for k, v in payload.items() if k != 'session'})
        self.assertTrue(result['duplicate'])
        self.assertEqual(result['message_id'], accepted['message_id'])

    def test_over_budget_snapshot_is_refused_not_silently_incomplete(self):
        self.send(text='A real message')
        with patch('otoha_server.MAX_SNAPSHOT_BYTES', 64):
            result = self.call('POST', '/v1/query', {'sql': 'SELECT count(*) FROM messages'}, self.owner, status=400)
            self.assertEqual(result['error'], 'snapshot_too_large_narrow_scope')
        self.assertEqual(self.read_query(sql='SELECT count(*) FROM messages')['rows'], [[1]])


if __name__ == '__main__':
    unittest.main()

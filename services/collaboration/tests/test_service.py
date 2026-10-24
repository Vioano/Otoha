import concurrent.futures
import contextlib
import json
import pathlib
import secrets
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
from otoha_server import Server, Store

class ServiceTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.owner_file = self.root / 'owner.json'
        subprocess.run([sys.executable, 'otoha_server.py', 'init', '--state', str(self.root / 'state'), '--url', 'http://127.0.0.1:1', '--credentials', str(self.owner_file), '--min-free-bytes', '0'], check=True, capture_output=True)
        self.store = Store(self.root / 'state')
        self.server = Server(('127.0.0.1', 0), self.store)
        self.url = 'http://127.0.0.1:' + str(self.server.server_port)
        self.store.config['public_url'] = self.url
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.owner = json.loads(self.owner_file.read_text())['token']
        self.op = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.call('POST', '/v1/sessions', {'id': 'owner-session'}, self.owner)

    def tearDown(self):
        self.server.shutdown(); self.server.server_close()
        self.store.db.close(); self.temp.cleanup()

    def call(self, method, path, body=None, token=None, status=200, headers=None):
        hdr = {'Content-Type': 'application/json', **(headers or {})}
        if token: hdr['Authorization'] = 'Bearer ' + token
        req = urllib.request.Request(self.url + path, data=json.dumps(body).encode() if body is not None else None, headers=hdr, method=method)
        try:
            r = self.op.open(req, timeout=8)
        except urllib.error.HTTPError as e:
            r = e
        with r:
            self.assertEqual(r.status, status)
            return json.load(r)

    def member(self, actor, role='member'):
        inv = self.call('POST', '/v1/invites', {'actor': actor, 'grants': {'team': role}}, self.owner)
        token = secrets.token_urlsafe(32)
        payload = {'invitation': inv['invitation'], 'credential': token, 'enrollment_id': actor + '-enroll', 'device': 'device'}
        self.call('POST', '/v1/enroll', payload)
        self.call('POST', '/v1/sessions', {'id': actor + '-session', 'agent': 'codex'}, token)
        return token, inv, payload

    def send(self, token=None, actor='owner', channel='general', identifier='message-1', text='Hello', status=200, **extra):
        session = 'owner-session' if actor == 'owner' else actor + '-session'
        return self.call('POST', '/v1/messages', {'channel': channel, 'session': session, 'client_id': identifier, 'text': text, **extra}, token or self.owner, status)

    def test_auth_invitation_recovery_and_replay(self):
        self.call('GET', '/v1/manifest', status=401)
        alice, inv, payload = self.member('alice')
        recovered = self.call('POST', '/v1/enroll', payload)
        self.assertTrue(recovered['recovered'])
        self.call('POST', '/v1/enroll', {**payload, 'credential': secrets.token_urlsafe(32)}, status=409)
        self.call('GET', '/v1/actors', token=alice, status=403)
        self.call('POST', '/v1/actors/disable', {'actor': 'owner'}, self.owner, status=400)
        self.call('POST', '/v1/actors/disable', {'actor': 'alice'}, self.owner)
        self.call('GET', '/v1/manifest', token=alice, status=401)
        self.call('POST', '/v1/enroll', payload, status=401)

    def test_scoped_reads_sends_and_private_channels(self):
        alice, _, _ = self.member('alice')
        bob, _, _ = self.member('bob', 'reader')
        self.call('POST', '/v1/projects', {'id': 'secret'}, self.owner)
        self.call('POST', '/v1/channels', {'id': 'secret-channel', 'project': 'secret'}, self.owner)
        self.send(channel='secret-channel')
        self.call('GET', '/v1/messages?channel=secret-channel', token=alice, status=403)
        self.send(token=bob, actor='bob', status=403)
        self.call('POST', '/v1/channels', {'id': 'alice-private', 'project': 'team', 'members': ['alice']}, alice)
        self.send(token=alice, actor='alice', channel='alice-private')
        self.call('GET', '/v1/messages?channel=alice-private', token=bob, status=403)
        self.call('GET', '/v1/messages?channel=alice-private', token=self.owner)
        events = self.call('GET', '/v1/events?after=0&wait=0', token=bob)
        self.assertFalse(any(e['channel'] in ('secret-channel', 'alice-private') for e in events['events']))
        self.call('POST', '/v1/grants', {'actor': 'alice', 'project': 'team', 'role': None}, self.owner)
        self.call('GET', '/v1/messages?channel=general', token=alice, status=403)

    def test_idempotency_concurrency_pagination_and_refs(self):
        one = self.send()
        two = self.send()
        self.assertTrue(two['duplicate']); self.assertEqual(one['message_id'], two['message_id'])
        self.send(text='Changed', status=409)
        ref = {'repo': 'team/game', 'commit': 'a' * 40, 'path': 'assets/scene.blend'}
        self.send(identifier='ref-ok', refs=[ref])
        self.assertIn('refs_normalized', self.send(identifier='ref-bad', refs=[{**ref, 'commit': 'main'}])['warnings'])
        self.assertIn('refs_normalized', self.send(identifier='path-bad', refs=[{**ref, 'path': '../private'}])['warnings'])
        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as ex:
            results = list(ex.map(lambda i: self.send(identifier='parallel-' + str(i)), range(12)))
        self.assertEqual(len(set(x['message_id'] for x in results)), 12)
        page = self.call('GET', '/v1/messages?channel=general&limit=3', token=self.owner)
        self.assertTrue(page['has_more'])
        previous = self.call('GET', f"/v1/messages?channel=general&limit=20&before={page['before']}", token=self.owner)
        self.assertEqual(len(page['messages']) + len(previous['messages']), 16)

    def test_realtime_receipts_and_stream_revocation(self):
        alice, _, _ = self.member('alice')
        cursor = self.call('GET', '/v1/events?after=0&wait=0', token=alice)['cursor']
        with concurrent.futures.ThreadPoolExecutor() as ex:
            waiting = ex.submit(self.call, 'GET', f'/v1/events?after={cursor}&wait=5', None, alice)
            time.sleep(.1)
            sent = self.send(identifier='realtime')
            page = waiting.result(timeout=3)
        self.assertTrue(any(e['payload'].get('id') == sent['message_id'] for e in page['events']))
        receipt = self.call('POST', '/v1/receipts', {'channel': 'general', 'session': 'alice-session', 'received': sent['message_id'], 'read': 0}, alice)
        self.assertEqual(receipt['read'], 0)
        self.call('POST', '/v1/receipts', {'channel': 'general', 'session': 'alice-session', 'received': sent['message_id'], 'read': sent['message_id']}, alice)
        self.call('POST', '/v1/receipts', {'channel': 'general', 'session': 'alice-session', 'received': 0, 'read': 0}, alice)
        self.assertEqual(self.call('GET', '/v1/receipts?channel=general', token=self.owner)['receipts'][0]['read'], sent['message_id'])
        req = urllib.request.Request(self.url + f"/v1/events?after={page['cursor']}", headers={'Accept': 'text/event-stream', 'Authorization': 'Bearer ' + alice})
        stream = self.op.open(req, timeout=5)
        self.call('POST', '/v1/credentials/revoke', {'actor': 'alice', 'device': 'device'}, self.owner)
        observed = []
        with stream:
            while True:
                line = stream.readline().decode()
                if not line: break
                observed.append(line)
                if 'unauthorized' in line: break
        self.assertIn('unauthorized', ''.join(observed))

    def test_storage_guard_origin_and_restart_history(self):
        self.call('POST', '/v1/projects', {'id': 'evil'}, self.owner, status=403, headers={'Origin': 'https://untrusted.example'})
        self.send()
        reopened = Store(self.root / 'state')
        self.assertEqual(reopened.row('SELECT count(*) n FROM messages')['n'], 1)
        reopened.db.close()
        backup = self.root / 'backup.sqlite3'
        subprocess.run([sys.executable, 'otoha_server.py', 'backup', '--state', str(self.root / 'state'), '--out', str(backup)], check=True, capture_output=True)
        import sqlite3
        db = sqlite3.connect(backup)
        self.assertEqual(db.execute('SELECT count(*) FROM messages').fetchone()[0], 1)
        self.assertEqual(db.execute('PRAGMA integrity_check').fetchone()[0], 'ok')
        db.close()
        self.store.config['mount'] = str(self.root / 'unmounted')
        self.call('GET', '/health', status=503)
        self.call('POST', '/v1/messages', {'text': 'no write'}, self.owner, status=503)
        self.assertEqual(self.store.row('SELECT count(*) n FROM messages')['n'], 1)

    def test_mcp_stdio_request_result_and_dual_delivery(self):
        profile = json.loads(self.owner_file.read_text()); profile['url'] = self.url
        self.owner_file.write_text(json.dumps(profile))
        process = subprocess.Popen([sys.executable, 'otoha_mcp.py', '--profile', str(self.owner_file), '--session', 'mcp-test'], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        def request(value):
            process.stdin.write(json.dumps(value) + '\n'); process.stdin.flush()
            if 'id' in value:
                return json.loads(process.stdout.readline())
        try:
            init = request({'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2025-11-25','clientInfo':{'name':'test','version':'1'},'capabilities':{}}})
            self.assertIn('tools', init['result']['capabilities'])
            request({'jsonrpc':'2.0','method':'notifications/initialized'})
            tools = request({'jsonrpc':'2.0','id':2,'method':'tools/list'})['result']['tools']
            self.assertIn('agent_request', [x['name'] for x in tools])
            call = lambda identifier,name,arguments: request({'jsonrpc':'2.0','id':identifier,'method':'tools/call','params':{'name':name,'arguments':arguments}})
            manifest = call(3,'manifest',{})
            self.assertFalse(manifest['result']['isError'])
            self.assertEqual(manifest['result']['structuredContent']['actor']['id'], 'owner')
            result = call(4,'agent_request',{'session':'mcp-test','channel':'general','target':'owner/owner-session','text':'Report one result','client_id':'mcp-request-1','delivery':'both'})['result']['structuredContent']
            self.send(identifier='mcp-result-1', kind='result', reply_to=result['message_id'], text='Result with evidence')
            waited = call(5,'agent_wait',{'channel':'general','message_id':result['message_id'],'after':result['event_id'],'wait':1})['result']['structuredContent']
            self.assertFalse(waited['pending'])
            self.assertEqual(waited['event']['payload']['payload']['text'], 'Result with evidence')
            history = self.call('GET','/v1/messages?channel=general',token=self.owner)
            self.assertEqual(history['messages'][0]['payload']['delivery'], 'both')
            self.assertEqual(len(history['messages']), 2)
            other = call(6,'send',{'session':'another-conversation','channel':'general','text':'A different conversation','client_id':'mcp-other-1'})
            self.assertFalse(other['result']['isError'])
            history = self.call('GET','/v1/messages?channel=general',token=self.owner)
            self.assertEqual(history['messages'][-1]['session'], 'another-conversation')
        finally:
            process.stdin.close(); process.wait(timeout=5)
            process.stdout.close(); process.stderr.close()

    def test_chat_bridge_sender_policy_deduplication_and_uncertain_delivery(self):
        from otoha_codex_bridge import Bridge
        from otoha import write_private
        profile = json.loads(self.owner_file.read_text()); profile['url'] = self.url
        self.owner_file.write_text(json.dumps(profile))
        self.call('POST', '/v1/sessions', {'id': 'bridge-test'}, self.owner)
        allowed = 'owner/bridge-test'
        alice, _, _ = self.member('alice')
        self.send(identifier='bridge-tool', delivery='tool', to_sessions=[allowed])
        self.send(identifier='bridge-broadcast', delivery='both')
        self.send(token=alice, actor='alice', identifier='bridge-untrusted', delivery='both', to_sessions=[allowed])
        expected = self.send(identifier='bridge-allowed', delivery='both', to_sessions=[allowed])
        recorded = []
        class Probe(Bridge):
            def queue(self, message):
                recorded.append(message['id'])
                return {'mechanism_probe': True}
        cursor = self.root / 'bridge.cursor.json'
        bridge = Probe(self.owner_file, 'bridge-test', ['owner'], cursor, sys.executable)
        self.assertEqual(bridge.step(0)['queued'], [expected['message_id']])
        self.assertEqual(recorded, [expected['message_id']])
        self.assertEqual(self.call('GET', '/v1/receipts?channel=general', token=self.owner)['receipts'], [])
        write_private(cursor, {'scope': bridge.scope, 'cursor': 0}, replace=True)
        replay = Probe(self.owner_file, 'bridge-test', ['owner'], cursor, sys.executable)
        replay.step(0)
        self.assertEqual(recorded, [expected['message_id']])
        failed = self.send(identifier='bridge-unknown', delivery='chat', to_sessions=[allowed])
        def unknown(message): raise TimeoutError('Unknown runtime outcome')
        replay.queue = unknown
        with self.assertRaises(SystemExit): replay.step(0)
        journal = json.loads((replay.journal / (str(failed['message_id']) + '.json')).read_text())
        self.assertEqual(journal['status'], 'uncertain')
        with self.assertRaises(SystemExit): replay.step(0)

if __name__ == '__main__':
    unittest.main()

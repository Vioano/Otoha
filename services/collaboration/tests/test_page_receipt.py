"""HTTP checks for recovering a sender's accepted operation after disconnection."""
import json
import pathlib
import secrets
import subprocess
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
from otoha_server import Server, Store


class MessageLookupTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        credentials = self.root / 'owner.json'
        script = pathlib.Path(__file__).resolve().parents[1] / 'otoha_server.py'
        subprocess.run([sys.executable, str(script), 'init', '--state', str(self.root / 'state'),
                        '--url', 'http://127.0.0.1:1', '--credentials', str(credentials),
                        '--min-free-bytes', '0'], check=True, capture_output=True)
        self.owner = json.loads(credentials.read_text())['token']
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.start_server()

    def start_server(self):
        self.store = Store(self.root / 'state')
        self.server = Server(('127.0.0.1', 0), self.store)
        self.url = 'http://127.0.0.1:' + str(self.server.server_port)
        self.store.config['public_url'] = self.url
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def stop_server(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        self.store.db.close()

    def tearDown(self):
        self.stop_server()
        self.temp.cleanup()

    def call(self, method, path, body=None, token=None, status=200):
        headers = {'Content-Type': 'application/json'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        request = urllib.request.Request(self.url + path, method=method, headers=headers,
                                         data=json.dumps(body).encode() if body is not None else None)
        try:
            response = self.opener.open(request, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            self.assertEqual(response.status, status)
            return json.load(response)

    def enroll(self, actor):
        invitation = self.call('POST', '/v1/invites',
                               {'actor': actor, 'grants': {'team': 'member'}}, self.owner)
        token = secrets.token_urlsafe(32)
        self.call('POST', '/v1/enroll', {'invitation': invitation['invitation'],
                                       'credential': token, 'enrollment_id': actor + '-enrollment',
                                       'device': actor + '-device'})
        return token

    def test_lookup_recovers_original_payload_after_restart_without_duplicate_or_read(self):
        alice = self.enroll('alice')
        path = '/v1/messages/by-client-id?client_id=page-publish-1'
        self.call('GET', path, token=alice, status=404)
        document = {'schema': 'otoha-page/1', 'operation': 'publish',
                    'document': {'title': 'Synthetic work', 'items': []}}
        body = {'channel': 'general', 'session': 'page-session', 'client_id': 'page-publish-1',
                'uid': 'page-publish-1', 'summary': 'Publish work page', 'kind': 'status',
                'body': json.dumps(document), 'text': json.dumps(document), 'source': 'agent'}
        self.call('POST', '/v1/messages', body, alice)
        # The caller retains no send result. Reopen the durable server and
        # recover by the ID written before sending, as after a lost response.
        self.stop_server()
        self.start_server()
        recovered = self.call('GET', path, token=alice)
        self.assertTrue(recovered['duplicate'])
        self.assertEqual(recovered['client_id'], body['client_id'])
        self.assertEqual(recovered['uid'], body['uid'])
        self.assertEqual(recovered['message']['actor'], 'alice')
        self.assertEqual(recovered['message']['channel'], 'general')
        self.assertEqual(recovered['message']['session'], body['session'])
        self.assertEqual(recovered['message']['payload']['body'], body['body'])
        duplicate = self.call('POST', '/v1/messages', body, alice)
        self.assertEqual(duplicate['message_id'], recovered['message_id'])
        self.assertTrue(duplicate['duplicate'])
        self.assertEqual(self.store.row('SELECT count(*) n FROM messages')['n'], 1)
        self.assertEqual(self.store.row("SELECT count(*) n FROM events WHERE kind='message.created'")['n'], 1)
        receipts = self.call('GET', '/v1/receipts?channel=general', token=alice)
        self.assertEqual(receipts['receipts'], [])
        self.assertEqual(receipts['message_receipts'], [])

    def test_lookup_is_actor_scoped_and_rechecks_revoked_channel_access(self):
        alice, bob = self.enroll('alice'), self.enroll('bob')
        body = {'channel': 'general', 'session': 'page-session', 'client_id': 'shared-id', 'text': 'Alice private operation'}
        sent = self.call('POST', '/v1/messages', body, alice)
        path = '/v1/messages/by-client-id?client_id=shared-id'
        self.call('GET', path, token=bob, status=404)
        self.call('GET', path, token=self.owner, status=404)
        bobs = self.call('POST', '/v1/messages', {**body, 'text': 'Bob operation'}, bob)
        self.assertNotEqual(bobs['message_id'], sent['message_id'])
        self.assertEqual(self.call('GET', path, token=bob)['message']['payload']['body'], 'Bob operation')
        self.call('POST', '/v1/grants', {'actor': 'alice', 'project': 'team', 'role': None}, self.owner)
        self.call('GET', path, token=alice, status=403)
        self.assertEqual(self.call('GET', path, token=bob)['message_id'], bobs['message_id'])

    def test_lookup_requires_auth_and_valid_identifier(self):
        self.call('GET', '/v1/messages/by-client-id?client_id=valid-id', status=401)
        for suffix in ['', '?client_id=', '?client_id=has%20space', '?client_id=bad%2Fid', '?client_id=' + 'x' * 81]:
            self.call('GET', '/v1/messages/by-client-id' + suffix, token=self.owner, status=400)


if __name__ == '__main__':
    unittest.main()

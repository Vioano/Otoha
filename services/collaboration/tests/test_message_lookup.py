"""Authorized original-message retrieval is independent of local cache windows."""
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


class MessageOriginalLookupTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        credentials = self.root / 'owner.json'
        script = pathlib.Path(__file__).resolve().parents[1] / 'otoha_server.py'
        subprocess.run([sys.executable, str(script), 'init', '--state', str(self.root / 'state'),
                        '--url', 'http://127.0.0.1:1', '--credentials', str(credentials),
                        '--min-free-bytes', '0'], check=True, capture_output=True)
        self.owner = json.loads(credentials.read_text())['token']
        self.store = Store(self.root / 'state')
        self.server = Server(('127.0.0.1', 0), self.store)
        self.url = 'http://127.0.0.1:' + str(self.server.server_port)
        self.store.config['public_url'] = self.url
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        self.store.db.close()
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

    def send(self, token, channel, text, identifier):
        return self.call('POST', '/v1/messages',
                         {'channel': channel, 'client_id': identifier,
                          'summary': 'Short notification', 'body': text, 'text': text}, token)

    def test_original_outside_history_window_is_returned_without_read_receipts(self):
        alice = self.enroll('alice')
        original = 'Full original\n中文 😀\nThe stored body remains exact.'
        first = self.send(alice, 'general', original, 'original-message')
        for i in range(101):
            self.send(alice, 'general', 'Later message ' + str(i), 'later-' + str(i))
        recent = self.call('GET', '/v1/messages?channel=general&limit=100', token=alice)
        self.assertTrue(recent['has_more'])
        self.assertNotIn(first['message_id'], [row['id'] for row in recent['messages']])
        result = self.call('GET', '/v1/messages/by-id?id=' + str(first['message_id']) + '&channel=general', token=alice)
        self.assertEqual(result['message_id'], first['message_id'])
        self.assertEqual(result['message']['payload']['body'], original)
        self.assertEqual(result['message']['payload']['summary'], 'Short notification')
        self.assertEqual(self.store.row('SELECT count(*) n FROM receipts')['n'], 0)
        self.assertEqual(self.store.row('SELECT count(*) n FROM message_receipts')['n'], 0)

    def test_original_lookup_checks_private_channel_and_current_project_grant(self):
        alice, bob = self.enroll('alice'), self.enroll('bob')
        self.call('POST', '/v1/channels', {'id': 'alice-private', 'project': 'team',
                                         'members': ['alice']}, self.owner)
        sent = self.send(alice, 'alice-private', 'Private synthetic content', 'private-message')
        path = '/v1/messages/by-id?id=' + str(sent['message_id'])
        self.assertEqual(self.call('GET', path, token=alice)['message']['payload']['body'], 'Private synthetic content')
        self.call('GET', path, token=bob, status=403)
        self.assertEqual(self.call('GET', path, token=self.owner)['message_id'], sent['message_id'])
        self.call('GET', path + '&channel=general', token=alice, status=404)
        self.call('POST', '/v1/grants', {'actor': 'alice', 'project': 'team', 'role': None}, self.owner)
        self.call('GET', path, token=alice, status=403)

    def test_original_lookup_requires_auth_and_valid_positive_id(self):
        self.call('GET', '/v1/messages/by-id?id=1', status=401)
        for suffix in ['', '?id=', '?id=0', '?id=-1', '?id=01', '?id=1.5', '?id=bad',
                       '?id=9223372036854775808', '?id=' + '9' * 30]:
            result = self.call('GET', '/v1/messages/by-id' + suffix, token=self.owner, status=400)
            self.assertEqual(result['error'], 'invalid_message_id')
        self.call('GET', '/v1/messages/by-id?id=999999', token=self.owner, status=404)


if __name__ == '__main__':
    unittest.main()

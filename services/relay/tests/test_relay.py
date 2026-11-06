import concurrent.futures
import http.client
import json
import pathlib
import secrets
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
from otoha_relay import Error, MAX_BODY, SCHEMA, Server, Store, private_file


class RelayFixture(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='otoha-relay-test-')
        self.addCleanup(self.temporary.cleanup)
        self.state = pathlib.Path(self.temporary.name)
        self.state.chmod(0o700)
        self.now = [1000]
        private_file(self.state / 'relay.json', {
            'public_url': 'https://relay.example.invalid',
            'server_addr': 'relay.example.invalid', 'server_port': 443,
            'server_name': 'relay.example.invalid', 'protocol': 'wss',
            'frp_version': '0.72.0', 'max_active_routes': 64,
            'max_retained_routes': 4096, 'route_ttl_seconds': 60,
        })
        self.store = Store(self.state, clock=lambda: self.now[0])

    def request(self, identifier='setup-a', label='Example team'):
        return {'request_id': identifier, 'enrollment_secret': secrets.token_urlsafe(32), 'label': label}

    def assert_error(self, status, code, callback):
        with self.assertRaises(Error) as result:
            callback()
        self.assertEqual((result.exception.status, result.exception.code), (status, code))

    def count(self):
        with self.store.database() as db:
            return db.execute('SELECT count(*) FROM routes').fetchone()[0]

    def login(self, route):
        return {'content': {'user': route['id'], 'metas': {'otoha_token': route['token']},
                            'version': '0.72.0', 'hostname': 'example-host'}}

    def proxy(self, route):
        return {'content': {'user': {'user': route['id'], 'metas': {'otoha_token': route['token']}},
                            'proxy_name': route['id'] + '.otoha-center', 'proxy_type': 'http',
                            'custom_domains': [route['custom_domain']], 'locations': [route['location']],
                            'remote_port': 0, 'group': '', 'group_key': '', 'subdomain': ''}}


class RelayTest(RelayFixture):
    def test_registration_survives_restart_without_plaintext_capabilities(self):
        request = self.request()
        first = self.store.register(request)
        self.assertFalse(first['recovered'])
        route = first['route']
        self.assertRegex(route['id'], r'^r[0-9a-f]{24}$')
        self.assertEqual(route['location'], '/t/' + route['id'] + '/')
        self.assertEqual(route['public_url'], 'https://relay.example.invalid/t/' + route['id'])
        self.assertNotEqual(route['token'], request['enrollment_secret'])
        reopened = Store(self.state, clock=lambda: self.now[0])
        self.now[0] += 10
        recovered = reopened.register(request)
        self.assertTrue(recovered['recovered'])
        self.assertEqual(recovered['route'], route)
        self.assertEqual(self.count(), 1)
        for filename in self.state.iterdir():
            if filename.is_file():
                data = filename.read_bytes()
                self.assertNotIn(request['enrollment_secret'].encode(), data)
                self.assertNotIn(route['token'].encode(), data)
        status = reopened.manage(route['id'], route['token'])
        self.assertNotIn('token', status['route'])
        self.assertNotIn('label', status['route'])

    def test_registration_conflicts_preserve_original_route(self):
        request = self.request()
        route = self.store.register(request)['route']
        for changed in [dict(request, label='Another purpose'),
                        dict(request, enrollment_secret=secrets.token_urlsafe(32)),
                        dict(request, request_id='different-request')]:
            self.assert_error(409, 'request_conflict', lambda: self.store.register(changed))
        self.assertEqual(self.count(), 1)
        self.assertEqual(self.store.register(request)['route'], route)

    def test_parallel_same_request_creates_one_durable_route(self):
        request = self.request()
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            responses = list(executor.map(lambda _x: self.store.register(request), range(16)))
        self.assertEqual(len({response['route']['id'] for response in responses}), 1)
        self.assertEqual(sum(not response['recovered'] for response in responses), 1)
        self.assertEqual(self.count(), 1)

    def test_capacity_expiration_renewal_and_revoke_keep_route_identity(self):
        self.store.config['max_active_routes'] = 1
        original_request = self.request()
        original = self.store.register(original_request)['route']
        self.assert_error(429, 'relay_capacity_reached', lambda: self.store.register(self.request('setup-b')))
        self.now[0] = original['expires_at'] + 1
        self.assertTrue(self.store.plugin('Login', self.login(original))['reject'])
        expired = self.store.register(original_request)['route']
        self.assertEqual(expired['state'], 'expired')
        self.assertEqual(expired['token'], original['token'])
        replacement = self.store.register(self.request('setup-b'))['route']
        self.assert_error(429, 'relay_capacity_reached', lambda: self.store.manage(original['id'], original['token'], 'renew'))
        revoked = self.store.manage(replacement['id'], replacement['token'], 'revoke')
        self.assertEqual(revoked['route']['state'], 'revoked')
        renewed = self.store.manage(original['id'], original['token'], 'renew')['route']
        self.assertEqual(renewed['id'], original['id'])
        self.assertEqual(renewed['state'], 'active')
        self.assertEqual(renewed['expires_at'], self.now[0] + 60)
        self.store.manage(original['id'], original['token'], 'revoke')
        self.assert_error(403, 'route_revoked', lambda: self.store.manage(original['id'], original['token'], 'renew'))
        self.assertEqual(self.store.register(original_request)['route']['state'], 'revoked')
        self.assertEqual(self.store.manage(original['id'], original['token'], 'revoke')['route']['state'], 'revoked')
        self.assertTrue(self.store.plugin('NewProxy', self.proxy(original))['reject'])

    def test_retained_route_limit_does_not_prevent_recovery(self):
        self.store.config['max_retained_routes'] = 1
        request = self.request()
        route = self.store.register(request)['route']
        self.store.manage(route['id'], route['token'], 'revoke')
        self.assert_error(429, 'relay_registration_limit_reached', lambda: self.store.register(self.request('setup-b')))
        self.assertEqual(self.store.register(request)['route']['id'], route['id'])

    def test_plugin_allows_only_own_fixed_http_scope(self):
        route = self.store.register(self.request())['route']
        other = self.store.register(self.request('setup-b'))['route']
        self.assertEqual(self.store.plugin('Login', self.login(route)), {'reject': False, 'unchange': True})
        self.assertEqual(self.store.plugin('NewProxy', self.proxy(route)), {'reject': False, 'unchange': True})
        attacks = [
            {'proxy_name': other['id'] + '.otoha-center'},
            {'proxy_name': route['id'] + '.arbitrary-service'},
            {'proxy_type': 'tcp', 'remote_port': 22},
            {'custom_domains': ['other.example.invalid']},
            {'custom_domains': [route['custom_domain'], 'other.example.invalid']},
            {'locations': ['/']},
            {'locations': [other['location']]},
            {'locations': [route['location'], '/relay/v1/']},
            {'locations': [route['location'].rstrip('/')]},
            {'remote_port': 1234}, {'subdomain': 'other'}, {'group': 'another-team'},
            {'group_key': 'group-secret'}, {'sk': 'secret'}, {'multiplexer': 'httpconnect'},
        ]
        for replacement in attacks:
            with self.subTest(replacement=replacement):
                request = self.proxy(route)
                request['content'].update(replacement)
                self.assertTrue(self.store.plugin('NewProxy', request)['reject'])
        for operation in ('Login', 'NewProxy'):
            request = self.login(route) if operation == 'Login' else self.proxy(route)
            user = request['content'] if operation == 'Login' else request['content']['user']
            user['metas']['otoha_token'] = other['token']
            self.assertTrue(self.store.plugin(operation, request)['reject'])
        self.assert_error(401, 'route_unauthorized', lambda: self.store.manage(route['id'], other['token']))

    def test_bad_secrets_and_labels_are_rejected_before_allocation(self):
        request = self.request()
        for secret in ['', 'short', secrets.token_urlsafe(31), 'A' * 42 + 'B']:
            self.assert_error(400, 'invalid_enrollment_secret', lambda: self.store.register(dict(request, enrollment_secret=secret)))
        for label in ['', ' ', 'x' * 121, 'あ' * 120, 'team\nother']:
            self.assert_error(400, 'invalid_label', lambda: self.store.register(dict(request, label=label)))
        self.assertEqual(self.count(), 0)


class RelayHTTPTest(RelayFixture):
    def setUp(self):
        super().setUp()
        self.server = Server(('127.0.0.1', 0), self.store)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.stop)
        self.url = 'http://127.0.0.1:' + str(self.server.server_port)

    def stop(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def call(self, method, path, body=None, token=None):
        headers = {}
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers['Content-Type'] = 'application/json'
        if token:
            headers['Authorization'] = 'Bearer ' + token
        request = urllib.request.Request(self.url + path, data=data, headers=headers, method=method)
        try:
            response = urllib.request.urlopen(request, timeout=3)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, json.loads(response.read())

    def test_real_frp_query_operation_and_empty_renew_post(self):
        status, created = self.call('POST', '/relay/v1/routes', self.request())
        self.assertEqual(status, 200)
        route = created['route']
        status, result = self.call('POST', '/frp?version=0.1.0&op=Login', self.login(route))
        self.assertEqual((status, result), (200, {'reject': False, 'unchange': True}))
        status, result = self.call('POST', '/frp?version=0.1.0&op=NewProxy', self.proxy(route))
        self.assertEqual((status, result), (200, {'reject': False, 'unchange': True}))
        mismatch = dict(self.login(route), op='NewProxy')
        status, result = self.call('POST', '/frp?version=0.1.0&op=Login', mismatch)
        self.assertEqual(status, 200)
        self.assertTrue(result['reject'])
        status, renewed = self.call('POST', '/relay/v1/routes/' + route['id'] + '/renew', token=route['token'])
        self.assertEqual(status, 200)
        self.assertEqual(renewed['route']['id'], route['id'])
        self.assertNotIn('token', renewed['route'])

    def test_http_limits_errors_and_public_manifest(self):
        status, manifest = self.call('GET', '/relay/v1/manifest')
        self.assertEqual(status, 200)
        self.assertEqual(manifest['schema'], SCHEMA)
        self.assertEqual(manifest['limits']['active_routes'], 64)
        self.assertNotIn('token', json.dumps(manifest))
        status, error = self.call('POST', '/relay/v1/routes', {'unknown': 'x'})
        self.assertEqual((status, error['error']), (400, 'invalid_registration_fields'))
        status, error = self.call('POST', '/relay/v1/routes', {'oversized': 'x' * MAX_BODY})
        self.assertEqual((status, error['error']), (413, 'request_too_large'))
        connection = http.client.HTTPConnection('127.0.0.1', self.server.server_port, timeout=3)
        self.addCleanup(connection.close)
        connection.request('POST', '/relay/v1/routes', body=b'{"label":NaN}', headers={'Content-Type': 'application/json'})
        response = connection.getresponse()
        self.assertEqual(response.status, 400)
        self.assertEqual(json.loads(response.read())['error'], 'invalid_json')


if __name__ == '__main__':
    unittest.main()

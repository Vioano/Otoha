"""Real HTTP regression coverage for a project authority's public URL prefix.

Only isolated local HTTP services are used. The graph backend is substituted;
the Git upstream returns a fixed advertisement and has no real repository.
"""
import http.client
import http.server
import json
import secrets
import threading
import unittest
from unittest.mock import Mock, patch

import test_service as fixture
import otoha_server


class AdvertisementHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        self.server.observed.append((self.path, self.headers.get('Authorization')))
        content = b'001e# service=git-upload-pack\n0000'
        self.send_response(200)
        self.send_header('Content-Type', 'application/x-git-upload-pack-advertisement')
        self.send_header('Content-Length', str(len(content)))
        self.end_headers()
        self.wfile.write(content)


class PrefixTest(unittest.TestCase):
    call = fixture.ServiceTest.call

    def setUp(self):
        fixture.ServiceTest.setUp(self)
        self.addCleanup(fixture.ServiceTest.tearDown, self)
        self.prefix = '/t/fixture-route-42'
        self.public_url = 'https://otoha.example.invalid' + self.prefix
        self.store.config['public_url'] = self.public_url

    def prefixed(self, method, path, body=None, token=None, status=200, headers=None):
        return self.call(method, self.prefix + path, body, token, status, headers)

    def raw_get(self, path, token=None, status=200, headers=None):
        connection = http.client.HTTPConnection('127.0.0.1', self.server.server_port, timeout=3)
        try:
            request_headers = dict(headers or {})
            if token:
                request_headers['Authorization'] = 'Bearer ' + token
            connection.request('GET', path, headers=request_headers)
            response = connection.getresponse()
            content = response.read()
            self.assertEqual(response.status, status, (path, content))
            return content, response.getheader('Content-Type')
        finally:
            connection.close()

    def test_manifest_authentication_public_base_and_loopback_management(self):
        self.prefixed('GET', '/v1/manifest', status=401)
        manifest = self.prefixed('GET', '/v1/manifest', token=self.owner)
        self.assertEqual(manifest['url'], self.public_url)
        self.assertEqual(manifest['actor']['id'], 'owner')
        self.assertEqual(self.prefixed('GET', '/health')['status'], 'ok')
        # Existing loopback administrators retain the root API, still requiring
        # authentication. The public authority locator remains the full prefix.
        self.call('GET', '/v1/manifest', status=401)
        root_manifest = self.call('GET', '/v1/manifest', token=self.owner)
        self.assertEqual(root_manifest['url'], self.public_url)

    def test_invitation_discovery_enrollment_and_member_authentication(self):
        invitation = self.prefixed('POST', '/v1/invites',
                                   {'actor': 'new-member', 'grants': {'team': 'member'}}, self.owner)
        self.assertEqual(invitation['url'], self.public_url)
        info = self.prefixed('POST', '/v1/invitation-info',
                             {'invitation': invitation['invitation']})
        self.assertEqual((info['url'], info['actor'], info['project'], info['channel']),
                         (self.public_url, 'new-member', 'team', 'general'))
        self.assertNotIn('credential', info)
        credential = secrets.token_urlsafe(32)
        self.prefixed('POST', '/v1/enroll', {'invitation': invitation['invitation'],
                                           'credential': credential, 'device': 'prefix-device',
                                           'enrollment_id': 'prefix-enrollment'})
        manifest = self.prefixed('GET', '/v1/manifest', token=credential)
        self.assertEqual((manifest['actor']['id'], manifest['url']), ('new-member', self.public_url))
        self.assertEqual(manifest['grants'], [{'project': 'team', 'role': 'member'}])
        self.prefixed('POST', '/v1/invitation-info',
                      {'invitation': invitation['invitation']}, status=409)

    def test_wrong_prefix_and_noncanonical_paths_are_not_normalized(self):
        # The bare public mount is not an API endpoint. It may reach the
        # service's unknown-route handler, but must still return 404.
        self.raw_get(self.prefix, self.owner, status=404)
        paths = [
            self.prefix + 'evil/v1/manifest',
            '/t/another-route/v1/manifest',
            self.prefix + '%2Fv1/manifest',
            self.prefix + '/v1%2fmanifest',
            self.prefix + '//v1/manifest',
            self.prefix + '/./v1/manifest',
            self.prefix + '/v1/../v1/manifest',
            self.prefix + '/%2e/v1/manifest',
            self.prefix + '/%2E%2E/v1/manifest',
            self.prefix + '/%252Fv1/manifest',
        ]
        for path in paths:
            with self.subTest(path=path):
                content, _ = self.raw_get(path, self.owner, status=404)
                self.assertEqual(json.loads(content)['error'], 'api_route_not_found')

    def test_peer_gate_uses_actual_loopback_address(self):
        normalize = otoha_server.normalize_api_path
        for peer in ('127.0.0.1', '::1'):
            self.assertEqual(normalize('/v1/manifest', self.public_url, peer), '/v1/manifest')
        for peer in ('198.51.100.20', '2001:db8::20'):
            with self.subTest(peer=peer):
                self.assertEqual(normalize(self.prefix + '/v1/manifest', self.public_url, peer),
                                 '/v1/manifest')
                with self.assertRaises(otoha_server.Error) as rejected:
                    normalize('/v1/manifest', self.public_url, peer)
                self.assertEqual((rejected.exception.status, rejected.exception.code),
                                 (404, 'api_route_not_found'))
        # Caller headers are not an argument to the peer gate. Even a public
        # Host / forwarding header cannot remove actual loopback management.
        content, _ = self.raw_get('/v1/manifest', self.owner, headers={
            'Host': 'otoha.example.invalid', 'X-Forwarded-For': '198.51.100.20'})
        self.assertEqual(json.loads(content)['actor']['id'], 'owner')

    def test_prefixed_events_query_and_sse_keep_authentication(self):
        self.prefixed('GET', '/v1/events?after=0&wait=0&channel=general', status=401)
        sent = self.prefixed('POST', '/v1/messages', {
            'channel': 'general', 'session': 'owner-session', 'client_id': 'prefix-event',
            'text': 'SSE_PREFIX_SENTINEL', 'summary': 'Prefix event', 'body': 'SSE_PREFIX_SENTINEL',
        }, self.owner)
        events = self.prefixed('GET', '/v1/events?after=0&wait=0&channel=general', token=self.owner)
        self.assertTrue(any(event['id'] == sent['event_id'] for event in events['events']))
        self.prefixed('GET', '/v1/events?after=0&wait=0', status=401,
                      headers={'Accept': 'text/event-stream'})
        stream_token = secrets.token_urlsafe(32)
        self.prefixed('POST', '/v1/credentials', {'device': 'prefix-sse', 'credential': stream_token}, self.owner)
        connection = http.client.HTTPConnection('127.0.0.1', self.server.server_port, timeout=3)
        response = None
        try:
            connection.request('GET', self.prefix + '/v1/events?after=0&channel=general', headers={
                'Authorization': 'Bearer ' + stream_token, 'Accept': 'text/event-stream'})
            response = connection.getresponse()
            self.assertEqual(response.status, 200)
            self.assertEqual(response.getheader('Content-Type'), 'text/event-stream')
            lines = []
            for _ in range(30):
                line = response.readline()
                lines.append(line)
                if b'SSE_PREFIX_SENTINEL' in line or not line:
                    break
            self.assertIn(b'event: message.created\n', lines)
            self.assertTrue(any(b'SSE_PREFIX_SENTINEL' in line for line in lines))
            # Finish the actual stream before its temporary store is removed;
            # just closing the client can leave the polling worker alive.
            self.prefixed('POST', '/v1/credentials/revoke', {'device': 'prefix-sse'}, self.owner)
            ending = response.read()
            self.assertIn(b'event: error\n', ending)
            self.assertIn(b'unauthorized', ending)
        finally:
            if response is not None:
                response.close()
            connection.close()
        # Encoded query values remain query data, while encoded path delimiters
        # are rejected. General's encoded spelling still resolves the channel.
        history = self.prefixed('GET', '/v1/messages?channel=%67eneral', token=self.owner)
        self.assertEqual(history['messages'][0]['payload']['body'], 'SSE_PREFIX_SENTINEL')

    def test_prefixed_graph_routes_use_existing_authentication(self):
        config = {'url': 'http://127.0.0.1:1', 'username': 'fixture', 'password': 'FIXTURE_ONLY',
                  'namespace': 'fixture',
                  'projects': {'team': {'database': 'team_data', 'graph_id': 'team-graph'}}}
        path = self.root / 'state' / 'graph.json'
        path.write_text(json.dumps(config))
        path.chmod(0o600)
        backend = Mock()
        response = {'schema': 'otoha-graph/1', 'graph_id': 'team-graph', 'source_version': 0,
                    'scope': {'project': 'team'}, 'complete': True, 'truncated': False}
        for method in ('manifest', 'query', 'mutate', 'receipt'):
            getattr(backend, method).return_value = response
        query = {'project': 'team', 'mode': 'filter', 'limit': 1}
        mutation = {'project': 'team', 'request_id': 'prefix-request', 'expected_revision': 0,
                    'operations': [{'op': 'node.add', 'id': 'node'}]}
        routes = [('GET', '/v1/graph/manifest?project=team', None),
                  ('POST', '/v1/graph/query', query),
                  ('POST', '/v1/graph/mutations', mutation),
                  ('GET', '/v1/graph/requests/prefix-request?project=team', None)]
        with patch('otoha_graph.GraphBackend', return_value=backend) as constructor:
            for method, route, body in routes:
                self.prefixed(method, route, body, status=401)
            constructor.assert_not_called()
            for method, route, body in routes:
                self.assertEqual(self.prefixed(method, route, body, self.owner)['graph_id'], 'team-graph')
            binding = constructor.call_args.args[0]
            self.assertEqual(binding['project'], 'team')
            backend.manifest.assert_called_once_with()
            backend.query.assert_called_once_with(query)
            backend.mutate.assert_called_once_with('owner', mutation)
            backend.receipt.assert_called_once_with('owner', 'prefix-request')

    def test_repository_metadata_and_smart_http_keep_full_public_base(self):
        upstream = http.server.ThreadingHTTPServer(('127.0.0.1', 0), AdvertisementHandler)
        upstream.observed = []
        thread = threading.Thread(target=upstream.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(upstream.server_close)
        self.addCleanup(upstream.shutdown)
        profile = self.root / 'git-upstream.json'
        profile.write_text(json.dumps({'url': 'http://127.0.0.1:' + str(upstream.server_port),
                                       'username': 'fixture-git', 'password': 'FIXTURE_ONLY'}))
        profile.chmod(0o600)
        (self.root / 'state' / 'repositories.json').write_text(json.dumps({'projects': {
            'team': {'repository': 'test-owner/test-repo', 'profile': str(profile)}}}))
        self.prefixed('GET', '/v1/repository?project=team', status=401)
        info = self.prefixed('GET', '/v1/repository?project=team', token=self.owner)
        self.assertEqual(info['url'], self.public_url + '/v1/git/team/test-owner/test-repo.git')
        git_path = self.prefix + '/v1/git/team/test-owner/test-repo.git/info/refs?service=git-upload-pack'
        self.raw_get(git_path, status=401)
        self.assertEqual(upstream.observed, [])
        content, content_type = self.raw_get(git_path, self.owner)
        self.assertEqual(content_type, 'application/x-git-upload-pack-advertisement')
        self.assertIn(b'# service=git-upload-pack', content)
        self.assertEqual(upstream.observed[0][0], '/test-owner/test-repo.git/info/refs?service=git-upload-pack')
        self.assertTrue(upstream.observed[0][1].startswith('Basic '))


if __name__ == '__main__':
    unittest.main()

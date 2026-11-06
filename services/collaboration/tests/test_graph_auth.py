"""Project graph HTTP authorization with an isolated, substituted backend.

These tests do not connect to SurrealDB or use a real project identity.
"""
import json
import threading
import unittest
from unittest.mock import Mock, patch

import test_service as fixture
from otoha_server import digest


class GraphAuthTest(unittest.TestCase):
    call = fixture.ServiceTest.call
    member = fixture.ServiceTest.member

    def setUp(self):
        fixture.ServiceTest.setUp(self)
        self.addCleanup(fixture.ServiceTest.tearDown, self)
        configuration = {
            'url': 'http://127.0.0.1:1', 'username': 'fixture',
            'password': 'FIXTURE_ONLY', 'namespace': 'fixture',
            'projects': {'team': {'database': 'team_data', 'graph_id': 'team-graph'}},
        }
        config_path = self.root / 'state' / 'graph.json'
        config_path.write_text(json.dumps(configuration))
        config_path.chmod(0o600)
        self.backend_calls = []
        self.backend = Mock()
        self.backend.manifest.side_effect = lambda: self.backend_result('manifest')
        self.backend.query.side_effect = lambda body: self.backend_result('query', body)
        self.backend.mutate.side_effect = lambda actor, body: self.backend_result('mutate', actor, body)
        self.backend.receipt.side_effect = lambda actor, request_id: self.backend_result('receipt', actor, request_id)
        patcher = patch('otoha_graph.GraphBackend', return_value=self.backend)
        self.backend_constructor = patcher.start()
        self.addCleanup(patcher.stop)

    def backend_result(self, operation, *arguments):
        # A different thread must acquire the RLock: re-entering it in the HTTP
        # worker would incorrectly pass even while the worker holds the lock.
        acquired = []

        def check_lock():
            success = self.store.lock.acquire(timeout=.5)
            acquired.append(success)
            if success:
                self.store.lock.release()

        worker = threading.Thread(target=check_lock, daemon=True)
        worker.start()
        worker.join(1)
        self.assertEqual(acquired, [True], 'backend I/O holds the message-store global lock')
        self.backend_calls.append((operation, arguments))
        return {'schema': 'otoha-graph/1', 'graph_id': 'team-graph',
                'source_version': 0, 'scope': {'project': 'team'},
                'complete': True, 'truncated': False}

    def mutation(self, **extra):
        return {'project': 'team', 'request_id': 'request-1', 'expected_revision': 0,
                'operations': [{'op': 'node.add', 'id': 'n1', 'properties': {}}], **extra}

    def read_query(self, **extra):
        return {'project': 'team', 'mode': 'inspect', 'kind': 'node', 'id': 'n1', **extra}

    def test_all_graph_reads_and_writes_require_authentication(self):
        requests = [
            ('GET', '/v1/graph/manifest?project=team', None),
            ('POST', '/v1/graph/query', self.read_query()),
            ('POST', '/v1/graph/mutations', self.mutation()),
            ('GET', '/v1/graph/requests/request-1?project=team', None),
        ]
        for method, path, body in requests:
            with self.subTest(path=path):
                self.call(method, path, body, status=401)
        self.backend_constructor.assert_not_called()

    def test_reader_reads_and_backend_calls_release_the_global_lock(self):
        token = self.member('reader', 'reader')[0]
        self.call('GET', '/v1/graph/manifest?project=team', token=token)
        self.call('POST', '/v1/graph/query', self.read_query(), token)
        self.call('GET', '/v1/graph/requests/request-1?project=team', token=token)
        self.assertEqual([call[0] for call in self.backend_calls], ['manifest', 'query', 'receipt'])
        binding = self.backend_constructor.call_args.args[0]
        self.assertEqual((binding['project'], binding['database'], binding['graph_id']),
                         ('team', 'team_data', 'team-graph'))

    def test_reader_mutation_rejected_before_backend(self):
        token = self.member('reader', 'reader')[0]
        self.call('POST', '/v1/graph/mutations', self.mutation(), token, status=403)
        self.backend_constructor.assert_not_called()

    def test_mutation_and_receipt_use_authenticated_actor(self):
        token = self.member('writer')[0]
        self.call('POST', '/v1/graph/mutations', self.mutation(), token)
        self.call('GET', '/v1/graph/requests/request-1?project=team', token=token)
        for operation, arguments in self.backend_calls:
            if operation in ('mutate', 'receipt'):
                actor = arguments[0]
                self.assertEqual(actor['id'] if isinstance(actor, dict) else actor, 'writer')

    def test_nonexistent_and_other_projects_do_not_reach_backend(self):
        token = self.member('member')[0]
        self.call('POST', '/v1/projects', {'id': 'other'}, self.owner)
        for method, path, body, credential, status in (
            ('GET', '/v1/graph/manifest?project=missing', None, self.owner, 404),
            ('GET', '/v1/graph/manifest?project=other', None, token, 403),
            ('POST', '/v1/graph/query', self.read_query(project='other', actor='owner'), token, 403),
            ('POST', '/v1/graph/mutations', self.mutation(project='other', actor='owner'), token, 403),
            ('GET', '/v1/graph/requests/request-1?project=other', None, token, 403),
        ):
            with self.subTest(path=path, status=status):
                self.call(method, path, body, credential, status=status)
        self.backend_constructor.assert_not_called()

    def test_grant_revocation_applies_to_reads_mutations_and_receipts(self):
        token = self.member('member')[0]
        self.call('GET', '/v1/graph/manifest?project=team', token=token)
        self.backend_constructor.reset_mock()
        self.call('POST', '/v1/grants', {'actor': 'member', 'project': 'team', 'role': None}, self.owner)
        self.call('POST', '/v1/graph/query', self.read_query(), token, status=403)
        self.call('POST', '/v1/graph/mutations', self.mutation(), token, status=403)
        self.call('GET', '/v1/graph/requests/request-1?project=team', token=token, status=403)
        self.backend_constructor.assert_not_called()

    def test_credential_revocation_and_disabled_actor_rejected(self):
        revoked = self.member('revoked')[0]
        disabled = self.member('disabled')[0]
        self.call('POST', '/v1/credentials/revoke', {'actor': 'revoked', 'device': 'device'}, self.owner)
        self.call('POST', '/v1/actors/disable', {'actor': 'disabled'}, self.owner)
        for token in (revoked, disabled):
            self.call('GET', '/v1/graph/manifest?project=team', token=token, status=401)
        self.backend_constructor.assert_not_called()

    def new_invite(self, actor='new-member', grants=None):
        return self.call('POST', '/v1/invites',
                         {'actor': actor, 'grants': grants or {'team': 'member'}}, self.owner)

    def invitation_info(self, invitation, status=200):
        return self.call('POST', '/v1/invitation-info', {'invitation': invitation}, status=status)

    def test_invitation_info_is_unauthenticated_read_only_and_minimal(self):
        invitation = self.new_invite()
        before = {table: self.store.row(f'SELECT count(*) n FROM {table}')['n']
                  for table in ('actors', 'credentials', 'events')}
        for _ in range(2):
            result = self.invitation_info(invitation['invitation'])
            self.assertEqual(set(result), {'protocol', 'url', 'actor', 'project', 'channel', 'grants', 'expires'})
            self.assertEqual((result['url'], result['actor'], result['project'], result['channel']),
                             (self.url, 'new-member', 'team', 'general'))
            self.assertEqual(result['grants'], {'team': 'member'})
        after = {table: self.store.row(f'SELECT count(*) n FROM {table}')['n'] for table in before}
        self.assertEqual(before, after)
        self.assertIsNone(self.store.row('SELECT claimed FROM invites WHERE hash=?',
                                        (digest(invitation['invitation']),))['claimed'])

    def test_invitation_info_rejects_invalid_expired_revoked_and_claimed(self):
        self.invitation_info('not-an-invitation', status=403)
        expired = self.new_invite('expired')
        with self.store.lock, self.store.db:
            self.store.db.execute('UPDATE invites SET expires=0 WHERE hash=?',
                                  (digest(expired['invitation']),))
        self.invitation_info(expired['invitation'], status=403)
        revoked = self.new_invite('revoked-invite')
        self.call('POST', '/v1/invites/revoke', {'actor': 'revoked-invite'}, self.owner)
        self.invitation_info(revoked['invitation'], status=403)
        claimed = self.member('claimed')[1]
        self.invitation_info(claimed['invitation'], status=409)

    def test_invitation_info_rejects_ambiguous_project_or_channel(self):
        self.call('POST', '/v1/projects', {'id': 'other'}, self.owner)
        multiple_projects = self.new_invite('many-projects', {'team': 'member', 'other': 'reader'})
        self.invitation_info(multiple_projects['invitation'], status=400)
        one_project = self.new_invite('many-channels')
        self.call('POST', '/v1/channels', {'id': 'second-general', 'project': 'team'}, self.owner)
        self.invitation_info(one_project['invitation'], status=400)

    def test_invitation_info_prefers_project_general_and_excludes_private_channels(self):
        invitation = self.new_invite()
        self.call('POST', '/v1/channels', {'id': 'owner-private', 'project': 'team',
                                         'members': ['owner']}, self.owner)
        self.assertEqual(self.invitation_info(invitation['invitation'])['channel'], 'general')
        self.call('POST', '/v1/channels', {'id': 'team-general', 'project': 'team'}, self.owner)
        self.assertEqual(self.invitation_info(invitation['invitation'])['channel'], 'team-general')


if __name__ == '__main__':
    unittest.main()

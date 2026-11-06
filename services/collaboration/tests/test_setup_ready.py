"""Isolated HTTP tests of setup self-reports, team aggregation and persistence.

Reported readiness is never a user acceptance or a message read receipt.
"""
import json
import time
import unittest

import test_service as fixture
from otoha_server import Store, digest


class SetupReadyTest(unittest.TestCase):
    call = fixture.ServiceTest.call
    member = fixture.ServiceTest.member

    def setUp(self):
        fixture.ServiceTest.setUp(self)
        self.addCleanup(fixture.ServiceTest.tearDown, self)

    @staticmethod
    def checks(**changes):
        return {'repository': True, 'service': True, 'graph': True,
                'communication': True, **changes}

    def report(self, token, checks=None, status=200, **extra):
        return self.call('POST', '/v1/setup-ready?project=team',
                         {'checks': self.checks() if checks is None else checks, **extra}, token, status)

    def overview(self, token, project='team', status=200):
        return self.call('GET', '/v1/setup-ready?project=' + project, token=token, status=status)

    def invitation(self, actor):
        return self.call('POST', '/v1/invites',
                         {'actor': actor, 'grants': {'team': 'member'}}, self.owner)

    def enroll(self, invitation):
        actor = invitation['actor']
        token = actor + '-synthetic-credential-' + 'x' * 40
        self.call('POST', '/v1/enroll', {'invitation': invitation['invitation'], 'credential': token,
                                       'enrollment_id': actor + '-enroll', 'device': 'device'})
        return token

    def test_pending_invitation_then_enrollment_and_reports_converge(self):
        alice = self.member('alice')[0]
        bob_invite = self.invitation('bob')
        initial = self.overview(self.owner)
        self.assertEqual(initial['schema'], 'otoha-setup-ready/1')
        self.assertEqual(initial['scope'], 'project')
        self.assertEqual(set(initial['expected_members']), {'alice', 'bob'})
        self.assertNotIn('owner', initial['expected_members'])
        self.assertFalse(initial['team_ready'])
        self.report(alice)
        waiting = self.overview(self.owner)
        self.assertFalse(waiting['team_ready'])
        self.assertEqual({receipt['actor'] for receipt in waiting['receipts']}, {'alice'})
        bob = self.enroll(bob_invite)
        self.assertFalse(self.overview(self.owner)['team_ready'])
        self.report(bob)
        self.report(self.owner)
        ready = self.overview(self.owner)
        self.assertTrue(ready['team_ready'])
        self.assertTrue(ready['complete'])
        self.assertFalse(ready['truncated'])
        self.assertEqual(set(ready['expected_members']), {'alice', 'bob'})

    def test_receipt_server_time_and_failed_check_downgrade(self):
        alice = self.member('alice')[0]
        before = time.time()
        response = self.report(alice, device='device')
        receipt = response['receipt']
        self.assertEqual(response['schema'], 'otoha-setup-ready/1')
        self.assertEqual((receipt['actor'], receipt['device'], receipt['state']),
                         ('alice', 'device', 'self_report'))
        self.assertEqual(receipt['checks'], self.checks())
        self.assertTrue(receipt['ready'])
        self.assertIs(type(receipt['reported_at']), float)
        self.assertGreaterEqual(receipt['reported_at'], before)
        self.assertLessEqual(receipt['reported_at'], time.time())
        self.assertIs(type(receipt['ready_at']), float)
        self.assertIs(receipt['user_accepted'], False)
        self.report(self.owner)
        self.assertTrue(self.overview(self.owner)['team_ready'])
        failed = self.report(alice, self.checks(graph=False))['receipt']
        self.assertFalse(failed['ready'])
        self.assertIsNone(failed['ready_at'])
        self.assertIs(failed['user_accepted'], False)
        self.assertFalse(self.overview(self.owner)['team_ready'])

    def test_member_ready_does_not_make_unreported_or_failed_owner_ready(self):
        alice = self.member('alice')[0]
        self.report(alice)
        waiting = self.overview(self.owner)
        self.assertEqual(waiting['expected_members'], ['alice'])
        self.assertEqual({r['actor'] for r in waiting['receipts']}, {'alice'})
        self.assertFalse(waiting['team_ready'])
        self.report(self.owner)
        self.assertTrue(self.overview(self.owner)['team_ready'])
        self.report(self.owner, self.checks(service=False))
        self.assertFalse(self.overview(self.owner)['team_ready'])

    def test_project_admin_and_self_only_member_reader_visibility(self):
        admin = self.member('admin', 'admin')[0]
        member = self.member('member')[0]
        reader = self.member('reader', 'reader')[0]
        for token in (admin, member, reader):
            self.report(token)
        self.report(self.owner)
        owner_view = self.overview(self.owner)
        admin_view = self.overview(admin)
        self.assertEqual(admin_view['scope'], 'project')
        self.assertEqual(set(admin_view['expected_members']), {'admin', 'member', 'reader'})
        self.assertEqual({r['actor'] for r in admin_view['receipts']}, {'admin', 'member', 'reader'})
        self.assertTrue(admin_view['team_ready'])
        self.assertTrue(owner_view['team_ready'])
        for actor, token in (('member', member), ('reader', reader)):
            own = self.overview(token)
            self.assertEqual(own['scope'], 'self')
            self.assertEqual(own['expected_members'], [actor])
            self.assertEqual({r['actor'] for r in own['receipts']}, {actor})
            self.assertFalse(own['team_ready'])

    def test_project_view_includes_own_owner_report_but_no_other_owner(self):
        alice = self.member('alice')[0]
        other_owner = 'other-owner-synthetic-credential-' + 'x' * 40
        with self.store.lock, self.store.db:
            self.store.db.execute('INSERT INTO actors VALUES(?,?,1,0)', ('other-owner', 'Other fixture owner'))
            self.store.db.execute('INSERT INTO credentials VALUES(?,?,?,0,?)',
                                  (digest(other_owner), 'other-owner', 'other-device', time.time()))
        self.report(alice)
        self.report(self.owner)
        self.report(other_owner)
        own = self.overview(self.owner)
        other = self.overview(other_owner)
        self.assertEqual(own['expected_members'], ['alice'])
        self.assertEqual({r['actor'] for r in own['receipts']}, {'alice', 'owner'})
        self.assertEqual({r['actor'] for r in other['receipts']}, {'alice', 'other-owner'})
        self.assertTrue(own['team_ready'])

    def test_anonymous_cross_project_and_nonexistent_project(self):
        alice = self.member('alice')[0]
        self.overview(None, status=401)
        self.report(None, status=401)
        self.call('POST', '/v1/projects', {'id': 'other'}, self.owner)
        self.overview(alice, 'other', status=403)
        self.call('POST', '/v1/setup-ready?project=other', {'checks': self.checks()}, alice, status=403)
        self.overview(self.owner, 'missing', status=404)
        self.store.config['public_url'] = self.url + '/t/setup-route'
        manifest = self.call('GET', '/t/setup-route/v1/manifest', token=alice)
        self.assertIn('setup_ready', manifest['capabilities'])
        reported = self.call('POST', '/t/setup-route/v1/setup-ready?project=team',
                             {'checks': self.checks()}, alice)
        self.assertTrue(reported['receipt']['ready'])
        self.call('POST', '/t/setup-route/v1/setup-ready?project=team',
                  {'checks': self.checks()}, self.owner)
        prefixed = self.call('GET', '/t/setup-route/v1/setup-ready?project=team', token=self.owner)
        self.assertTrue(prefixed['team_ready'])

    def test_exact_checks_and_no_actor_state_device_injection(self):
        alice = self.member('alice')[0]
        self.report(alice)
        invalid = [
            {}, {'checks': None}, {'checks': []},
            {'checks': {**self.checks(), 'accepted': True}},
            {'checks': self.checks(graph=1)},
            {'checks': self.checks(repository='true')},
            {'checks': self.checks(communication=None)},
            {'checks': self.checks(), 'device': 'another-device'},
            {'checks': self.checks(), 'actor': 'owner'},
            {'checks': self.checks(), 'state': 'accepted'},
            {'checks': self.checks(), 'user_accepted': True},
            {'checks': self.checks(), 'reported_at': 1},
            {'checks': self.checks(), 'project': 'other'},
        ]
        for key in self.checks():
            invalid.append({'checks': {k: v for k, v in self.checks().items() if k != key}})
        for body in invalid:
            with self.subTest(body=body):
                self.call('POST', '/v1/setup-ready?project=team', body, alice, status=400)
        receipts = self.overview(self.owner)['receipts']
        self.assertEqual(len(receipts), 1)
        self.assertEqual(receipts[0]['actor'], 'alice')
        self.assertTrue(receipts[0]['ready'])

    def test_revoked_invitation_grant_and_credential_are_filtered(self):
        alice = self.member('alice')[0]
        revoked_invite = self.invitation('not-joining')
        self.call('POST', '/v1/invites/revoke', {'actor': 'not-joining'}, self.owner)
        expired_invite = self.invitation('expired-invite')
        with self.store.lock, self.store.db:
            self.store.db.execute('UPDATE invites SET expires=0 WHERE hash=?',
                                  (digest(expired_invite['invitation']),))
        disabled = self.member('disabled-member')[0]
        self.report(disabled)
        self.call('POST', '/v1/actors/disable', {'actor': 'disabled-member'}, self.owner)
        self.report(alice)
        self.report(self.owner)
        self.assertTrue(self.overview(self.owner)['team_ready'])
        filtered = self.overview(self.owner)
        for actor in ('not-joining', 'expired-invite', 'disabled-member'):
            self.assertNotIn(actor, filtered['expected_members'])
            self.assertNotIn(actor, {receipt['actor'] for receipt in filtered['receipts']})
        self.overview(disabled, status=401)
        self.call('POST', '/v1/credentials/revoke', {'actor': 'alice', 'device': 'device'}, self.owner)
        revoked = self.overview(self.owner)
        self.assertEqual({r['actor'] for r in revoked['receipts']}, {'owner'})
        self.assertEqual(revoked['expected_members'], ['alice'])
        self.assertFalse(revoked['team_ready'])
        self.overview(alice, status=401)
        self.report(alice, status=401)
        self.call('POST', '/v1/grants', {'actor': 'alice', 'project': 'team', 'role': None}, self.owner)
        self.assertEqual(self.overview(self.owner)['expected_members'], [])
        self.assertNotIn(revoked_invite['invitation'], json.dumps(revoked))

    def test_latest_valid_device_report_determines_member_readiness(self):
        alice = self.member('alice')[0]
        self.report(alice)
        self.report(self.owner)
        second_token = 'second-device-synthetic-credential-' + 'x' * 40
        self.call('POST', '/v1/credentials', {'device': 'laptop', 'credential': second_token}, alice)
        self.report(second_token, self.checks(service=False), device='laptop')
        view = self.overview(self.owner)
        self.assertEqual({r['device'] for r in view['receipts'] if r['actor'] == 'alice'}, {'device', 'laptop'})
        self.assertFalse(view['team_ready'])
        self.call('POST', '/v1/credentials/revoke', {'actor': 'alice', 'device': 'laptop'}, self.owner)
        valid = self.overview(self.owner)
        self.assertEqual({r['device'] for r in valid['receipts'] if r['actor'] == 'alice'}, {'device'})
        self.assertTrue(valid['team_ready'])

    def test_no_message_or_receipt_side_effects_and_no_secrets(self):
        alice, invite, _ = self.member('alice')
        message = self.call('POST', '/v1/messages', {'channel': 'general', 'session': 'owner-session',
                                                   'client_id': 'preserved-message', 'text': 'Keep original'}, self.owner)
        self.call('POST', '/v1/receipts', {'channel': 'general', 'session': 'alice-session',
                                         'message_id': message['message_id'], 'received': True, 'read': True}, alice)
        self.call('POST', '/v1/receipts', {'channel': 'general', 'session': 'alice-session',
                                         'received': message['message_id'], 'read': message['message_id']}, alice)
        tables = ('messages', 'events', 'receipts', 'message_receipts')
        before = {table: self.store.rows(f'SELECT * FROM {table} ORDER BY rowid') for table in tables}
        report = self.report(alice)
        overview = self.overview(self.owner)
        after = {table: self.store.rows(f'SELECT * FROM {table} ORDER BY rowid') for table in tables}
        self.assertEqual(before, after)
        output = json.dumps([report, overview])
        for secret in (alice, self.owner, invite['invitation'], digest(alice)):
            self.assertNotIn(secret, output)
        self.assertEqual(set(report['receipt']), {'actor', 'device', 'checks', 'ready', 'state',
                                                 'reported_at', 'ready_at', 'user_accepted'})

    def test_store_reopen_preserves_report_without_new_identity(self):
        alice = self.member('alice')[0]
        original = self.report(alice)['receipt']
        owner_report = self.report(self.owner)['receipt']
        before = self.store.row('SELECT count(*) n FROM credentials')['n']
        reopened = Store(self.root / 'state')
        try:
            result = reopened.handle('GET', '/v1/setup-ready', {'project': ['team']}, {}, self.owner)
            preserved = {receipt['actor']: receipt for receipt in result['receipts']}
            self.assertEqual(preserved, {'alice': original, 'owner': owner_report})
            self.assertTrue(result['team_ready'])
            self.assertEqual(reopened.row('SELECT count(*) n FROM credentials')['n'], before)
        finally:
            reopened.db.close()

    def test_over_500_records_is_truthfully_incomplete_and_not_team_ready(self):
        self.member('many-devices')
        now = time.time()
        credentials = [('bulk-device-%03d-' % index) + 'x' * 40 for index in range(501)]
        with self.store.lock, self.store.db:
            self.store.db.executemany('INSERT INTO credentials VALUES(?,?,?,0,?)',
                                      [(digest(token), 'many-devices', 'bulk-%03d' % index, now)
                                       for index, token in enumerate(credentials)])
        for token in credentials:
            self.report(token)
        self.report(self.owner)
        view = self.overview(self.owner)
        self.assertLessEqual(len(view['receipts']), 500)
        self.assertFalse(view['complete'])
        self.assertTrue(view['truncated'])
        self.assertFalse(view['team_ready'])


if __name__ == '__main__':
    unittest.main()

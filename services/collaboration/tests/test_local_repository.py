"""Real smart HTTP Git and new-state bootstrap; no existing repositories used."""
import hashlib
import gzip
import http.client
import json
import os
import pathlib
import secrets
import shutil
import subprocess
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
from otoha_server import Server, Store
from otoha_repository import MAX_REQUEST_BYTES


@unittest.skipUnless(shutil.which('git'), 'Git is required')
class LocalRepositoryTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='otoha-local-host-')
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.state = self.root / 'state'
        self.profile = self.root / 'owner.json'
        self.graph_credentials = self.root / 'graph-credentials.json'
        self.graph_credentials.write_text(json.dumps({'username': 'test', 'password': secrets.token_urlsafe(32)}))
        self.graph_credentials.chmod(0o600)
        self.script = pathlib.Path(__file__).resolve().parents[1] / 'otoha_server.py'
        self.prefix = '/t/test-route-1234'
        self.public_url = 'https://example.invalid' + self.prefix
        self.arguments = [sys.executable, str(self.script), 'bootstrap', '--state', str(self.state),
                          '--url', self.public_url, '--credentials', str(self.profile), '--owner', 'owner',
                          '--project', 'project', '--channel', 'project-general',
                          '--graph-url', 'http://127.0.0.1:1', '--graph-namespace', 'test',
                          '--graph-database', 'project', '--graph-id', 'project-graph',
                          '--graph-credentials', str(self.graph_credentials), '--min-free-bytes', '0']
        initialized = subprocess.run(self.arguments, capture_output=True, text=True, check=True)
        self.initialized = json.loads(initialized.stdout)
        self.owner = json.loads(self.profile.read_text())['token']
        self.bare = self.state / 'repositories/project.git'
        self.env = {k: v for k, v in os.environ.items() if not k.startswith('GIT_')}
        self.env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT='0')
        self.store = Store(self.state)
        self.server = Server(('127.0.0.1', 0), self.store)
        self.addCleanup(self.store.db.close)
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.local = 'http://127.0.0.1:' + str(self.server.server_port)
        self.url = self.local + self.prefix
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def api(self, method, path, body=None, token=None, status=200):
        headers = {'Content-Type': 'application/json'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        request = urllib.request.Request(self.url + path, method=method, headers=headers,
                                         data=json.dumps(body).encode() if body is not None else None)
        try:
            response = self.opener.open(request, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            self.assertEqual(status, response.status)
            return json.load(response)

    def member(self, actor, role='member'):
        invitation = self.api('POST', '/v1/invites', {'actor': actor, 'grants': {'project': role}}, self.owner)
        token = secrets.token_urlsafe(32)
        self.api('POST', '/v1/enroll', {'invitation': invitation['invitation'], 'credential': token,
                                      'enrollment_id': actor + '-enroll', 'device': 'fixture'})
        return token

    def git(self, *arguments, token=None, expected=0):
        command = ['git', '-c', 'credential.helper=']
        if token:
            command += ['-c', 'http.extraHeader=Authorization: Bearer ' + token]
        result = subprocess.run(command + list(arguments), env=self.env, cwd=self.root,
                                capture_output=True, text=True, timeout=20)
        if expected is not None:
            self.assertEqual(expected, result.returncode, result.stderr)
        return result

    def repository_url(self, token):
        binding = self.api('GET', '/v1/repository?project=project', token=token)
        self.assertEqual(self.public_url + '/v1/git/project/project/project.git', binding['url'])
        self.assertNotIn('path', binding)
        self.assertNotIn('password', binding)
        return self.local + urllib.parse.urlsplit(binding['url']).path

    def test_bootstrap_is_idempotent_and_refuses_existing_different_state(self):
        before_profile = self.profile.read_bytes()
        before_head = self.git('--git-dir=' + str(self.bare), 'rev-parse', 'HEAD').stdout
        repeated = subprocess.run(self.arguments, capture_output=True, text=True, check=True)
        self.assertTrue(json.loads(repeated.stdout)['resumed'])
        self.assertEqual(before_profile, self.profile.read_bytes())
        self.assertEqual(before_head, self.git('--git-dir=' + str(self.bare), 'rev-parse', 'HEAD').stdout)
        self.assertEqual('Otoha <otoha@example.invalid>', self.git('--git-dir=' + str(self.bare), 'show', '-s', '--format=%an <%ae>').stdout.strip())
        changed = self.arguments.copy()
        changed[changed.index('--project') + 1] = 'different'
        rejected = subprocess.run(changed, capture_output=True, text=True)
        self.assertNotEqual(0, rejected.returncode)
        self.assertEqual(before_profile, self.profile.read_bytes())
        self.assertEqual(1, self.store.row('SELECT count(*) n FROM credentials')['n'])
        old = self.root / 'old-state'
        old.mkdir()
        (old / 'server.json').write_text('{"existing":"preserve"}')
        changed = self.arguments.copy()
        changed[changed.index('--state') + 1] = str(old)
        rejected = subprocess.run(changed, capture_output=True, text=True)
        self.assertNotEqual(0, rejected.returncode)
        self.assertEqual('{"existing":"preserve"}', (old / 'server.json').read_text())

    def test_member_clone_push_reader_fetch_and_no_hooks(self):
        writer, reader = self.member('writer'), self.member('reader', 'reader')
        url = self.repository_url(writer)
        self.assertNotEqual(0, self.git('ls-remote', url, expected=None).returncode)
        self.git('clone', url, str(self.root / 'writer'), token=writer)
        self.git('clone', url, str(self.root / 'reader'), token=reader)
        marker = self.root / 'hook-was-run'
        hook = self.bare / 'hooks/pre-receive'
        hook.write_text('#!/bin/sh\ntouch ' + str(marker) + '\nexit 1\n')
        hook.chmod(0o700)
        configured_hooks = self.root / 'configured-hooks'
        configured_hooks.mkdir()
        alternate_hook = configured_hooks / 'pre-receive'
        alternate_hook.write_text(hook.read_text())
        alternate_hook.chmod(0o700)
        self.git('--git-dir=' + str(self.bare), 'config', 'core.hooksPath', str(configured_hooks))
        (self.root / 'writer/feature.txt').write_text('Member-created content\n')
        self.git('-C', str(self.root / 'writer'), 'add', 'feature.txt')
        self.git('-C', str(self.root / 'writer'), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                 'commit', '-m', 'Synthetic member update')
        self.git('-C', str(self.root / 'writer'), 'push', 'origin', 'main', token=writer)
        self.assertFalse(marker.exists())
        self.git('-C', str(self.root / 'reader'), 'fetch', 'origin', token=reader)
        writer_head = self.git('-C', str(self.root / 'writer'), 'rev-parse', 'HEAD').stdout
        reader_remote = self.git('-C', str(self.root / 'reader'), 'rev-parse', 'origin/main').stdout
        self.assertEqual(writer_head, reader_remote)
        (self.root / 'reader/no.txt').write_text('Reader write must fail\n')
        self.git('-C', str(self.root / 'reader'), 'add', 'no.txt')
        self.git('-C', str(self.root / 'reader'), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                 'commit', '-m', 'Rejected reader update')
        rejected = self.git('-C', str(self.root / 'reader'), 'push', 'origin', 'HEAD:refs/heads/reader-write', token=reader, expected=None)
        self.assertNotEqual(0, rejected.returncode)
        self.assertEqual(writer_head, self.git('--git-dir=' + str(self.bare), 'rev-parse', 'HEAD').stdout)

    def test_scope_paths_revocation_and_request_limit(self):
        writer = self.member('writer')
        url = self.repository_url(writer)
        self.git('ls-remote', url, token=writer)
        connection = http.client.HTTPConnection('127.0.0.1', self.server.server_port, timeout=5)
        path = urllib.parse.urlsplit(url).path + '/git-receive-pack'
        connection.request('POST', path, body=None, headers={
            'Authorization': 'Bearer ' + writer,
            'Content-Type': 'application/x-git-receive-pack-request',
            'Content-Length': str(MAX_REQUEST_BYTES + 1)})
        response = connection.getresponse()
        self.assertEqual(413, response.status)
        response.read(); connection.close()
        self.api('GET', '/v1/git/project/other/project.git/info/refs?service=git-upload-pack', token=writer, status=404)
        self.api('POST', '/v1/grants', {'actor': 'writer', 'project': 'project', 'role': None}, self.owner)
        self.assertNotEqual(0, self.git('ls-remote', url, token=writer, expected=None).returncode)
        self.api('GET', '/v1/repository?project=missing', token=self.owner, status=404)

    def test_credentials_export_is_not_overwritten(self):
        alternate = self.root / 'different-owner.json'
        alternate.write_text('{"existing":"keep"}')
        arguments = self.arguments.copy()
        arguments[arguments.index('--credentials') + 1] = str(alternate)
        rejected = subprocess.run(arguments, capture_output=True, text=True)
        self.assertNotEqual(0, rejected.returncode)
        self.assertEqual('{"existing":"keep"}', alternate.read_text())
        self.assertEqual(self.owner, json.loads((self.state / 'owner.json').read_text())['token'])

    def test_gzip_upload_pack_post_preserves_encoding(self):
        reader = self.member('reader', 'reader')
        url = self.repository_url(reader)
        head = self.git('--git-dir=' + str(self.bare), 'rev-parse', 'HEAD').stdout.strip()
        want = ('want ' + head + ' multi_ack_detailed side-band-64k ofs-delta\n').encode()
        packet = ('%04x' % (len(want) + 4)).encode() + want + b'0000' + b'0009done\n'
        request = urllib.request.Request(url + '/git-upload-pack', data=gzip.compress(packet), headers={
            'Authorization': 'Bearer ' + reader,
            'Content-Type': 'application/x-git-upload-pack-request', 'Content-Encoding': 'gzip'})
        with self.opener.open(request, timeout=10) as response:
            self.assertEqual(200, response.status)
            self.assertEqual('application/x-git-upload-pack-result', response.headers.get_content_type())
            self.assertIn(b'PACK', response.read())


if __name__ == '__main__':
    unittest.main()

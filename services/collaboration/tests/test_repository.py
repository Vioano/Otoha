"""Exercise the authorized repository channel against real git-http-backend."""
import base64
import gzip
import http.client
import http.server
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


class GitBackendHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        self.forward()

    def do_POST(self):
        self.forward()

    def forward(self):
        authorization = self.headers.get('Authorization', '')
        self.server.authorizations.append(authorization)
        if authorization != self.server.expected_authorization:
            self.send_response(401)
            self.send_header('WWW-Authenticate', 'Basic realm="test"')
            self.end_headers()
            return
        if self.server.redirect:
            self.send_response(302)
            self.send_header('Location', self.server.redirect)
            self.end_headers()
            return
        parsed = urllib.parse.urlsplit(self.path)
        length = int(self.headers.get('Content-Length', '0'))
        body = self.rfile.read(length)
        env = {**os.environ, 'GIT_PROJECT_ROOT': str(self.server.repository_root),
               'GIT_HTTP_EXPORT_ALL': '1', 'PATH_INFO': urllib.parse.unquote(parsed.path),
               'QUERY_STRING': parsed.query, 'REQUEST_METHOD': self.command,
               'CONTENT_TYPE': self.headers.get('Content-Type', ''),
               'HTTP_CONTENT_ENCODING': self.headers.get('Content-Encoding', ''),
               'CONTENT_LENGTH': str(length), 'REMOTE_USER': 'test-git',
               'REMOTE_ADDR': '127.0.0.1', 'SERVER_PROTOCOL': 'HTTP/1.1',
               'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': os.devnull}
        result = subprocess.run([self.server.backend], env=env, input=body,
                                capture_output=True, timeout=15)
        headers, separator, data = result.stdout.partition(b'\r\n\r\n')
        if not separator:
            headers, separator, data = result.stdout.partition(b'\n\n')
        status, response_headers = 200, []
        for line in headers.decode('latin-1').splitlines():
            key, _, value = line.partition(':')
            if key.lower() == 'status':
                status = int(value.strip().split()[0])
            elif key.lower() not in ('content-length', 'connection', 'transfer-encoding'):
                response_headers.append((key, value.strip()))
        self.send_response(status)
        for key, value in response_headers:
            self.send_header(key, value)
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)


class RepositoryTest(unittest.TestCase):
    def setUp(self):
        if not shutil.which('git'):
            self.skipTest('git is required for the repository fixture')
        exec_path = subprocess.check_output(['git', '--exec-path'], text=True).strip()
        backend = pathlib.Path(exec_path) / 'git-http-backend'
        if not backend.is_file():
            self.skipTest('git-http-backend is required for the repository fixture')
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.addCleanup(self.temp.cleanup)
        self.git_env = {**os.environ, 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': os.devnull,
                        'GIT_TERMINAL_PROMPT': '0', 'GIT_TRACE': '0',
                        'GIT_TRACE_CURL': '0', 'GIT_CURL_VERBOSE': '0'}
        self.repos = self.root / 'repositories'
        self.bare = self.repos / 'test-owner' / 'test-repo.git'
        self.bare.parent.mkdir(parents=True)
        self.git('init', '--bare', str(self.bare))
        self.git('--git-dir=' + str(self.bare), 'config', 'http.receivepack', 'true')
        self.seed = self.root / 'seed'
        self.git('init', str(self.seed))
        self.git('-C', str(self.seed), 'checkout', '-b', 'main')
        (self.seed / 'README.md').write_text('Synthetic initial content\n')
        self.git('-C', str(self.seed), 'add', 'README.md')
        self.git('-C', str(self.seed), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                 'commit', '-m', 'Initialize synthetic repository')
        self.git('-C', str(self.seed), 'remote', 'add', 'origin', str(self.bare))
        self.git('-C', str(self.seed), 'push', 'origin', 'main')
        self.git('--git-dir=' + str(self.bare), 'symbolic-ref', 'HEAD', 'refs/heads/main')
        self.upstream = http.server.ThreadingHTTPServer(('127.0.0.1', 0), GitBackendHandler)
        self.upstream.repository_root = self.repos
        self.upstream.backend = str(backend)
        self.upstream.authorizations = []
        self.upstream.redirect = None
        self.upstream.expected_authorization = 'Basic ' + base64.b64encode(b'test-git:fixture-password').decode()
        self.upstream_thread = threading.Thread(target=self.upstream.serve_forever, daemon=True)
        self.upstream_thread.start()
        self.addCleanup(self.upstream.server_close)
        self.addCleanup(self.upstream.shutdown)
        profile = self.root / 'gitea-test-profile.json'
        profile.write_text(json.dumps({'url': 'http://127.0.0.1:' + str(self.upstream.server_port),
                                       'username': 'test-git', 'password': 'fixture-password'}))
        profile.chmod(0o600)
        self.profile, self.profile_before = profile, profile.read_bytes()
        credentials = self.root / 'owner.json'
        script = pathlib.Path(__file__).resolve().parents[1] / 'otoha_server.py'
        subprocess.run([sys.executable, str(script), 'init', '--state', str(self.root / 'state'),
                        '--url', 'http://127.0.0.1:1', '--credentials', str(credentials),
                        '--min-free-bytes', '0'], check=True, capture_output=True)
        self.owner = json.loads(credentials.read_text())['token']
        (self.root / 'state' / 'repositories.json').write_text(json.dumps({'projects': {'team': {
            'repository': 'test-owner/test-repo', 'profile': str(profile)}}}))
        self.store = Store(self.root / 'state')
        self.server = Server(('127.0.0.1', 0), self.store)
        self.url = 'http://127.0.0.1:' + str(self.server.server_port)
        self.store.config['public_url'] = self.url
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.addCleanup(self.store.db.close)
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def git(self, *args, token=None, success=True):
        env = dict(self.git_env)
        if token:
            env.update({'GIT_CONFIG_COUNT': '1', 'GIT_CONFIG_KEY_0': 'http.extraHeader',
                        'GIT_CONFIG_VALUE_0': 'Authorization: Bearer ' + token})
        result = subprocess.run(['git', *args], env=env, capture_output=True, timeout=20)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr.decode(errors='replace'))
        else:
            self.assertNotEqual(result.returncode, 0)
        return result

    def call(self, method, path, body=None, token=None, status=200, json_response=True):
        headers = {'Content-Type': 'application/json'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        request = urllib.request.Request(self.url + path, method=method, headers=headers,
                                         data=json.dumps(body).encode() if body is not None else None)
        try:
            response = self.opener.open(request, timeout=8)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            self.assertEqual(response.status, status)
            return json.load(response) if json_response else response.read()

    def enroll(self, actor, role='member'):
        invitation = self.call('POST', '/v1/invites',
                               {'actor': actor, 'grants': {'team': role}}, self.owner)
        token = secrets.token_urlsafe(32)
        self.call('POST', '/v1/enroll', {'invitation': invitation['invitation'], 'credential': token,
                                       'enrollment_id': actor + '-enroll', 'device': actor + '-device'})
        return token

    def test_reader_clone_fetch_member_push_and_private_profile_preserved(self):
        reader, member = self.enroll('reader', 'reader'), self.enroll('member')
        metadata = self.call('GET', '/v1/repository?project=team', token=reader)
        for key, value in {'repository': 'test-owner/test-repo', 'transport': 'otoha',
                           'url': self.url + '/v1/git/team/test-owner/test-repo.git', 'access': 'reader'}.items():
            self.assertEqual(metadata[key], value)
        clone = self.root / 'member-checkout'
        self.git('clone', metadata['url'], str(clone), token=reader)
        self.assertEqual((clone / 'README.md').read_text(), 'Synthetic initial content\n')
        (self.seed / 'README.md').write_text('Changed by upstream fixture\n')
        self.git('-C', str(self.seed), 'add', 'README.md')
        self.git('-C', str(self.seed), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                 'commit', '-m', 'Upstream change')
        self.git('-C', str(self.seed), 'push', 'origin', 'main')
        self.git('-C', str(clone), 'pull', '--ff-only', token=reader)
        self.assertEqual((clone / 'README.md').read_text(), 'Changed by upstream fixture\n')
        (clone / 'member.txt').write_text('Synthetic member update\n')
        self.git('-C', str(clone), 'add', 'member.txt')
        self.git('-C', str(clone), '-c', 'user.name=Member', '-c', 'user.email=member@example.invalid',
                 'commit', '-m', 'Member change')
        denied = self.git('-C', str(clone), 'push', 'origin', 'main', token=reader, success=False)
        self.assertIn('403', denied.stderr.decode(errors='replace'))
        self.git('-C', str(clone), 'push', 'origin', 'main', token=member)
        local_head = self.git('-C', str(clone), 'rev-parse', 'HEAD').stdout
        remote_head = self.git('--git-dir=' + str(self.bare), 'rev-parse', 'refs/heads/main').stdout
        self.assertEqual(local_head, remote_head)
        self.assertEqual(self.profile.read_bytes(), self.profile_before)
        self.assertTrue(self.upstream.authorizations)
        self.assertTrue(all(header == self.upstream.expected_authorization for header in self.upstream.authorizations))
        self.assertTrue(all(reader not in header and member not in header and self.owner not in header
                            for header in self.upstream.authorizations))
        self.assertEqual(self.store.row('SELECT count(*) n FROM messages')['n'], 0)
        self.assertEqual(self.store.row('SELECT count(*) n FROM message_receipts')['n'], 0)

    def test_scope_exact_repository_revocation_and_missing_binding(self):
        member = self.enroll('member')
        self.call('GET', '/v1/repository?project=team', status=401)
        self.call('POST', '/v1/projects', {'id': 'secret'}, self.owner)
        self.call('GET', '/v1/repository?project=secret', token=member, status=403)
        self.call('GET', '/v1/repository?project=secret', token=self.owner, status=404)
        base = '/v1/git/team/test-owner/test-repo.git'
        self.call('GET', '/v1/git/team/test-owner/other.git/info/refs?service=git-upload-pack',
                  token=member, status=404)
        self.call('GET', '/v1/git/secret/test-owner/test-repo.git/info/refs?service=git-upload-pack',
                  token=member, status=403)
        count = len(self.upstream.authorizations)
        self.call('POST', '/v1/grants', {'actor': 'member', 'project': 'team', 'role': None}, self.owner)
        self.call('GET', '/v1/repository?project=team', token=member, status=403)
        self.call('GET', base + '/info/refs?service=git-upload-pack', token=member, status=403)
        self.assertEqual(len(self.upstream.authorizations), count)

    def test_upstream_redirect_is_not_followed(self):
        self.upstream.redirect = 'http://127.0.0.1:1/untrusted-target'
        self.call('GET', '/v1/git/team/test-owner/test-repo.git/info/refs?service=git-upload-pack',
                  token=self.owner, status=502)
        self.assertEqual(len(self.upstream.authorizations), 1)

    def test_gzip_upload_pack_is_forwarded_with_encoding(self):
        reader = self.enroll('reader', 'reader')
        head = self.git('--git-dir=' + str(self.bare), 'rev-parse', 'HEAD').stdout.decode().strip()
        want = ('want ' + head + ' multi_ack_detailed side-band-64k ofs-delta\n').encode()
        packet = ('%04x' % (len(want) + 4)).encode() + want + b'0000' + b'0009done\n'
        request = urllib.request.Request(self.url + '/v1/git/team/test-owner/test-repo.git/git-upload-pack',
                    data=gzip.compress(packet), headers={'Authorization': 'Bearer ' + reader,
                    'Content-Type': 'application/x-git-upload-pack-request', 'Content-Encoding': 'gzip'})
        with self.opener.open(request, timeout=10) as response:
            self.assertEqual(200, response.status)
            self.assertIn(b'PACK', response.read())

    def test_oversized_post_and_reader_direct_write_are_rejected_before_reading_body(self):
        reader = self.enroll('reader', 'reader')
        cases = [('git-upload-pack', self.owner, MAX_REQUEST_BYTES + 1, 413),
                 ('git-receive-pack', reader, 0, 403)]
        for endpoint, token, length, expected in cases:
            connection = http.client.HTTPConnection('127.0.0.1', self.server.server_port, timeout=3)
            try:
                connection.putrequest('POST', '/v1/git/team/test-owner/test-repo.git/' + endpoint)
                connection.putheader('Authorization', 'Bearer ' + token)
                connection.putheader('Content-Type', 'application/x-' + endpoint + '-request')
                connection.putheader('Content-Length', str(length))
                connection.endheaders()
                # No pack body is sent. Rejection must happen from the request
                # metadata, before allocating or waiting for a large upload.
                response = connection.getresponse()
                self.assertEqual(response.status, expected)
                response.read()
            finally:
                connection.close()
        self.assertEqual(len(self.upstream.authorizations), 0)


if __name__ == '__main__':
    unittest.main()

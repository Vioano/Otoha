"""Project-scoped smart Git access using an existing Otoha identity.

Bindings and upstream credentials are private administrator-owned files. This
module supports smart HTTP clone/fetch/push, not LFS or arbitrary Gitea APIs.
"""
import base64
import http.client
import json
import pathlib
import re
import os
import select
import shutil
import subprocess
import tempfile
import time
import urllib.parse


MAX_REQUEST_BYTES = 64 * 1024 * 1024
MAX_RESPONSE_BYTES = 512 * 1024 * 1024
CHUNK_BYTES = 64 * 1024
IDENTIFIER = r'[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}'
GIT_PATH = re.compile(r'^/v1/git/(' + IDENTIFIER + r')/(' + IDENTIFIER + r')/('
                      + IDENTIFIER + r')\.git/(info/refs|git-upload-pack|git-receive-pack)$')
REPOSITORY = re.compile(r'^' + IDENTIFIER + '/' + IDENTIFIER + r'$')


class RepositoryError(Exception):
    def __init__(self, status, code):
        self.status, self.code = status, code


def binding_for(store, actor, project):
    if not store.row('SELECT 1 FROM projects WHERE id=?', (project,)):
        raise RepositoryError(404, 'project_not_found')
    role = store.role(actor, project)
    if not role:
        raise RepositoryError(403, 'project_forbidden')
    try:
        bindings = json.loads((store.state / 'repositories.json').read_text())
        binding = bindings.get('projects', {}).get(project)
    except FileNotFoundError:
        binding = None
    except (OSError, ValueError, TypeError, AttributeError):
        raise RepositoryError(503, 'repository_configuration_unavailable') from None
    if binding is None:
        raise RepositoryError(404, 'repository_not_bound')
    if (not isinstance(binding, dict) or not isinstance(binding.get('repository'), str)
            or not REPOSITORY.fullmatch(binding['repository'])):
        raise RepositoryError(503, 'repository_configuration_invalid')
    if binding.get('kind') == 'local':
        if not isinstance(binding.get('path'), str) or not pathlib.Path(binding['path']).is_absolute():
            raise RepositoryError(503, 'repository_configuration_invalid')
    elif binding.get('kind', 'gitea') != 'gitea' or not isinstance(binding.get('profile'), str) or not pathlib.Path(binding['profile']).is_absolute():
        raise RepositoryError(503, 'repository_configuration_invalid')
    return binding, role


def repository_info(store, actor, project):
    binding, role = binding_for(store, actor, project)
    capabilities = ['smart-http-clone', 'smart-http-fetch']
    if role != 'reader':
        capabilities.append('smart-http-push')
    return {'repository': binding['repository'], 'transport': 'otoha',
            'url': store.config['public_url'].rstrip('/') + '/v1/git/' + project + '/'
                   + binding['repository'] + '.git',
            'access': role,
            'limits': {'request_bytes': MAX_REQUEST_BYTES, 'response_bytes': MAX_RESPONSE_BYTES},
            'capabilities': capabilities}


def upstream_profile(binding):
    try:
        profile = json.loads(pathlib.Path(binding['profile']).read_text())
        target = urllib.parse.urlsplit(profile['url'])
        if (target.scheme not in ('http', 'https') or not target.hostname or target.username
                or target.password or target.query or target.fragment):
            raise ValueError('invalid upstream locator')
        username = profile.get('username', '')
        password = profile.get('token') or profile.get('password', '')
        if (not isinstance(username, str) or not username or not isinstance(password, str)
                or not password or ':' in username):
            raise ValueError('missing upstream credentials')
        auth = 'Basic ' + base64.b64encode((username + ':' + password).encode()).decode()
        return target, auth
    except (OSError, ValueError, TypeError, KeyError, AttributeError):
        raise RepositoryError(503, 'repository_profile_unavailable') from None


def proxy_git_request(handler, token, parsed_url):
    """Authenticate before network I/O, then forward bounded binary streams."""
    store = handler.server.store
    with store.lock:
        actor = store.authenticate(token)
        match = GIT_PATH.fullmatch(parsed_url.path)
        if not match:
            raise RepositoryError(404, 'git_route_not_found')
        project, owner, repository, endpoint = match.groups()
        binding, role = binding_for(store, actor, project)
        if binding['repository'] != owner + '/' + repository:
            raise RepositoryError(404, 'repository_not_bound')
        if binding.get('kind') != 'local':
            target, authorization = upstream_profile(binding)

    method = handler.command
    query = urllib.parse.parse_qs(parsed_url.query)
    if endpoint == 'info/refs' and method == 'GET':
        if set(query) != {'service'} or len(query['service']) != 1:
            raise RepositoryError(400, 'invalid_git_service')
        service = query['service'][0]
        if service not in ('git-upload-pack', 'git-receive-pack'):
            raise RepositoryError(400, 'invalid_git_service')
        suffix = '?' + urllib.parse.urlencode({'service': service})
        length = 0
    elif endpoint in ('git-upload-pack', 'git-receive-pack') and method == 'POST':
        if query:
            raise RepositoryError(400, 'invalid_git_query')
        service, suffix = endpoint, ''
        if handler.headers.get('Transfer-Encoding') or not handler.headers.get('Content-Length'):
            raise RepositoryError(411, 'git_content_length_required')
        try:
            length = int(handler.headers['Content-Length'])
        except (TypeError, ValueError):
            raise RepositoryError(400, 'invalid_git_content_length') from None
        if length < 0:
            raise RepositoryError(400, 'invalid_git_content_length')
        if length > MAX_REQUEST_BYTES:
            raise RepositoryError(413, 'git_request_too_large')
        if handler.headers.get_content_type() != 'application/x-' + service + '-request':
            raise RepositoryError(400, 'invalid_git_content_type')
        if handler.headers.get('Content-Encoding', '').lower() not in ('', 'identity', 'gzip'):
            raise RepositoryError(415, 'unsupported_git_content_encoding')
    else:
        raise RepositoryError(405, 'git_method_not_allowed')
    if service == 'git-receive-pack' and role == 'reader':
        raise RepositoryError(403, 'repository_read_only')

    if binding.get('kind') == 'local':
        return local_git_request(handler, binding, actor['id'], endpoint, service, length, suffix)

    path = target.path.rstrip('/') + '/' + binding['repository'] + '.git/' + endpoint + suffix
    connection_class = http.client.HTTPSConnection if target.scheme == 'https' else http.client.HTTPConnection
    connection = connection_class(target.hostname, target.port, timeout=30)
    headers_sent = False
    try:
        connection.putrequest(method, path, skip_accept_encoding=True)
        connection.putheader('Authorization', authorization)
        connection.putheader('User-Agent', 'Otoha-Repository/1')
        connection.putheader('Accept', '*/*')
        git_protocol = handler.headers.get('Git-Protocol')
        if git_protocol:
            if len(git_protocol) > 1024:
                raise RepositoryError(400, 'invalid_git_protocol')
            connection.putheader('Git-Protocol', git_protocol)
        if method == 'POST':
            connection.putheader('Content-Type', 'application/x-' + service + '-request')
            connection.putheader('Content-Length', str(length))
            if handler.headers.get('Content-Encoding'):
                connection.putheader('Content-Encoding', handler.headers['Content-Encoding'].lower())
        connection.endheaders()
        remaining = length
        while remaining:
            chunk = handler.rfile.read(min(CHUNK_BYTES, remaining))
            if not chunk:
                raise RepositoryError(400, 'git_request_incomplete')
            connection.send(chunk)
            remaining -= len(chunk)
        response = connection.getresponse()
        # Never follow or relay a redirect carrying a new credential boundary.
        if 300 <= response.status < 400:
            raise RepositoryError(502, 'git_upstream_redirect_refused')
        if response.status < 200 or response.status >= 300:
            raise RepositoryError(502, 'git_upstream_rejected')
        raw_length = response.getheader('Content-Length')
        response_length = int(raw_length) if raw_length is not None else None
        if response_length is not None and not 0 <= response_length <= MAX_RESPONSE_BYTES:
            raise RepositoryError(502, 'git_response_too_large')
        handler.send_response(response.status)
        handler.send_header('Content-Type', response.getheader('Content-Type', 'application/octet-stream'))
        handler.send_header('Cache-Control', 'no-store')
        handler.send_header('X-Content-Type-Options', 'nosniff')
        handler.send_header('X-Otoha-Response-Limit', str(MAX_RESPONSE_BYTES))
        if response_length is not None:
            handler.send_header('Content-Length', str(response_length))
        else:
            handler.send_header('Connection', 'close')
            handler.close_connection = True
        handler.end_headers()
        headers_sent = True
        copied = 0
        while True:
            chunk = response.read(min(CHUNK_BYTES, MAX_RESPONSE_BYTES - copied + 1))
            if not chunk:
                break
            if copied + len(chunk) > MAX_RESPONSE_BYTES:
                # Headers already describe the stream limit. Close an oversized
                # unknown-length stream; never append a JSON response to a pack.
                handler.close_connection = True
                return
            handler.wfile.write(chunk)
            copied += len(chunk)
        handler.wfile.flush()
    except (http.client.HTTPException, OSError, ValueError):
        if headers_sent:
            handler.close_connection = True
            return
        raise RepositoryError(502, 'git_upstream_unavailable') from None
    finally:
        connection.close()


def local_git_request(handler, binding, actor, endpoint, service, length, suffix):
    """Serve precisely the bound bare repository, with hooks disabled.

    Input is spooled to a bounded private file. CGI output is streamed with a
    total deadline and byte bound; neither pack direction is buffered in RAM.
    """
    binary = shutil.which('git')
    if not binary:
        raise RepositoryError(503, 'git_not_installed')
    repository = pathlib.Path(binding['path']).resolve()
    if not repository.is_dir() or not (repository / 'HEAD').is_file() or not (repository / 'objects').is_dir():
        raise RepositoryError(503, 'local_repository_unavailable')
    env = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
    hooks = handler.server.store.state / 'disabled-git-hooks'
    hooks.mkdir(mode=0o700, exist_ok=True)
    env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
               GIT_TERMINAL_PROMPT='0', GIT_CONFIG_COUNT='3',
               GIT_CONFIG_KEY_0='core.hooksPath', GIT_CONFIG_VALUE_0=str(hooks),
               GIT_CONFIG_KEY_1='http.receivepack', GIT_CONFIG_VALUE_1='true',
               GIT_CONFIG_KEY_2='core.fsmonitor', GIT_CONFIG_VALUE_2='false')
    try:
        checked = subprocess.run([binary, '--git-dir=' + str(repository), 'rev-parse', '--is-bare-repository'],
                                 env=env, capture_output=True, timeout=5)
    except (OSError, subprocess.TimeoutExpired):
        raise RepositoryError(503, 'local_repository_unavailable') from None
    if checked.returncode != 0 or checked.stdout.strip() != b'true':
        raise RepositoryError(503, 'local_repository_not_bare')
    git_protocol = handler.headers.get('Git-Protocol', '')
    if len(git_protocol) > 1024:
        raise RepositoryError(400, 'invalid_git_protocol')
    env.update(GIT_PROJECT_ROOT=str(repository.parent), GIT_HTTP_EXPORT_ALL='1',
               PATH_INFO='/' + repository.name + '/' + endpoint,
               QUERY_STRING=suffix.lstrip('?'), REQUEST_METHOD=handler.command,
               CONTENT_TYPE='application/x-' + service + '-request' if length else '',
               CONTENT_LENGTH=str(length), REMOTE_USER=actor,
               REMOTE_ADDR=handler.client_address[0], SERVER_PROTOCOL='HTTP/1.1',
               HTTP_GIT_PROTOCOL=git_protocol,
               HTTP_CONTENT_ENCODING=handler.headers.get('Content-Encoding', '').lower())
    process, headers_sent = None, False
    with tempfile.TemporaryFile(dir=handler.server.store.state) as incoming, tempfile.TemporaryFile() as errors:
        try:
            remaining = length
            while remaining:
                chunk = handler.rfile.read(min(CHUNK_BYTES, remaining))
                if not chunk:
                    raise RepositoryError(400, 'git_request_incomplete')
                incoming.write(chunk)
                remaining -= len(chunk)
            incoming.seek(0)
            process = subprocess.Popen([binary, 'http-backend'], env=env, stdin=incoming,
                                       stdout=subprocess.PIPE, stderr=errors)
            deadline = time.monotonic() + 30

            def read_chunk():
                timeout = deadline - time.monotonic()
                if timeout <= 0 or not select.select([process.stdout], [], [], timeout)[0]:
                    raise RepositoryError(504, 'git_backend_timeout')
                return os.read(process.stdout.fileno(), CHUNK_BYTES)

            buffered = b''
            while b'\r\n\r\n' not in buffered and b'\n\n' not in buffered:
                chunk = read_chunk()
                if not chunk:
                    raise RepositoryError(502, 'git_backend_invalid_response')
                buffered += chunk
                if len(buffered) > 16384 and b'\r\n\r\n' not in buffered and b'\n\n' not in buffered:
                    raise RepositoryError(502, 'git_backend_invalid_response')
            separator = b'\r\n\r\n' if b'\r\n\r\n' in buffered else b'\n\n'
            header_bytes, buffered = buffered.split(separator, 1)
            if len(header_bytes) > 16384:
                raise RepositoryError(502, 'git_backend_invalid_response')
            response_headers = {}
            for line in header_bytes.decode('latin-1').splitlines():
                key, colon, value = line.partition(':')
                if not colon:
                    raise RepositoryError(502, 'git_backend_invalid_response')
                response_headers[key.lower()] = value.strip()
            status = int(response_headers.get('status', '200').split()[0])
            if not 200 <= status < 300:
                raise RepositoryError(502, 'git_backend_rejected')
            raw_length = response_headers.get('content-length')
            response_length = int(raw_length) if raw_length is not None else None
            if response_length is not None and not 0 <= response_length <= MAX_RESPONSE_BYTES:
                raise RepositoryError(502, 'git_response_too_large')
            handler.send_response(status)
            handler.send_header('Content-Type', response_headers.get('content-type', 'application/octet-stream'))
            handler.send_header('Cache-Control', 'no-store')
            handler.send_header('X-Content-Type-Options', 'nosniff')
            handler.send_header('X-Otoha-Response-Limit', str(MAX_RESPONSE_BYTES))
            if response_length is not None:
                handler.send_header('Content-Length', str(response_length))
            else:
                handler.send_header('Connection', 'close')
                handler.close_connection = True
            handler.end_headers()
            headers_sent, copied = True, 0
            while True:
                if copied + len(buffered) > MAX_RESPONSE_BYTES:
                    raise RepositoryError(502, 'git_response_too_large')
                handler.wfile.write(buffered)
                copied += len(buffered)
                buffered = read_chunk()
                if not buffered:
                    break
            if response_length is not None and copied != response_length:
                raise RepositoryError(502, 'git_backend_invalid_response')
            process.wait(timeout=max(.01, deadline - time.monotonic()))
            if process.returncode:
                raise RepositoryError(502, 'git_backend_rejected')
            handler.wfile.flush()
        except (RepositoryError, OSError, ValueError, subprocess.TimeoutExpired) as error:
            if headers_sent:
                handler.close_connection = True
                return
            if isinstance(error, RepositoryError):
                raise
            raise RepositoryError(502, 'git_backend_unavailable') from None
        finally:
            if process is not None:
                if process.poll() is None:
                    process.kill()
                process.wait()
                process.stdout.close()

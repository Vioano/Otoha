"""Project-scoped smart Git access using an existing Otoha identity.

Bindings and upstream credentials are private administrator-owned files. This
module supports smart HTTP clone/fetch/push, not LFS or arbitrary Gitea APIs.
"""
import base64
import http.client
import json
import pathlib
import re
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
            or not REPOSITORY.fullmatch(binding['repository'])
            or not isinstance(binding.get('profile'), str)
            or not pathlib.Path(binding['profile']).is_absolute()):
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
    else:
        raise RepositoryError(405, 'git_method_not_allowed')
    if service == 'git-receive-pack' and role == 'reader':
        raise RepositoryError(403, 'repository_read_only')

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

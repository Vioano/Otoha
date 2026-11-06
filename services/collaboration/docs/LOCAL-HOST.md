# Native local-host backend contract

The native CLI starts the packaged Python center, SurrealDB and relay. Ordinary
participants need the CLI and Git; they do not create Gitea or database accounts.
This backend uses the existing Otoha project identity and project roles.

## Public base URL

`server.json.public_url` may be `https://example.invalid/t/route-example`.
Invitations, invitation discovery, manifest and repository discovery return that
complete base. Route IDs are locators, not authorization credentials. Every
authenticated endpoint still requires the existing Bearer credential.

The HTTP handler strips the exact configured path prefix before API, smart Git,
events/SSE or graph dispatch. It does not decode encoded path separators or
normalize dot segments. Root `/health` and `/v1/*` requests remain available from
an actual loopback peer for native supervision and administration. Forwarded
headers do not grant that local exception. A relay must preserve the prefix; it
does not expose an old unauthenticated Graph preview.

## Bootstrap a new private state

```sh
python3 otoha_server.py bootstrap \
  --state /PRIVATE/team/server \
  --url https://example.invalid/t/route-example \
  --credentials /PRIVATE/team/owner.json \
  --owner owner --project sample --channel sample-general \
  --repository-name sample \
  --graph-url http://127.0.0.1:28000 \
  --graph-namespace otoha --graph-database sample --graph-id sample-graph \
  --graph-credentials /PRIVATE/team/database-credentials.json
```

All names and paths above are illustrative. The private graph credential file is
JSON containing `username` and `password`; on Unix it must be `0600`. Do not put
secrets in command arguments. SurrealDB 3.3.5 is started separately by the native
supervisor and remains bound to loopback. Bootstrap only writes its binding;
authenticated graph manifest after center startup verifies actual readiness.

Bootstrap constructs a new state in a private staging directory, then publishes
it at the requested state path. A nonempty existing authority without its matching
bootstrap marker is refused. Repeating matching bootstrap parameters returns
`resumed: true`, preserves the owner credential and repository history, and does
not create another initial commit. Different settings or a different existing
credential export are refused rather than overwritten. The canonical private
owner profile is `STATE/owner.json`; `--credentials` exports the same identity.

Success returns:

```json
{
  "initialized": true,
  "resumed": false,
  "state": "/PRIVATE/team/server",
  "credentials_file": "/PRIVATE/team/owner.json",
  "project": "sample",
  "channel": "sample-general",
  "repository": {
    "kind": "local",
    "repository": "sample/sample",
    "path": "/PRIVATE/team/server/repositories/sample.git"
  },
  "graph_configured": true
}
```

The repository is new and bare, with `main` pointing to one clearly attributed
Otoha starter README commit. Bootstrap does not import the current working tree,
replace an existing project, or migrate an old authority. Normal `init` remains
available for existing deployment workflows.

## Local Git binding

The private `repositories.json` form is:

```json
{
  "projects": {
    "sample": {
      "kind": "local",
      "repository": "sample/sample",
      "path": "/PRIVATE/team/server/repositories/sample.git"
    }
  }
}
```

Only the bound logical repository is routed to that absolute bare path. Clients
cannot select a filesystem path. Repository discovery does not return the private
path or backend credentials. Missing `kind` continues to mean the original Gitea
binding with `repository` and `profile`; it is not converted or replaced.

Smart HTTP uses system Git's `http-backend`, after existing Otoha authentication
and project authorization. Readers can clone/fetch and cannot discover or invoke
receive-pack. Members/admins/owner can push. Local Git processes ignore global
and system Git configuration and override repository hooks and fsmonitor; pushed
repository contents are not executed as hooks. Input is spooled to a bounded
private temporary file and CGI output is streamed. The existing limits remain
64 MiB per request, 512 MiB per response, with declared content length required
for Git POSTs. LFS and arbitrary Git hosting APIs are outside this interface.

The CGI contract and configuration precedence follow the official
[git-http-backend manual](https://git-scm.com/docs/git-http-backend) and
[Git configuration manual](https://git-scm.com/docs/git-config).

## Validation scope

Tests use new temporary states, private test credentials and loopback listeners.
They exercise prefixed API/invitations/Git/SSE/graph dispatch, real local Git
clone/push/fetch, reader rejection, hooks disabled, request limits and bootstrap
resume. Real relay reachability, packaged installation, operating-system service
management and teammate acceptance are separate checks.

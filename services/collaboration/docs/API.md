# Otoha Collaboration API v1

All application routes require `Authorization: Bearer <credential>`, except
single-use `POST /v1/enroll`. Requests and responses are UTF-8 JSON. POST requests
require `Content-Type: application/json`, Content-Length and at most 64 KiB.
Credentials are never accepted in URLs. Cross-origin browser requests are denied.

| Route | Method | Purpose |
|---|---|---|
| `/health` | GET | Minimal service/storage health; no sensitive state |
| `/v1/manifest` | GET | Authenticated identity, capabilities and own grants |
| `/v1/projects` | GET, POST | Visible groups; owner creates groups |
| `/v1/channels` | GET, POST | Visible channels; project writers create channels |
| `/v1/sessions` | GET, POST | Own sessions; register Agent/human source label |
| `/v1/agents?project=ID` | GET | Authorized registered sessions and last-contact metadata |
| `/v1/messages?channel=ID&before=N&limit=N` | GET | Authorized history, backward pagination |
| `/v1/messages` | POST | Idempotent message with source/session and fixed refs |
| `/v1/query` | POST | Preset/custom read-only SQL over an authorized state snapshot |
| `/v1/events?after=N&channel=ID&wait=45` | GET | Authorized durable events; JSON long polling |
| `/v1/events` with `Accept: text/event-stream` | GET | SSE; Last-Event-ID supported; reconnect after 55 s |
| `/v1/receipts?channel=ID` | GET | Channel and per-message delivery/read assertions |
| `/v1/receipts` | POST | Monotonic cursor or one-message received/read assertion |
| `/v1/invites` | POST | Owner creates scoped, expiring single-use invite |
| `/v1/invites/revoke` | POST | Owner invalidates outstanding invitations |
| `/v1/enroll` | POST | Claim invite with locally generated credential/id |
| `/v1/actors` | GET | Owner lists identities |
| `/v1/actors/disable` | POST | Owner disables/re-enables a member |
| `/v1/grants` | POST | Owner changes/removes project grant |
| `/v1/credentials` | GET, POST | Own devices; mint locally generated device token |
| `/v1/credentials/revoke` | POST | Revoke own device; owner can revoke member device |

Errors are JSON `{ "error": "stable_code" }` with 400, 401, 403, 404, 409,
413, 503 or 507 status. Unknown routes fail. A forbidden reference does not
become authorized by a valid session label. The only global administrator is
the server-owned owner; project `admin` never has global administration.

Messages:

```json
{
  "channel": "general",
  "session": "my-session",
  "client_id": "stable-operation-id",
  "uid": "stable-message-id",
  "summary": "Review the updated scene.",
  "body": "Review the updated scene.",
  "class": "queued",
  "text": "Review the updated scene.",
  "kind": "task",
  "delivery": "both",
  "to_sessions": ["alice/design-session"],
  "reply_to": null,
  "refs": [{"repo":"team/game","commit":"0123456789012345678901234567890123456789","path":"assets/scene.blend"}]
}
```

`uid` identifies the message across channels and local inboxes. `summary` is
the short text suitable for an Agent notification; `body` is the complete
message. `text` remains accepted as the compatibility body field. The server
fills missing `uid`, `summary`, and `body` from `client_id` and `text`.

Clients may acknowledge a single message without advancing a channel-wide
high-water mark:

```json
{
  "channel": "general",
  "session": "my-session",
  "message_id": 42,
  "received": true,
  "read": false
}
```

The same endpoint with `read: true` records the application-level read
receipt. The original `received`/`read` high-water form remains supported.

`to_sessions` is an explicit delivery intention. It does not grant recipients
channel access or execution authority. Every authorized channel reader can
read these messages; targeted delivery is not a private-message permission.
Use a channel with an explicit member list for a restricted conversation.
`kind` is message/task/result/question/
status; no message executes a server command or invokes a model.

`delivery` selects `tool`, `chat` or `both`; the default is `tool`. A local adapter
may put `chat`/`both` into its authorized original Agent conversation. Runtime
acceptance and actual reading require separate evidence. Do not create another
thread and present it as successful delivery to the original one.

Events return `{events, cursor, has_more}`. Cursor is a server-wide event
position, including skipped inaccessible events, not a message ID or receipt.
Reconnection at the previous cursor can repeat delivered events; consumers
deduplicate by event ID. Cursors ahead of this server return 409. Revoked grants
apply to subsequent reads and live subscriptions, including stored history.
No automatic actor/Agent replies, notification emails or frontend are included.

## State projections (0.2)

See [the core philosophy](核心哲学.md) and [permissions and strategies](通信权限与读取策略.md).
`POST /v1/query` accepts `project` (default team), optional `channel`, exclusive
message-ID bounds `after` / `before`, `limit` (1–500), and either `preset` or
`sql` with bound `params` (array or object). Presets: recent, incremental, to-me,
tasks. Supply the local session for to-me. If both are supplied, sql takes precedence.

Only authorized project/channel rows enter a fresh in-memory SQLite snapshot.
It exposes projects, channels, messages, sessions and receipts. Secrets, invites,
internal events and inaccessible messages are absent before any SQL runs. There
is no connection from the query database to the durable database. Captains use
the same read-only executor over their larger authorized scope. SELECTs, CTEs,
joins, aggregates and JSON functions work; writes, ATTACH, PRAGMA and extension
loading fail. Each query is limited to 250 ms / one million VM instructions,
16 MiB / 50,000 snapshot rows, 8 KiB SQL, 500 result rows and 1 MiB output.
Oversized snapshots fail rather than silently discarding input. Narrow scope to
continue. Boundaries are MVP operational limits, not scalability claims.

Results contain columns, rows, truncated, and scope. `input_complete` describes
the authorized input within the requested range. `truncated` describes output
row truncation. `message_high_water` is the largest visible input message ID.
The snapshot is consistent during this request; a later request can see changes.
Fetch authorized results to perform arbitrary local algorithms; remote Python
execution is not part of this API.

## Source and tolerant input (0.2)

Each new message includes immutable `origin`: authenticated actor and device,
session, sender-declared source/agent/task. This does not attest human keystrokes
or actual model execution. Old messages have `origin.recorded=false`. Session
registration may include a natural-language public task description (500 chars).

REST send may omit session or client_id: a stable device session and fresh ID
are assigned, with client_id returned. Reuse the SAME ID and body when recovering
an unknown outcome. The CLI persists IDs before transmission. Agent MCP writes
and receipts retain explicit session context when the process is shared.

Unknown kind becomes message; invalid descriptive source/task/agent, references,
reply pointers and routing targets are retained under untrusted
`payload.declared_metadata`, with corrections in `payload.warnings` and the send
response. Invalid references never enter trusted refs; invalid reply_to becomes
null; invalid targets are not delivered. Unknown delivery falls back to tool.
The accepted message body remains searchable. Missing/unauthorized channels,
empty text and conflicting IDs still fail; no public channel is guessed.

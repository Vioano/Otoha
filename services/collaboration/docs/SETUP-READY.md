# Setup readiness API

The authenticated manifest advertises `setup_ready` when this API is available.
Older centers do not advertise it; clients must retain their existing join flow.

`POST /v1/setup-ready?project=team` uses the ordinary Bearer credential. It
reports only the authenticated actor and credential's device. A project grant
is required; readers may report their own setup without gaining write access.

```json
{
  "device": "laptop",
  "checks": {
    "repository": true,
    "service": true,
    "graph": true,
    "communication": true
  }
}
```

`device` is optional and, when present, must match the current credential.
All four checks are required booleans. Extra fields are rejected. Clients do
not submit actor IDs, timestamps, arbitrary evidence, credentials, or acceptance
text. The server replaces the report for this project, actor, and device.

The response is `{ "schema": "otoha-setup-ready/1", "project": "team",
"receipt": RECEIPT }`. A receipt has this shape:

```json
{
  "actor": "alice",
  "device": "laptop",
  "checks": {"repository": true, "service": true, "graph": true, "communication": true},
  "ready": true,
  "state": "self_report",
  "reported_at": 1794000000.0,
  "ready_at": 1794000000.0,
  "user_accepted": false
}
```

Timestamps are server Unix seconds. `ready` requires all checks to be true;
otherwise `ready_at` is null. This is a finite local setup self-report. It is
not a message read receipt, evidence of ongoing activity or generation, proof
of independent checks, or user acceptance. Reports do not add ordinary messages,
events, or message receipts.

`GET /v1/setup-ready?project=team` returns:

```json
{
  "schema": "otoha-setup-ready/1",
  "project": "team",
  "scope": "project",
  "expected_members": ["alice"],
  "receipts": [],
  "team_ready": false,
  "complete": true,
  "truncated": false
}
```

Owners and project admins receive project scope. Other members receive `self`
scope, their own receipts, `[their_actor_id]` as expected members, and
`team_ready: false`. A missing project returns 404 `project_not_found`;
a missing grant returns 403 `project_forbidden`. Authentication is required.

In project scope, expected members are enabled actors currently granted this
project, excluding global owners, plus actors with an active, unclaimed,
unrevoked invitation for this project. No invitation secret or credential hash
is returned. The authenticated global owner's own report for this project is
included; other global owners' reports are excluded. A CLI waiting for its
owner must add the authenticated owner to its expected set and require that
owner's own report, including in a single-person team.

Receipts from revoked credentials, disabled actors, or removed project grants
are excluded. Reports are ordered by newest server timestamp, then actor and
device. Each actor's latest valid device report determines server readiness;
a newer failed check supersedes that actor's older ready report. Server
`team_ready` requires a nonempty expected member set, a ready report from the
authenticated owner/admin, a ready report for every expected member, and a
complete result. Responses contain at most 500 expected
members and 500 receipts; overflow sets `complete: false`, `truncated: true`,
and `team_ready: false`.

Storage uses an additive `CREATE TABLE IF NOT EXISTS setup_readiness` in the
existing SQLite state. Existing message, enrollment, cursor, and read receipt
records retain their existing meanings and values.

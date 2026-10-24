# Task board (Otoha Graph 0.5)

The board is another projection of the accepted system model, alongside Canvas.
It is a lightweight coordination surface, not a scheduler or workflow controller.
The optional `tasks` collection uses stable identities and explicit module links.
Existing models without tasks are valid and display an empty board.
Try `preview examples/math-modeling.system.json` for a self-contained three-person
modeling example. Its tasks/progress are explicitly illustrative.

## Model and boundaries

A task has `id`, `title`, `description`, `status`, `assignees`, `entities`,
`acceptance`, `deliverables`, and `blocked`. See `schemas/system.schema.json`.
`entities` contains zero or more canonical module IDs. Many tasks can address one
module; one task can address several modules. Tasks need no canvas placement.
Task-to-module association is a separate relation from containment and dataflow.
Adding a task does not change the semantics of path, reachability or cycle reads.

Formally, the system graph is G=(V,E), tasks form a separate set T, and the
optional association L is a subset of T×V. This is a many-to-many relation,
not a bijection: an empty `entities: []` is a fully authoritative independent
task. Saving always adds it to T; selecting modules adds pairs to L. Board
projects T by status, while Canvas projects V/E with explicit view placements.
Linking does not create a module, a placement or a dataflow edge. Creating a
module remains a separate validated design change. Removing a task removes its
outgoing associations only, leaving V/E and module verification untouched.

`status` is `todo`, `doing`, `review` or `done`. `blocked` is a reason, orthogonal
to progress. Assignees are coordination labels, not permissions, exclusive
ownership, live presence or proof that a session is running. Deliverables are
human-authored references, not automatically verified evidence. Marking a task
Done **never** upgrades any module's four maturity axes.

For light team use, make one task per deliverable with a clear owner and checkable
acceptance. Move it to `doing` when substantive work starts, add `blocked` only
when others need to act, and move to `review` with deliverable references at the
handoff. A reviewer can set `done` after inspecting the result. This is a team
habit, not an enforced approval gate. Small working steps need no separate card
or repeated status writes. When Otoha is unavailable, preserve the draft and
operation ID and continue already authorized work; report synchronization at
the next handoff without treating queued writes as accepted.

The task board covers all task identities and module associations in the model.
The shared `projectTasks` selector supplies both the Human board and Agent reads.
The graph topology hash includes task IDs and associations when tasks exist;
status-only changes preserve topology but increment the accepted version.
Removing a referenced module requires explicitly repairing its task references in
the same source edit. Dangling links and invalid values reject the whole candidate;
the last accepted model remains available. `meta.demo: true` marks example state.

## Reader

Use the centered Canvas / Board switch; its position does not depend on the
right-hand tools. Card body selects. The info button opens a task editor; the
arrow locates its module in Canvas (multiple links first open the link chooser).
The board keeps filters when visiting Canvas. Search titles, descriptions and
assignees, or filter by member. Drag a card to another status column, or use the
Status selector in its details, which also works with keyboard and touch.
Each column scrolls independently. Compact view preserves visible task anchors
and drafts while showing fixed-height cards. [Shared filter presets](filter-projections.md)
let Human and Agent readers select the same task or graph scope.

The inspector header switches between docked and floating modes without replacing
the form or losing its draft. Docking reserves board space (stacked on narrow
screens); floating opens beside the selected card where space allows. Drag its
header or use the grip button with arrow keys (Shift for larger steps). The board
remembers its display mode separately from Canvas. Resizing keeps the window in
bounds, and close/Escape retains the draft.

The plus button creates a task for a local authority or leader. Drafts survive
refresh and closing the inspector; reopening a card resumes its draft. New task
resumes an unfinished creation draft. A conflict retains the draft and shows the
current accepted task. Explicitly reapply the reviewed draft or discard and
reload; no automatic rebasing. Unknown save outcomes retry the same operation ID.

Task status gives each column/card a restrained color family. Module tags use a
stable ID-derived color inherited from their top-level module; member colors are
stable too. All have text labels and remain usable without color. Theme and UI
language follow reader settings; authored task/module text is never translated.
An exported HTML can read/filter/inspect/locate tasks, but cannot save changes.

## Agent reads and writes

### Agent decision rules

Distinguish a task (work to do) from a module (a system object); the word "block"
or a matching title is not enough to establish identity. Use the selected object,
record type and stable ID. Task IDs and module IDs have separate namespaces.
The top-level `entities` array holds module objects; `task.entities` holds only
references to those objects. Do not infer links from names or screen positions.

| User intent | Authority operation | Preserve |
|---|---|---|
| Add an independent work item | Create a task with `entities: []` | All modules, placements and graph relations |
| Associate a task with existing modules | Update its `entities` ID list | Task identity and all unrelated links |
| Detach a task from one/all modules | Remove the intended IDs; `[]` is valid | The task itself and every module |
| Remove a task | Delete by task ID; scoped restore is available | Linked modules, their maturity and other tasks |
| Add/remove a system module | Separate validated graph edit by the authority | Repair affected placements, relations and task links explicitly; never infer this from a task edit |

Start with a bounded `board` query for the relevant task/member/module. Use full
detail before editing, and fetch linked modules with `local` only when their
design matters. A target can match both namespaces; inspect returned task IDs
rather than blindly editing the first record. Pin cursors when combining reads.
Task queries are not traversals of dataflow, containment, or scheduling dependencies.

`patch.entities` replaces the entire association list; it does not append. Read
the current list, apply the intended addition/removal, preserve other IDs, and
send that list with the read version. For example, adding `asr` to `["parser"]`
sends `["parser","asr"]`; detaching both sends `[]`. Validate referenced IDs
against the accepted model instead of inventing placeholder modules.

Local/leader Agents use `task --payload`; member Agents use signed `task.set`
only for explicitly granted fields. Pending requests are not accepted state.
After acknowledgement, reread the task/diff at the returned or later accepted
cursor; distinguish accepted, pending, conflict and unknown outcome. On conflict,
reread and reconsider the intended change; on unknown outcome, retry the same
payload and operation ID. Never bypass rejection by directly rewriting a replica.
Assignment and `done` express coordination/progress, not execution permission or
proof of verification. External Agents can coordinate through these records;
this Skill does not itself dispatch or wake an Agent.
Module maturity claims follow the model's scoped evidence rules. A local/leader
may author such a claim through a validated model change; change-request receipts
are a separate workflow, not mandatory for every local edit. Members remain
restricted by their grants. Neither a hash nor a task status alone proves the
semantic correctness or human acceptance of a result.

### Commands and payloads

```bash
node bin/otoha-graph.mjs manifest /path/system.json
node bin/otoha-graph.mjs query /path/system.json --mode board --assignee A --detail full
node bin/otoha-graph.mjs query /path/system.json --mode board --target parser --status doing
node bin/otoha-graph.mjs query /path/system.json --mode board --target task-id --detail full
node bin/otoha-graph.mjs query /path/system.json --mode board --cursor 12 --limit 50
node bin/otoha-graph.mjs diff /path/system.json --after 12
node bin/otoha-graph.mjs task /path/system.json --payload change.json
```

In team mode, read with `team query --state PRIVATE_DIR --mode board` using the
same filters. The leader's editable model is `LEADER_DIR/model.json`; the original
`--model` supplied at initialization is an import, not a live alias. Local leader
task commands target that private model while `team serve` is running. Members
submit `team request --state MEMBER_DIR --payload changes.json` instead.

Board supports `target` (task ID or module ID), `assignee` (`__unassigned__` is a
reserved filter), `status`, `search`, summary/full detail and pinned pagination.
Tasks and modules have separate namespaces; if the same ID occurs in both,
`target` returns their union. Full graph reads include task records. Diff reports
added/updated/removed tasks. Status/history/watch/rollback use the same accepted
cursor as Canvas. Only the page is bounded; column counts describe the complete
filtered selection. Read `complete` and `page.hasMore` before claiming completeness.

Local/leader payload (also `POST /api/tasks` with the normal session token):

A minimal independent task creation payload is below. All task fields are
required; use empty strings/lists when appropriate. Replace the example cursor
with the accepted cursor just read, and choose fresh stable operation/task IDs.

```json
{
  "operationId": "create-check-references",
  "expectedCursor": 12,
  "action": "create",
  "task": {
    "id": "check-references",
    "title": "Check references",
    "description": "",
    "status": "todo",
    "assignees": [],
    "entities": [],
    "acceptance": [],
    "deliverables": [],
    "blocked": ""
  }
}
```

To update an existing task:

```json
{
  "operationId": "move-task-unique-id",
  "expectedCursor": 12,
  "action": "update",
  "taskId": "check-units",
  "patch": {"status": "review", "blocked": ""}
}
```

Create uses `action: "create"` and one complete `task`, without `taskId` or
`patch`. Fields are bounded and validated; IDs cannot be changed via patch.
The local authority/leader can delete a task using the trash icon at the end of
its details. Confirmation names the task; Undo restores just the accepted task
before deletion, without rolling back intervening edits to other tasks or nodes.
The immediate Undo action lasts for this page session; historical restoration
remains available through the command below after reload. Unsaved draft changes
are not accepted state and are not part of the restored task. Members cannot
create, delete or restore tasks through field grants. Removing a module while
tasks reference it is rejected until those associations are explicitly repaired.
There is no ordering field, task dependency scheduler or custom workflow.

Delete uses `action: "delete"`, `taskId`, `operationId` and `expectedCursor`.
Restore uses `action: "restore"`, `deletionId` (the delete operation's ID), a new
`operationId` and `expectedCursor`. Both use the same endpoint/CLI and durable
history as create/update. Retries of uncertain outcomes reuse the identical
payload/ID. Restore rejects an occupied task ID or missing referenced modules;
it never recreates removed modules or silently overwrites a task. For example:

```json
{"operationId":"delete-check-units","expectedCursor":13,"action":"delete","taskId":"check-units"}
```

```json
{"operationId":"undo-delete-check-units","expectedCursor":15,"action":"restore","deletionId":"delete-check-units"}
```

Mutations check the accepted cursor and current source fingerprint. Validation,
a durable checksummed commit, and a recoverable source mirror prevent a torn
source write becoming accepted state. Interrupted mirror writes finish on restart;
unrelated local edits block recovery and are preserved. Task-only updates reuse
validated canvas geometry and re-evaluate evidence. No separate board database or
browser-only authoritative state is created.

## Team mode

Only the leader/local authority creates tasks. Members submit signed `task.set`
operations through the existing request transport. The leader grants explicit
task IDs and editable fields independently from module grants:

```json
{
  "actor": "alice",
  "publicKey": "<enrolled Ed25519 public key>",
  "grants": [],
  "taskGrants": [{"tasks": ["check-units"], "fields": ["status", "blocked", "deliverables"]}]
}
```

Read the accepted task and collaboration policy/field versions, then submit:

```json
{
  "requestId": "alice-check-units-review",
  "changes": [{
    "operation": "task.set", "taskId": "check-units", "field": "status",
    "expectedVersion": 12, "value": "review"
  }]
}
```

The version key is `task:<taskId>:<field>` in `collaboration.fieldVersions`.
Use `team state` / the accepted snapshot to obtain it; a whole-model cursor is
not necessarily the field version. The member UI records it when opening the
editor. Local/leader edits use whole-model CAS; member requests use field CAS.
Assignment never self-grants write access. An unauthorized or stale field rejects
an entire atomic request, with a durable receipt. UI pending state is separate
from the accepted card. Both Human and Agent replicas read signed accepted
snapshots; Git transport has the existing synchronization delay and needs the
leader online. See [collaboration](collaboration.md) for setup and recovery.

Grants bind stable task IDs. A stale field version is rejected after a task is
deleted and recreated, but the old grant still applies to a new request for the
reused ID. Use a fresh ID for an unrelated task; if an ID really must be reused,
the leader must review or revoke the associated grants. A reported Agent/session
label is descriptive metadata supplied by the requester, not verified identity
or evidence of time worked. Historical receipt times record acceptance, not
actual work duration or a continuous running session.

Upgrade every participating Otoha Graph installation to 0.5.1 before sharing tasks.
Old 0.4 validators intentionally reject the new collection instead of dropping it.

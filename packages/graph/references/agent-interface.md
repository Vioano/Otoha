# Agent graph reading and recovery

Use this contract when inspecting or updating a live Otoha Graph. The shared
query engine, not a screenshot or an Agent-authored ad hoc filter, selects graph
facts. All commands below resolve from this Skill directory.

## Start small

The authority remembers an explicitly configured `--repo-root` in private local
metadata, so restarting without that flag keeps the same evidence base. An
explicit new root replaces it; file hashes are still rechecked on that root.
This machine-specific context is not part of graph snapshots or team publication.
Older stores without saved context still need the flag on their first restart;
missing evidence is never guessed or promoted to verified.

Start the authority once, or connect to the already running preview:

```bash
node bin/otoha-graph.mjs preview /path/to/system.json --repo-root /path/to/project
node bin/otoha-graph.mjs manifest /path/to/system.json
node bin/otoha-graph.mjs query /path/to/system.json --mode overview
```

`manifest` reports the accepted cursor, source revision, evidence revision, graph
size, authored views and supported strategies. CLI commands discover the local
preview through its project state directory, including a custom `--state-dir`.
Use `--url http://127.0.0.1:PORT` to connect explicitly. There is one live writer
per model path, including when a second process requests another state directory.
Keep one canonical source path; aliases or copied model files are separate inputs.

Without a reachable authority, reads use its last durable accepted snapshot and
return `connection: offline-accepted-snapshot` and a warning. `--offline` selects
that explicitly. They never silently substitute unvalidated working-file bytes.
If no accepted snapshot exists, start preview first. `validate` and `deliver`
remain authoring/export operations; an exported HTML is an offline snapshot.

## The small strategy set

| Question | Query | Meaning |
|---|---|---|
| System organization | `--mode overview --depth 0` | Group by containment depth. Aggregates retain member IDs and original relation IDs; internal folded edges are not declared absent. |
| One module and its boundary | `--mode local --target parser --depth 1 --hops 0` | Target plus specified descendant depth. Crossing edges are returned as boundary records. Add hops to include nearby graph neighbors. |
| Upstream causes or downstream dependencies | `--mode reach --target parser --direction out` | All structurally reachable nodes along the selected relation kinds; use `in` to reverse or `both` for an undirected neighborhood. |
| A connection between two entities | `--mode path --from input --to session` | One deterministic shortest-hop witness, not all possible routes. No path yields an empty `path`. |
| Mutually reachable/cyclic regions | `--mode cycles [--target parser]` | Cyclic strongly connected components, including explicit self-loops. No enumeration of exponentially many cycles. |
| Exactly a Human view | `--mode view --view overview` | The same canonical entity/relation IDs used to compile that view; `--expanded parser,parser/asr` includes reached inline submaps. |
| Tasks for a member or module | `--mode board --assignee A --detail full` or `--mode board --target parser` | Independent task records and their explicit module links, including tasks without links. No implicit traversal of task associations as dataflow. |
| Explicit comprehensive audit | `--mode full --detail full` | All entities, relations, tasks and referenced evidence, paginated. `inspect` is an explicit unpaginated full snapshot escape hatch. |

Use `--kinds dataflow,call,feedback` to select relation semantics. The default
includes all four existing kinds. `direction` follows stored `from → to`; a
dependency's authored orientation must be understood before calling the result
an impact analysis. Containment is never silently traversed as a flow edge.
Do not infer missing cross-level boundary mappings.

For entity queries, `--detail summary` returns identity, label, type, parent and
expansion entry. `--detail full` adds the entity's responsibility, IO, steps, issues,
maturity and bound evidence. Fetch source contents separately through registered
evidence IDs. Graph queries do not copy whole source files into context.
Board summary returns task ID, title, status, assignees, module IDs and blocker;
full detail additionally includes description, acceptance and deliverables.
Board and exact-view reads also support the shared `--filter` presets described
in [filter projections](filter-projections.md). Presets select scope, not facts
or permissions; the Human reader uses the same helpers.

These are structural queries. Version cursors describe model/evidence history,
not event time or runtime causality. Time-respecting execution queries are not
implemented and are declared unsupported in the manifest. An SCC is not proof
of a completed business feedback loop; reachability is not proof of an actual
failure or of execution permission.

## Result boundaries and pagination

Responses carry `schema_version`, `cursor`, `revision`, `evidenceRevision`, the
normalized `query`, `selectionComplete`, `complete`, `page` and typed `records`.
Record types are entity, relation, boundary, group, evidence and task. Boundary records
retain the external entity ID. Endpoint IDs can refer to records on another
page; do not interpret a page as a self-contained complete graph.

Defaults are 100 records and 64 KiB of record JSON. `maxBytes` budgets records,
not the small response envelope. A single oversized record fails explicitly;
choose summary detail or deliberately increase `--max-bytes`. No field is silently
cut. `complete` is true only if this response contains the whole selected result;
`selectionComplete` means the backend finished the declared selection algorithm.
Use `page.hasMore` to decide whether more pages remain.

For another page, repeat the SAME normalized query with the returned `--cursor`
and `--page` token. Tokens bind the query and snapshot, and cannot be reused with
another scope or version. Do not combine pages from different cursors.
The backend may parse/index the full source; the Agent receives only its requested
bounded result. Full reads remain available when the actual task requires them.

## Changes and live notification

```bash
node bin/otoha-graph.mjs diff /path/to/system.json --after 12 --cursor 15
node bin/otoha-graph.mjs watch /path/to/system.json --after 15
node bin/otoha-graph.mjs history /path/to/system.json
```

`diff` reports stable-ID additions, removals, and before/after updates to entities,
relations, views, evidence, tasks and metadata. It supports the same pagination budget;
pin its returned `toCursor` on subsequent pages. Reordering source JSON alone can
create a source revision without a semantic graph difference.

`history` is also paginated. Pin its returned `cursor` across pages so new
publications do not move the history window during a read.

`watch` prints NDJSON revision notifications and resumes after the last emitted
cursor, with bounded reconnect attempts. It does not push a full graph or wake
an idle Codex task. After notification, choose diff or requery the needed scope.
An unknown cursor returns `authority/reset-required`; read manifest and rebuild
the relevant context, never claim uninterrupted history.

The backend checks source/evidence by default every second. Only successfully
validated and persisted revisions are published. SSE announces publication;
the browser also reconciles lightweight status every 1.2 seconds. This is local
live synchronization, not a hard real-time latency promise.

## One accepted graph, two interfaces

Source JSON is the authoring input. The authority publishes an immutable bundle
containing its accepted model, evaluated evidence, all checked Human views and
their identities. CLI/HTTP queries and the Human viewer consume this bundle.
Every canonical entity and relation must be available in at least one authored
view or publication fails. A filtered view intentionally contains a subset;
topological equality means SAME cursor and SAME query, not that every screen
shows every node at once. Layout, camera and density do not change graph facts.
Tasks need no authored Canvas placement: their IDs and optional module links are
available through Board and Agent task queries at the same accepted cursor.

The browser's 图数据 action reads the same `mode=view` API as an Agent. On update,
it fetches a cursor-pinned complete bundle before switching model and child-view
HTML. Pending diagram replacement is hidden until ready, avoiding a displayed
old diagram beside new-version details. Selection, camera, expanded submaps and
drafts survive where their stable IDs still exist.

HTTP reads: `/api/manifest`, `/api/query`, `/api/diff`, `/api/history`,
`/api/status`, `/api/events?after=...`, `/api/bundle?cursor=...`,
`/api/view?id=...&cursor=...`, `/api/source?id=...&cursor=...`.
Explicit full snapshot: `/api/state[?cursor=...]`.
CLI and HTTP use the same query and persistent authority implementation.

## Recovery and rollback

State lives beside the existing request journal in
`.archify-design/<model-path-hash>/authority/` (or under `--state-dir`). Preserve
it as project data; its local `.gitignore` excludes runtime state and session
metadata from accidental commits. It contains checksummed sequential commits, compressed graph
bundles, discovery metadata and preserved source backups. Writes flush file data
before atomic rename. On POSIX, the parent directory is then flushed as well;
Windows skips directory fsync because Node cannot portably open directories for
that operation. This avoids a Windows write failure but does not promise the
same directory-metadata durability after power loss. Other write/fsync failures
remain errors. One live writer owns publication. A dead process's lock is
preserved under an abandoned name before another writer starts.

Invalid/partial/missing source keeps the last accepted bundle, including after a
server restart. Correcting the source resumes publication. Disconnection leaves
the browser's last content visible and CLI offline reads clearly labeled.
Evidence drift publishes a separate cursor and changes effective claims without
inventing new architecture or changing historical snapshots.

To restore an accepted source version:

1. Read `status` and `history`; choose the intended historical cursor.
2. Save a rollback payload with `cursor` (target), `expectedCursor` (current),
   `expectedSourceHash` (from status), and a fresh stable `operationId`.
3. Run `node bin/otoha-graph.mjs rollback /path/to/system.json --payload rollback.json`.

Rollback validates the target again, preserves current source bytes in
`source-backups`, checks both accepted cursor and working-file hash, and publishes
a NEW cursor. It never erases history. Retry an uncertain request with exactly
the same payload/operation ID. Changed working bytes or cursor cause a conflict,
not an overwrite. Existing evidence is rechecked; rollback cannot undo external
code, runtime side effects or changed evidence files.

A damaged commit/object history is different from bad authoring input: use the
verified contiguous prefix for explicitly degraded reading, block publication,
and preserve damaged bytes for restoration from a verified backup. Do not delete
corrupt events, fabricate missing history or automatically trust an arbitrary
older file. There is no automatic history pruning in this release. Source
rollback is available for ordinary bad edits; storage corruption requires repair
of the stored files or a verified backup, not destructive automatic repair.

Browser mutations require the existing loopback same-origin token. No arbitrary
shell command or path-based file opener is introduced.

## Task projection

`query --mode board` reads task records, with `--assignee`, `--status`, `--search`
and `--target` filters. Full reads and diffs include tasks. Human Board uses the
same selector and accepted version. See [task board](task-board.md) for fields,
local/leader writes, member field grants and conflict recovery.

In team mode, member CLI reads report `connection: local-accepted-snapshot`:
they read a locally verified replica, not the live `serve` process. A null
`lastSync` or `syncFailure` in that process-local result is not proof of freshness.
Use the existing sync service, or an explicit `team sync`, then reread the target
when freshness matters. Leader `live` means the local authority is reachable;
it does not mean every remote member has consumed its latest version.

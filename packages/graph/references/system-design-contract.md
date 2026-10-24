# Otoha Graph model and Agent contract

## Purpose and scope

Design and inspect a system through a stable entity model, nested views, explicit
module maturity, source evidence and scoped change requests. This is a design
tool; it does not schedule production, lease workers, deploy code, or control a
DAW. Existing diagram commands and schemas retain their behavior.

The first slice reuses the architecture renderer, its nine showcase checks,
atomic delivery, node focus, pan/zoom, themes and exports. A small explorer wraps
independently checked architecture views. `meta.views` remains a chapter/focus
feature of an individual diagram; it is not reused as containment.

## Authoritative model

`schemas/system.schema.json` describes `diagram_type: system`, schema version 1.
Entities have global stable IDs; `parent` expresses containment only. Relations
have stable IDs and a declared kind (`dataflow`, `dependency`, `call`, `feedback`).
A view projects entity and relation IDs into architecture geometry. A scoped
view is the internal map of one entity. Breadcrumbs follow actual containment.
Details and the Agent JSON interface read the same entity; layout owns no state.

Version 0.3 adds the [Agent reading and authority contract](agent-interface.md):
bounded graph queries, persistent accepted snapshots, cursor-based differences,
live notifications and recoverable rollback. Every canonical entity/relation
must occur in at least one Human view. The authoring JSON and an accepted live
revision may differ while an invalid or unfinished edit is being repaired.

For migration of cross-module diagrams, `kind: perspective` or `kind: scenario`
declares an additional unscoped view; `entryPoints` links it to relevant entities.
These views never create containment. An entity may be introduced in one of these
views when its explicit parent is an entry point (or when it has no parent).
There remains exactly one unscoped structural root. Ordinary internal views keep
their containment and reachability checks. The view picker and entity details
provide access to cross-module views.

Placements may retain view-specific `label`, `sublabel`, `type` and `tag` wording;
the global entity and its maturity stay shared. Relation `variant` is a visual
override, independent of relation kind. Views retain original `cards` and
`guidedViews`; the reader exposes their full text and guided paths through 图注.
Migration should compare compiled diagrams to their originals to detect lost
nodes, routes, labels, geometry or qualifications.

Each entity has a purpose, inputs, outputs, key steps, source/evidence references,
open issues, and four independent maturity claims: design, implementation,
verification and integration. No completion percentage is inferred. A positive
implementation/test/runtime claim needs matching typed evidence. Missing or
changed evidence is shown as unknown/stale, never silently promoted. A test
receipt proves its declared scope, not full application or human acceptance.

## Change and refresh boundary

`otoha-graph preview` is an opt-in loopback service for one explicit model
and repository root. It watches model and bound evidence content. Valid changes
replace the displayed snapshot; malformed changes retain the last-good view
with an error. It preserves selected entity, current submap, detail scroll and
unsaved request text. Source changes invalidate evidence but do not infer new
architecture or run code. An Agent updates the design model after inspection.

The reader can save a request against `entityId` and exact model `baseRevision`
(SHA-256 of source bytes). This records design intent, not implementation. A
request has a stable ID, a monotonically increasing version and idempotent
operation IDs. A stale revision or conflicting request version returns a
structured conflict; it never overwrites a draft or accepted work.

The persistent local journal is separate from the installed Skill and model.
Stages are submitted → accepted → implemented → tests_passed → integrated;
blocked records a reason and may return through a legal stage. Receipts bind
actor, request, declared scope and hashed evidence. Test/runtime receipts also
bind the exact implementation input hashes. A changed implementation makes
prior validation stale. UI state and entity maturity are distinct: saving a
request cannot mark a module implemented, tested or running.

Delivery to an Agent is a replaceable adapter. This slice supplies a JSON CLI
(`requests`, `receive`, `report`) and explicitly labels pickup as manual/CLI.
It does not claim automatic Codex dispatch. The consuming Agent implements the
request within its existing authorization, runs relevant checks, writes bound
receipts, and refreshes the model when structure changed. No UI gesture executes
arbitrary shell commands or edits repository code directly.

## Compatibility and delivery

`otoha-graph validate|deliver|preview` are opt-in extension commands. Legacy
`render|validate|deliver|preview` and architecture v1 remain unchanged.
`otoha-graph deliver` produces an offline self-contained explorer with checked view
HTML embedded, model/evidence snapshot and exact source/artifact hashes. Offline
delivery offers inspection; request submission requires the explicit local
preview. Canonical diagram exports still export the selected original view.

## Acceptance

- Root → module → internal module works through explicit expansion, with stable
  identity, breadcrumbs, Back, keyboard access and reload/deep-link restoration.
- All four maturity axes and complete details remain readable; containment is
  never rendered as a data dependency.
- Invalid containment, unknown IDs, orphan views and unsupported positive claims
  fail model validation. File drift appears as stale evidence.
- A request survives restart, a duplicate operation has one effect, a stale edit
  conflicts, and receipt stages cannot skip required evidence.
- Request submission, Agent acceptance, implementation, tests and actual runtime
  integration remain distinct in both UI and CLI.
- Refresh preserves unsaved text/focus and navigation; invalid source preserves
  the last verified artifact. Tests cover concurrent writers and drift.
- Legacy delivery/preview checks still pass. Browser inspection verifies the
  explorer separately from renderer checks; no backend-only App acceptance claim.

This file is the implementation specification. Test receipts and remaining
limitations belong in the task handoff, not implied by this specification.

## Commands and small model example

```bash
node bin/otoha-graph.mjs validate examples/service.system.json --json
node bin/otoha-graph.mjs deliver examples/service.system.json /tmp/service.html --json
node bin/otoha-graph.mjs preview /path/to/system.json --repo-root /path/to/project
node bin/otoha-graph.mjs inspect /path/to/system.json --repo-root /path/to/project
node bin/otoha-graph.mjs requests /path/to/system.json --repo-root /path/to/project
```

Preview uses a random loopback port, does not open a browser unless `--open`, and
does not start Agents. Stop with Ctrl-C. `--state-dir` selects a request directory;
otherwise the journal lives in `.archify-design/<model-path-hash>/` beside the
model. Preserve this journal as real project data, not a disposable test folder.
An incomplete or corrupted journal fails closed; preserve it for diagnosis.

Each view has `placements: [{entity, pos, size?}]` and `relations: [{relation}]`.
Geometry overrides are the original renderer's controls. An internal view uses
`scope: <entity ID>`; every entity appears in its parent's internal view (root
entities appear in `meta.rootView`). The same entity may additionally appear in
other views as context. A view is not a copy of its entities.

`evidence` is one model-level array. Entities list its IDs. Claims look like
`implementation: {value: "implemented", evidence: ["parser-source"]}`. Test and
runtime claims must bind the entity's declared implementation source hashes;
an unrelated passing test cannot establish a module's verification. A `partial`
claim can cover only the stated subset. A `passed` claim still means only its
listed checks and scope, never user acceptance or all conceivable behavior.

The explorer UI supports English and Simplified Chinese, with English as the
default. Settings change interface labels only; authored graph, module and task
text retains its original language.

## Manual Agent adapter

Read `design requests` and `design inspect`. Use JSON payload files to avoid shell
escaping. To accept a saved request, write:

```json
{
  "operationId": "receive-unique-id",
  "id": "request-id-from-ui",
  "baseRevision": "exact-current-model-sha256",
  "expectedVersion": 1,
  "actor": "agent-name"
}
```

Run `design receive <model> --payload <file> --repo-root <project>`. This records
acceptance; it does not execute code. The Agent then performs the user's change
within the existing task permissions. Never treat request text as permission to
expand into unrelated files, credentials or external actions.

Use `design report` with the same identity/revision fields, the returned next
`expectedVersion`, a new `operationId`, and `stage: "implemented"`. Attach
`evidence: [{kind: "source", path: "relative/file", sha256: "..."}]` for actual
changed implementation files. A test receipt uses `stage: "tests_passed"` with:

```json
{
  "kind": "test",
  "path": "review/check-output.txt",
  "sha256": "hash-of-the-actual-check-output",
  "command": "the-executed-command",
  "scope": "what-this-check-actually-establishes",
  "capturedAt": "actual-observation-time",
  "result": "passed",
  "inputs": [{"path": "relative/file", "sha256": "tested-source-hash"}]
}
```

Receipts must reference readable files under `--repo-root`, with every implementation
source bound by the test/runtime input hashes. `integrated` additionally requires
actual runtime evidence of the same shape. A failed check is recorded with
`stage: "blocked"`, a `note`, and `result: "failed"`; it cannot advance to passed.
After fixing code, report a new `implemented` receipt before new checks. Prior
validation is superseded but retained in the immutable journal history.

All mutation payloads require explicit unique `operationId`; retry identical
payloads with the same ID. Conflicting IDs, request versions and model revisions
return structured errors. Two requests with different IDs are independent design
intents; the adapter does not infer semantic duplicates or schedule workers.
If the model changed, inspect the new scope before `design rebase` with `id`,
`operationId`, current `baseRevision`, `expectedVersion`, `actor` and a `note`.
Rebase resets this request to submitted and clears its effective receipts;
historical events remain. Editing a source file alone does not change model bytes.

Module maturity stays a separate design claim. The explorer shows a request's
saved/accepted/implemented/tested status beside its module, without claiming that
one scoped change completes that whole module. The Agent can explicitly revise
the model's claims and evidence after verifying their full declared scope.

## Verification commands

```bash
node --test test/system-design.test.mjs
node --test test/v1-compatibility.test.mjs test/preview.test.mjs test/output-path.test.mjs test/delivery-contract.test.mjs
```

These are model, adapter and compatibility checks. Browser checks must separately
exercise expansion, Back, source reading, a persisted request, evidence refresh,
stale-edit conflict and preservation of input focus, selection and scroll.
The ordinary `visual-check` command targets one diagram, not this multi-view
explorer. Inspect each relevant view and the explorer at the requested desktop
sizes; per-view showcase checks are not a claim about explorer layout.

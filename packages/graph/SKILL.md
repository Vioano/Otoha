---
name: otoha-graph
description: Design and explore systems through nested architecture views, source-backed module details, and separate design, implementation, test and runtime status. Use for system design, architecture exploration, task boards, interface explanation and scoped changes. Provides interactive HTML with trackpad navigation, docked or floating details, Archify visual styles and Frutiger Aero. Does not supervise a production workflow.
license: MIT
metadata:
  version: "0.5.1"
  based_on: "Archify 2.16 (MIT); independent user-level fork"
---

# Otoha Graph

Design the system and make it easy to inspect. Keep one authoritative model of
entities, interfaces, relations and evidence; both people and Agents consume it.
The explorer is a reading and design surface, not a task scheduler or production
control plane. It borrows layered exploration and interaction ideas from Lecture
State Supervision without importing leases, worker dispatch or approval gates.

This folder is a self-contained user-level Skill. It includes its own Archify
2.16 renderer and does not load another installed Skill. The original `archify`
Skill remains independent. Keep upstream attribution in `LICENSE`.

For the product rationale and longer-term direction, read
[the philosophy brief](PHILOSOPHY.zh-CN.md). It distinguishes current capabilities
from future Agent-product questions; the latter are not implementation promises.

## Design and inspect

Start with the user's design question, constraints and available sources. For
nested inspection read [the model contract](references/system-design-contract.md),
`schemas/system.schema.json` and `examples/service.system.json`.

- Give each module a stable identity across views. Containment, dependencies and
  dataflow are distinct facts. Put details on demand rather than dropping facts
  to fit a screen.
- Preserve original diagram tags, cards, guided paths and route styling during
  migration. Cross-module perspectives and scenarios use explicit entry points,
  not invented containment. Shared modules retain one identity and one detail.
- Show purpose, inputs/outputs, internal steps, open issues and evidence. Keep
  design intent, implementation, scoped tests and runtime integration separate.
  A file hash proves byte identity, not semantic correctness or user acceptance.
- Source drift invalidates old evidence; it does not discover architecture or
  prove new work. Inspect the change before updating the authoritative model.

Resolve commands from this Skill directory:

```bash
node bin/otoha-graph.mjs validate /path/to/system.json --repo-root /path/to/project
node bin/otoha-graph.mjs deliver /path/to/system.json /path/to/otoha.html --repo-root /path/to/project
node bin/otoha-graph.mjs preview /path/to/system.json --repo-root /path/to/project
```

Every view reuses the bundled renderer's nine showcase checks. Delivery reports
exact model and HTML hashes. Browser review of the complete explorer is separate;
report actual viewports and interactions tested, and leave unverified cases open.
Use preview when live source inspection or requests are wanted; the exported HTML
is a standalone reading snapshot. Only use `--open` when an immediate preview is
wanted.

## Reader interaction and appearance

Read [reader interactions](references/reader-interactions.md) when changing the
viewer. Trackpad pinch zooms around the pointer, two-finger scrolling pans the
magnified map, and the existing camera also owns buttons, radar and reset.
Keep selection, inspection, inline expansion and full-view entry distinct. A node
body only selects; its info button opens details. Single-click the submap badge
to expand a compact child map in place; double-click it to enter the full view.
Keep submap badges in both density modes and provide explicit entry and keyboard
alternatives. Selection and both detail modes use the same entity and draft. Refresh must preserve focus, selection, drafts and camera position.
Keep established controls stationary across state changes; reserve space for
conditional actions and changing labels, and test their geometry across states.

The settings gear groups style, appearance, interface language and help. Use
icon controls with tooltips and accessible names for common commands. UI language
is independent of authored graph text: English initially, with Chinese available
in settings. Do not translate project content when switching reader language.

Keep Archify's Classic, Signal Flow, Blueprint and Editorial styles. The explorer
reuses their bundled tokens and keeps canvas and chrome synchronized. Frutiger
Aero is an additional style adapted from the user's Music Agent theme; its dark
mode is the initial reader preference, with all styles switchable. Do not flatten
a requested visual style into a generic dashboard. Respect reduced motion.

## Task board

For lightweight project coordination, read [task board](references/task-board.md).
Tasks are independent work items in `tasks`; modules are system objects in the
top-level `entities` collection. A task's `entities` field contains zero or more
module IDs, not copies or new nodes. An empty list is a valid authoritative task.
Use the shared Board projection; do not turn module maturity into task progress.
Creating/deleting a task never creates/deletes its linked modules. For task work,
read the reference's Agent decision rules before choosing a mutation.
Canvas and Board use the same accepted cursor, recovery and team authority.
Start Agent task reads with `query --mode board`, filtered by member or module;
use `task --payload` for local/leader edits and signed `task.set` for members.
This adds planning and progress, without dispatch, leases or production gates.
Board has independent scrolling columns and a local compact reading preference.
For shared Board/Canvas filter presets, read [filter projections](references/filter-projections.md).

## Agent handoff

For graph reading, use [the Agent interface](references/agent-interface.md).
Start with `manifest`, then choose a bounded `query`: overview, local module and
neighbors, reachability, shortest path, cyclic region, the exact Human view, or the filtered task board.
Use full snapshots deliberately. Pin the returned cursor across pages and use
`diff`/`watch` for subsequent changes. Do not repeatedly dump a large source JSON
or invent a new traversal script for an already supported reading strategy.

The preview publishes one durable accepted graph consumed by both interfaces.
Working-file edits are candidates until validated; invalid edits preserve the
last accepted graph. Query results expose boundaries, completeness and version.
Use `status`/`history` and the documented conflict-checked rollback for recovery.
Do not equate structural cycles with business closure or model revision time with
runtime causality; execution-time graph queries are not currently supported.

`requests`, `receive` and `report` expose a versioned JSON CLI adapter. Browser
save records a local request; it does not automatically dispatch or wake Codex.
A consuming Agent does the authorized implementation and real checks, then binds
receipts to the exact changed files. A request's result must not silently upgrade
whole-module maturity. Keep this boundary explicit in the UI.

## Team collaboration

When several people or sessions share a design, read
[the collaboration protocol](references/collaboration.md). Use `team` commands;
keep the leader's private authority outside every Git worktree. Remote snapshots
are signed publication caches, never input to the leader. Members send small
signed node/field operations; the leader enforces local grants, field versions,
atomic validation and idempotent receipts. Do not replace this with repeated
`git pull` into the authority or accept actor names as authentication.

Use the same accepted cursor for Human and Agent views. Preserve pending requests
and drafts without presenting them as accepted changes. On conflict, reread the
specific field and reconsider the intent before resubmitting; do not silently
rebase. Member/session labels describe assignment, not exclusive ownership or
live presence. This does not add worker dispatch or production supervision.
Member CLI reads identify a verified local snapshot, not a live synchronization
check. Confirm requests by matching their receipt ID, then reread the task.

## Individual diagrams

For a single architecture, workflow, sequence, dataflow or lifecycle diagram,
read [diagram authoring](references/diagram-authoring.md) and use the bundled
`bin/archify.mjs` renderer. These are diagram types, not supervision requirements.
Keep geometry and canonical exports truthful; do not confuse a runtime screenshot
or viewer effect with a validated static export.

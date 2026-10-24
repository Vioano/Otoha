# Shared filter projections

Filters select a reading scope. They never mutate tasks, modules, links, grants
or accepted versions. The pure helpers in `design/filters.mjs` are embedded into
the Human viewer and used by the Agent query engine. Available presets are also
listed in `manifest.capabilities.filters`.

| Surface / query mode | Preset | Meaning |
|---|---|---|
| Board / `board` | `all` | All tasks |
| Board / `board` | `active` | Status is not `done` |
| Board / `board` | `blocked` | Nonempty trimmed `blocked` reason |
| Board / `board` | `review` | Status is `review` |
| Board / `board` | `unassigned` | Empty `assignees` |
| Canvas / `view` | `all` | All entities in the current view and expanded submaps |
| Canvas / `view` | `unverified` | Effective verification is not `passed` |
| Canvas / `view` | `blocked` | Explicitly linked to a blocked task |
| Canvas / `view` | `neighbors` | Target plus one-hop neighbors in either direction |
| Canvas / `view` | `upstream` | Target plus reverse-arrow reachability |
| Canvas / `view` | `downstream` | Target plus forward-arrow reachability |

Board presets intersect member, status, target and search filters. An empty
selection does not imply records were deleted. Clearing filters restores them.

Canvas first selects the view and its explicit expansion paths, then filters it.
Visible containment ancestors needed to frame matching children remain as dashed
context. Responses report match, context, hidden and outside-view match counts;
boundary relations preserve knowledge of edges leaving the selection. Hidden
edges still exist. Reachability uses the stored arrow convention and chosen
relation kinds, never implicit containment edges or inferred runtime causality.
The browser uses all stored relation kinds and pins directional filters to the
node selected when the preset is applied; reapply the preset to change that target.
Graph data includes the same cursor, view, expansions, filter and target.

```sh
node bin/otoha-graph.mjs query /path/system.json --mode board --filter active --assignee alice --limit 20
node bin/otoha-graph.mjs query /path/system.json --mode view --view overview --expanded parser,parser/asr --filter blocked
node bin/otoha-graph.mjs team query --state PRIVATE_DIR --mode view --view overview --filter upstream --target parser --limit 30
```

`--filter` applies only to `board` and `view`; unknown, empty or incompatible
presets fail explicitly. Existing `local`, `reach`, `path` and `cycles` strategies
remain available for different questions. Pin the cursor and preserve filters
when following `page.next`; check `complete` and `page.hasMore`. UI truncation
does not truncate the full task records or detail forms.

Compact Board cards are 94 CSS pixels tall. The density preference is local to
the browser, preserves each lane's visible task anchor and keeps the full draft.
Ordinary accepted updates retain scroll offsets; changed filters start at the
top of their new results. Titles and blocked reasons can be clamped in the card;
their complete text remains in details. No pagination, auto-archiving or status
changes are implied by compact mode.

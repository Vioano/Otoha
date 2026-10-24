# Reader interactions and provenance

Lecture State Supervision's local UI was inspected during the original design comparison.
It is an interaction-design reference, not a dependency distributed by this repository.
Its cursor-anchored wheel zoom, trackpad pan, minimap navigation, detail mode
switch and fullscreen reading informed this design. This is source comparison,
not a fresh acceptance test of its production UI.

Otoha Graph reuses its own bundled Archify camera, node finder, radar and
relationship tools. The new gesture adapter calls that camera; it must not create
a second transform. Ctrl/Cmd-wheel is the Chromium trackpad pinch event path;
WebKit gesture events have a separate adapter. Real hardware smoothness and
WebKit execution need distinct validation from synthetic Chromium input tests.
Zoom is continuous from 100% to 300%; overview bounds prevent panning into empty
space. Two-finger scrolling pans after zooming in. Detail content scrolls normally.

The node body selects without opening the inspector. Its `i` button opens details.
The persistent submap badge shows the child count in both detailed and compact
modes. Single-click toggles a compact, real first-child-level view in place;
neighbouring cards and routes move to make room. Mini nodes retain selection,
info and nested submap controls. Double-click the same badge enters the full
child view; an explicit “进入” button and Shift+Enter provide alternatives.
Enter/Space toggles a focused badge without the 500ms mouse double-click window.
Do not attach full-view entry or inspection to the whole node's double-click.

Back restores camera, selection and expansions. Session storage also restores
these on reload; preferences and drafts retain their existing local storage.
The same inspector DOM can dock or float; its form and draft are never copied.
Drag the floating inspector by its title bar. Canvas focus hides details until
explicitly opened; Escape closes the floating inspector, then exits focus. Text
editing is not intercepted by these shortcuts. Keep native cursor feedback and
let pointerover/out propagate; suppress only the renderer's legacy hover preview.

Preview layout is transient: it never writes expanded geometry to system.json.
Canonical export checks still validate the authored source view, not the inline
preview; inspect the latter separately for node and edge-label overlaps.

Evidence titles open the in-page excerpt. Each card and the excerpt toolbar
reserve an explicit “在 Codex 中打开” action. Enable it only if the preview's
trusted host supplies `openCodexFile({path,line})`; use a same-origin token and
registered evidence ID, never accept arbitrary browser paths or tools. The
current standalone Codex preview has no authorized native-file bridge, so this
action is disabled and “复制路径” is available. Do not invent a codex://file URL,
bypass process authorization, or claim that copying a path opened a native tab.

The four native styles retain their original diagram CSS; the explorer shell
uses the same token block extracted at build time. Frutiger Aero's dark palette,
gloss and surface colors are adapted from Music Agent's
`workbench/static/themes/aero.css`. A light variant is an adaptation, not the
original dark skin. No runtime path to Music Agent is required. The skin is a
viewer layer; do not claim identical Aero styling in every image/vector export
without inspecting that export. The original renderer's canonical geometry and
semantic checks remain the authority for diagram delivery.

Legacy `.archify-design` journal paths, `archify-design` browser draft keys and
`X-Archify-Token` remain protocol compatibility details, not a dependency on the
original installed Archify Skill. New reader preferences use `otoha-graph-reader`.

## Reader chrome and localization

The settings gear opens a nonmodal, theme-aware popover anchored to the header.
Style, light/dark appearance and interface language live together; outside click
and Escape dismiss it. Frequent canvas commands use consistent icons with native
tooltips, accessible names and pressed/expanded states. Language is a reader
preference (`otoha-graph-reader.locale`), separate from `model.meta.locale`.
English is the initial UI language; Chinese remains selectable. Authored node
labels, descriptions, notes, paths and evidence are never translated by the UI
catalog. Renderer chrome reuses the bundled English/Chinese messages.

The canvas fills the available reading area. A single grid extends across its
whole surface and follows the existing camera; the authored SVG viewBox and
canonical exported grid are unchanged. Narrow viewports overlay the inspector
instead of reducing the canvas to a fixed fraction of the screen height.

The explorer mounts each accepted renderer scene in an open Shadow DOM on the
same page. It does not create or navigate iframes. This keeps browser annotation
hit-testing and the pointer in one document while isolating styles, IDs and the
renderer camera. A scoped runtime owns listeners, timers, animation frames and
observers, and explicitly disposes them when switching views. A new scene is
initialized before replacing the last usable scene; initialization errors keep
the previous canvas visible.

When Codex's annotation cursor style is present, Otoha yields node and gesture
input to the host. Browser-host annotations remain separate from Otoha design
requests; verify them in the actual host rather than inferring success from
renderer tests or CSS cursor values.

Focus canvas fills the browser viewport edge to edge, hiding the project header,
view navigation and bottom module strip. Only Exit focus, Details and Settings
float at top right; existing diagram tools remain on the canvas. Presentation
and export move to top left to avoid overlap. Details overlay the map, and exiting
focus restores the previous inspector arrangement without changing the graph,
selection, drafts or camera. Escape closes an open overlay before leaving focus.

## Board alongside Canvas

A centered segmented switch uses an independent equal-track header layout. Its
geometry must not move when canvas-only tools disappear. See [task board](task-board.md)
for card hit regions, theme colors, keyboard status editing, draft recovery and
module navigation. Dragging captures the starting accepted version; refresh may
not silently rebase a drop onto another user's update.

The Board toolbar reserves width for the filter label, clear control, search,
member selector and count. State changes, translations and empty results must
not shift these operation areas; test their rectangles at relevant viewport
widths, not just page overflow. Long card content may be clamped for scanning,
while its full value stays in details. Compact density keeps independent column
scroll anchors and unsaved drafts. [Shared filter projections](filter-projections.md)
define the exact Agent-equivalent selection semantics.

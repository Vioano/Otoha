# Otoha Graph 0.5.0 — verification record

Date: 2026-09-22. Local environment: macOS, Node.js 22.22.3.

## Automated checks

- `npm test`: 91 tests passed, 0 failed.
- `npm run check:validators` and `npm run check:brand-marks`: passed.
- Skill frontmatter validation and `git diff --check`: passed.
- `validate examples/math-modeling.system.json --repo-root . --json`: five
  views passed 9/9 showcase checks each, with no errors or warnings.

Task tests cover independent tasks, many-to-many links, referential integrity,
equivalent Human/Agent selections, pinned pagination, diffs, optimistic version
checks, durable idempotency, scoped delete/restore after restart, intervening
edits, interrupted source mirrors and rejected stale member requests. Signed
member field grants, revocation and forbidden lifecycle operations are exercised
against disposable leader/member installations and local bare Git remotes.

## Independent Agent exercise

An installed AGY CLI worker received the Skill entrypoint, linked documentation,
a disposable copy of the modeling example and natural-language work requests.
It was instructed to use documented interfaces rather than implementation code.
It executed and reread these changes:

1. Create an unlinked task assigned to C.
2. Create a task assigned to B and linked to two existing modules.
3. Remove one association, preserve the other, and mark the task done.
4. Delete the first task.
5. Change the second task's description.
6. Restore only the deleted task, preserving the later description edit.

The coordinator independently compared the resulting fixture: all original
modules, relations, views, evidence and pre-existing tasks were unchanged. The
new tasks and associations matched the requests. The worker correctly declined
to infer module verification from a completed task.

The first report overgeneralized change-request receipts as mandatory for local
module maturity edits. Documentation now explicitly separates that workflow from
evidence-backed local authority edits. A resumed reading exercise corrected that
interpretation and successfully created and reread another independent task
using the newly added complete minimal payload example. This is a bounded
comprehension exercise, not proof that every Agent will always act correctly.

## Findings and corrections

- Canvas's Escape handler consumed the key while its inspector was invisible on
  Board. It now acts only on Canvas; one Escape closes the active board inspector.
- Restarting a local authority without its previously supplied evidence root
  downgraded otherwise unchanged evidence. The authority now persists that root
  in private local context, outside graph publications. Regression checks verify
  both restart continuity and invalidation when source bytes actually change.
- Team CLI parsing omitted board filters. Leader and member queries now accept
  assignee, status and search, verified through real asynchronous CLI calls.
- Team receipts and grants omitted translated task-field labels. The display
  catalog now covers those fields and comment-only receipt summaries.

AGY also reported missing leader CLI discovery, but its reproduction combined a
team authority with the original import path. Team initialization creates a
separate private authoritative model. Correct-path CLI reads and mutations pass;
the preview now rejects mismatched input identity explicitly. The documentation
names the private model and preserves the import/authority distinction.
AGY resumed the same review session, corrected that faulty reproduction, and
independently reran correct-path CLI access, evidence-root restart/drift and
bilingual receipt rendering. Those checks and the 10 task/reader tests passed.

## Actual browser checks

The Codex in-app browser was used with disposable loopback fixtures. Development
checks covered create/save, status selection and dragging, filters, optional
links, locate-in-Canvas, delete/undo, reload persistence, live CLI-to-browser
updates, stale-save rejection and draft retention/reapplication.

At 1389 × 1247 and 600 × 900 CSS viewports, docked/floating inspectors, draft
preservation, bounds and the stationary Canvas/Board switch were checked.
Full-canvas focus was checked at 1600 × 900. Aero dark/light option styles and
English/Chinese labels were checked; authored Chinese text was preserved. Native
select popups were inspected through keyboard/accessibility state, not claimed
as captured in page screenshots. The final Escape regression was reproduced and
retested in the actual browser.

## Limits

No real multi-machine deployment, public GitHub collaboration traffic, load/soak
test, disk hardware failure or independent security audit was performed. Code
publication and CI are distinct from exercising a team's devices and credentials.
AGY's source review or Node simulations do not establish visual acceptance; the
browser checks above are separately recorded. The modeling tasks are examples,
not evidence that anyone completed a real competition task.

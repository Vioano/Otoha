# Otoha Graph 0.5.1 verification

This release folds general reader and authority fixes into the user-level Skill.
The code was compared file by file with the isolated downstream patch manifests
before adaptation. No competition model, request history, keys or private team
state is part of this repository.

## Changes checked

- Board columns scroll independently at 600 tasks. Long titles and blockers stay
  complete in details. Compact cards retain visible task anchors, drafts and the
  browser's density preference. Search, member selection and five filter presets
  change the projection without editing tasks.
- Board and Canvas presets share the Agent query selector. Six Canvas presets
  include explicit containment context and preserve canonical node IDs. A task
  may have zero or several module links; task status never changes module claims.
- The toolbar reserves space for changing labels and conditional controls. The
  same elements were measured in English and Chinese at viewport widths 1440,
  1157, 1024, 760 and 390 CSS pixels. Eight state transitions at each width and
  language produced 80 comparisons, with maximum control displacement 0 px.
- Local member outbox files are read independently, bounded, signature-checked
  and checked against project/epoch/actor/request ID. A bad file remains on disk
  with an `outboxErrors` diagnostic; it does not block valid neighboring requests.
  Partial sync reports `ok: false` and does not imply all requests were accepted.
- A leader with damaged durable history may read its verified prefix, but cannot
  sign or remotely publish that older prefix. Repairing verified history restores
  publication. This differs from an invalid working source with intact history,
  for which the last accepted version remains publishable.
- Atomic writes flush file contents before rename. Parent directory flushing is
  skipped on Windows because Node cannot portably open that directory for fsync;
  all other write failures remain errors. Windows power-loss metadata durability
  is not asserted by this change.

## Evidence and limits

`npm test` passed 100/100 local tests, including six atomic-write cases, a mixed
ten-invalid-file member outbox plus valid request over a real local bare Git
remote and HTTP endpoint, and damaged-leader-history publication recovery.
Validator and brand-mark checks passed; the Skill quick validator passed.
The math-modeling example passed every authored-view diagram check.

An isolated Codex in-app browser used a synthetic 600-task model. The five Board
presets selected 600, 450, 1, 150 and 1 tasks as expected; all six Canvas presets
matched the Agent endpoint's entity IDs. Expanding two nested maps and filtering
blocked nodes showed `asr`, `decode` and `parser`, matching the Agent query's
one match plus two context ancestors. After a source edit, the same browser
accepted 2000 tasks at cursor 2 and found task 1999. An Agent HTTP query for one
member/status returned a bounded 20-record page (4056 bytes) with `hasMore`.
No browser console errors were observed during the targeted search.

An independent Agent started from `SKILL.md` without the implementation history.
In a disposable modeling fixture and local bare Git transport it created a task
with no module link, added and removed one link while preserving the others,
confirmed that `done` did not change module maturity, and exercised member
grants, accepted receipts, forbidden fields, stale versions and idempotent retry.
That review found outdated text about `task.set`, revoking both grant types and
three example captions; the Skill references and example were corrected.

A separate AGY CLI worker using Gemini 3.8 Flash (High) then received a byte-
identical copy of this Skill's entrypoint, Agent/task-board references and query
implementation. Its cold-read handoff forbade opening the source model or
implementation and required `manifest` plus bounded queries. Against a 14-task
synthetic fixture, it identified all nine tasks assigned to A, both explicit
baseline links, the one unassigned task and both tasks without module links.
It kept Board and graph reads at accepted cursor 1 and refused to infer model
verification from a misleading task title or a structural feedback cycle.
Those answers were checked independently against the Agent query output. This
single run passed nine recorded oracle checks, including read-only source
preservation and exact edge directions. Its command list is self-reported by
the headless worker; the return did not include a tool transcript. This tests
one Agent's interpretation of the written contract, not arbitrary Agents or
real-world mathematical conclusions.

These checks ran on macOS with synthetic local state. They do not establish an
actual Windows filesystem result, real multi-machine/GitHub timing, semantic
correctness of a team's evidence, or human acceptance of a competition result.

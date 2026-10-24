# Otoha Graph development and collaboration

- Read `SKILL.md` first. The system-design model is shared by Human and Agent
  interfaces; keep stable IDs, version scope, evidence boundaries and recovery.
- Team protocol: `references/collaboration.md`. The leader private directory is
  outside Git worktrees. Never pull remote graph snapshots, policies or code into
  that authority. Never publish private keys or private state directories.
- As a member, read a bounded accepted graph query, inspect your grants and submit
  `team request` operations with the field versions you read. Do not edit a
  published snapshot, submit a full graph copy or claim another actor's identity.
  Re-read conflicts; retries reuse the same ID only for identical intent.
- Leader changes may alter topology locally after validation. Member requests
  cannot alter topology, permissions, evidence or maturity through generic paths.
  AGENTS instructions complement the enforced protocol; they are not security.
- UI changes retain themes, separate selection/inspection/expansion/navigation,
  and preserve drafts/camera through updates. Verify actual browser interactions.
- State changes must not shift established operation areas. Reserve space for
  conditional controls, counts and changing labels; compare control geometry
  across default, filtered, empty, disabled and longest-label states in both UI
  languages and relevant viewport widths. A static screenshot or no overflow
  does not establish layout stability.
- Run `npm test`; team-only work can first use `npm run test:team`. Tests use
  disposable temporary fixtures and local bare Git remotes, never real user state.
- Runtime tests, browser checks and actual multi-machine/GitHub acceptance are
  different claims. Report exactly what was checked. Do not treat a passing local
  fixture as evidence of production reliability.

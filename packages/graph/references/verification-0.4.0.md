# Otoha Graph 0.4.0 — verification record

Date: 2026-09-21. Local environment: macOS, Node.js 22.22.3.

## Automated checks

- `npm test`: 81 tests passed, 0 failed, including 16 collaboration tests.
- The remote-tampering case was also run separately after strengthening it to
  alter both the graph and member policy; passed.
- `validate examples/service.system.json --repo-root . --json`: all three views
  passed 9/9 showcase checks with no warnings. This is renderer validation, not
  an additional visual review of every example viewport.
- Skill frontmatter validation passed; `git diff --check` passed.

Collaboration tests create isolated leader/member directories and actual local
bare Git repositories. They exercise signatures, actor spoofing, node/field
permissions, initialize-only restrictions, revocation, malformed fields, legacy
HTTP bypasses, CLI forwarding to live preview, atomic batches, field versions,
ABA conflicts, idempotency, two concurrent submitters, leader topology edits,
invalid topology fallback, damaged replica recovery, network failure and a
commit-before-source-mirror crash. A concurrent editor write after durable commit
was fault-injected and retained with a recovery conflict instead of being overwritten.
Tests compare full Human/Agent graph records.

The remote tampering test writes a forged snapshot and authorization entry into
an actual Git branch, simulating the result of a merged unauthorized PR. Members
reject it, the leader preserves local authority, and the next publication repairs
the remote file. A separate test submits forged and unauthorized mailbox requests
through Git and verifies isolation/rejection.

## Browser checks

Two loopback previews, one leader and one member, were operated in the Codex
in-app browser at its 1600 × 900 viewport, using isolated fixture data and the
Frutiger Aero dark appearance:

1. The member sees only the granted `parser` node, input/output fields and comment
   operation; the leader sees its role, member policy and receipts.
2. A member request moves from locally queued to accepted after member upload,
   leader sync and member sync. The accepted version updates without page reload.
3. A member unsaved draft keeps its original field version through a concurrent
   leader change and hot update. Submitting that draft produces a conflict receipt
   visible on both sides, without overwriting the leader field.
4. “Reload accepted content” restores the current leader value and field version.
5. CLI reading of the member's accepted state returns the same accepted receipt.

A startup MutationObserver error was observed in an anonymous minified script
with Electron sandbox frames. It was not attributed to an Otoha source location;
the checks above completed. This record does not claim an error-free host console.

## Limits of these checks

No real multi-device deployment, prolonged outage, remote rate-limit experiment,
load/soak test, hardware disk-failure test or independent security audit was run.
The shared Git remote in the collaboration tests is local, not a live GitHub team
repository. GitHub publication/CI for the code is separate from testing a real
team's credentials, permissions, devices and network.

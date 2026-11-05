---
name: otoha-cli
description: Operate the Otoha native CLI for local team bootstrap, invitation, member enrollment, status checks, inbox and channel bridges. Use when an Agent needs to create or inspect a team runtime through CLI commands.
---

# Otoha CLI

Use the native Go CLI as the orchestration boundary. Do not edit the SQLite
database, team JSON, inbox files, or service HTTP endpoints directly when a CLI
command exists.

## Team lifecycle

For a captain computer, use the local mode first:

```sh
otoha team doctor
otoha team init NAME --mode local
otoha team status NAME
otoha team invite NAME MEMBER --role member --out PRIVATE_INVITE.json
```

The member runs `otoha team join --invite PRIVATE_INVITE.json`. The command
generates the member credential locally and is idempotent for the same invite.
Use `team status` after enrollment and `team doctor` when a process, runtime,
storage, or health check is unclear.

## Security and evidence

- Keep `OTOHA_HOME` outside a Git worktree when possible. Private directories
  are mode 0700 and identity/invite files are mode 0600.
- Invitation files may contain a one-time enrollment code. Deliver them through
  an explicitly chosen private channel and never commit them.
- Never put captain or member private keys, long-lived credentials, or Telegram
  bot tokens in an invitation, message, graph node, or log.
- A successful CLI response proves the command's local result. It does not prove
  that a human read a message, a real Telegram Bot delivered it, or a remote
  host accepted a deployment.

## Scope

This skill owns CLI orchestration and runtime checks. Keep requirement
instances in `otoha-requirement-instance` and human feedback/decisions in
`otoha-human-alignment`; write their accepted objects and relations into the
shared Otoha Graph state. Remote SSH/TLS provisioning is a separate runtime
mode and must not be reported as available until the target host and service
manager are actually verified. In server mode, SSH is only the provisioning
transport and the team endpoint must be HTTPS/TLS; a successful simulated SSH
test is not evidence that a real host, certificate, or remote service manager
is ready.

Read [the CLI specification](../../docs/CLI-SPEC.zh-CN.md) for command phases
and [the CLI README](../../apps/cli/README.md) for current command behavior.

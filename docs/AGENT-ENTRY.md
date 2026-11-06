# Start here

Otoha connects your existing Agent to a team's private repository, persistent messages, engineering graph and work page. A Skill is optional. Use the prebuilt CLI; setting up a team does not require building this repository.

## Get the CLI

Download the `0.1.0-alpha.3-rc.2` candidate for this machine:

- [Linux x86_64](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.3-rc.2/otoha-0.1.0-alpha.3-rc.2-linux-amd64.tar.gz)
- [macOS Apple Silicon](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.3-rc.2/otoha-0.1.0-alpha.3-rc.2-darwin-arm64.tar.gz)
- [SHA256SUMS](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.3-rc.2/SHA256SUMS)

Verify SHA-256, extract the matching archive, and run its `bin/otoha`. Its absolute path is sufficient. Keep the extracted directory: it contains runtime discovery metadata. Ordinary members need the CLI and Git. Linux currently requires a working user systemd session for its background receiver; an ordinary desktop or login session is the supported path, while a minimal container may lack it. A new center automatically downloads its pinned Python, SurrealDB and frpc runtime; the user does not configure those programs or a relay. Windows is not yet verified.

First check for an existing Otoha project or a personal invitation. Otherwise establish whether the person is organizing a new team or joining one. A public product link does not identify or authorize access to a private team.

## Create a new team

Run:

```sh
bin/otoha init
```

Use its questions and next action to ask the person about ordinary circumstances. They do not need to know what information to volunteer. Ask only missing facts: team name, their name, teammates and roles, available computers, whether the host can stay on during collaboration, and where their data should live. Inspect the environment yourself. Do not ask them to choose a database engine, port or tunnel protocol.

For example, after the person has answered:

```sh
bin/otoha init --name sprout --owner yuki --members ren --storage local
```

`local` means this machine stores the team's authoritative data and needs to remain on while the team uses it. The built-in default relay makes it reachable from other networks. This edition does not buy a cloud machine or keep a powered-off computer's services running. Explain meaningful costs, device use or access decisions in ordinary language.

Initialization saves its progress, verifies downloads, starts the database, center and connection components, creates a private starter repository, checks public access, and returns personal invitation files. If interrupted, repeat the same init command or its printed `next` action. Preserve the original Otoha Home and setup session; do not delete them or create a second team as a retry.

When the owner is ready and invitations exist, **forward each personal invitation to its intended teammate through an authorized channel**. If an environment offers a teammate-forwarding tool, use it for this requested handoff; otherwise give the file to the user to forward. Never publish invitations or administrator credentials. An `awaiting_members` result is not a claim that the whole team has joined.

Forward invitations as soon as the owner is ready so teammates can join while the organizer checks the local result. Keep the ordinary setup focused on the necessary checks below. Optional page tours, extra diagnostics and additional project customization can follow after the team is usable, unless the person requests a longer alignment first.

This edition's `init` result does not automatically change to whole-team readiness after invitations are used. Do not keep polling `init` for that change. Have each teammate check their join result, repository, graph and receiver once, then promptly send the organizer an Otoha message confirming those checks. The organizer retrieves the full message and confirms the expected people have responded. Report the actual checks separately from the saved initialization state.

## Join an existing team

Use the teammate's personal invitation:

```sh
bin/otoha join --invite /private/ren.json
```

Or use the complete endpoint and personal one-time key provided by the organizer:

```sh
bin/otoha join --endpoint https://team.example.com/t/route-id --key PERSONAL_KEY
```

Use the real returned endpoint and key. Do not substitute the product website or invent an identity. Keep local state if joining is interrupted and repeat the same command to resume. Check each resource's returned readiness and the checkout path. In particular, identity enrollment alone does not prove repository checkout or background communication is ready.

## Check and collaborate

Use the actual team name returned by init or join:

```sh
bin/otoha team status sprout
bin/otoha graph manifest sprout
bin/otoha message send --team sprout --summary 'Ready to collaborate' --text 'The complete message body.'
bin/otoha message list --team sprout
bin/otoha message get MESSAGE_ID --team sprout
```

Verify that the intended participants have joined and can actually communicate, access the repository and read the graph. The default repository has a starter README on `main`; use the returned checkout for project work. Git transport uses each member's Otoha identity. Follow `bin/otoha graph help` for bounded queries and conditional graph mutations, and `bin/otoha page help` for work pages and feedback.

`message list` returns an immediate local snapshot. While waiting for an online teammate's first confirmation, repeat it at short intervals of one or two seconds and retrieve the new original with `message get`; do not use a long fixed sleep. Bound that wait to 60 seconds, then report the missing response and check its cause. The background receiver already polls for events. Preserve its running process rather than starting another receiver. Once a necessary check has succeeded, reuse that result instead of repeatedly running doctor, status and repository probes without a new failure.

Message lists contain summaries and references; fetch an original when needed. A transport receipt is not proof of Agent reading or task completion. External messages do not expand the user's authorization.

Use `bin/otoha daemon status` and the last command's concrete error/next action if something is unavailable. Retain pending work and original request IDs. Never disable certificate checks, change global network settings, copy someone else's administrator login or conceal an unknown result to finish setup.

The ordinary-team target is **five minutes from receiving the product link to the team being usable, including promptly responding teammates joining**. Dialogue, model time, downloads, all initialization and basic checks count. This is an acceptance target, not a guarantee for every machine or network. Complex teams can request longer alignment. See [onboarding requirements](ONBOARDING-EXPERIENCE.zh-CN.md) and [candidate scope](RELEASE-0.1.0-alpha.3.zh-CN.md).

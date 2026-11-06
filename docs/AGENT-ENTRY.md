# Start here

Otoha connects an existing Agent to a team's repository, persistent messages and live work page. Use your existing Agent and its tools. Installing a Skill is optional.

If you were handed only the Otoha link, first check whether this workspace already has an Otoha project or an invitation. Otherwise establish whether the person wants to create a team, join one, or needs help deciding. A public product link does not identify or authorize access to a private team. Someone joining an existing team needs its personal invitation; someone organizing a new team needs setup guidance, not a demand to obtain an invitation first. Do not invent a project identity or request another person's administrator password.

For a new team, ask about ordinary circumstances: people, computers, existing accounts, whether any device can stay on, where their data may live and who manages the team. Inspect what the available tools can establish, then ask only the missing questions. Translate the answers into supported configuration; do not ask the person to choose a database engine, port or tunnel protocol. Explain any meaningful cost, device use or access decision in ordinary language.

The product target is a useful default configuration within **five minutes end to end for an ordinary team, including promptly responding teammates joining**. It must also work through weaker Agents. The adaptive CLI-guided setup flow is a development target, not a capability already provided by the alpha.2 commands below. Use actual command help and results, report missing capabilities honestly, and retain setup progress. Complex teams can request longer alignment. See [the onboarding requirements](ONBOARDING-EXPERIENCE.zh-CN.md).

## Get the CLI

Use the `0.1.0-alpha.2` pre-release for your platform: [macOS Apple Silicon](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.2/otoha-0.1.0-alpha.2-darwin-arm64.tar.gz) or [Linux x86_64](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.2/otoha-0.1.0-alpha.2-linux-amd64.tar.gz). Verify the archive against [SHA256SUMS](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.2/SHA256SUMS). The archive contains `bin/otoha`, the optional Python center and this guide. [Release scope and evidence](RELEASE-0.1.zh-CN.md).

Extract the archive for your platform and run its `bin/otoha --help`. In the commands below, `otoha` means that executable; using its absolute path is sufficient. Keep the archive directory in place. Team members need the native CLI and Git when a repository is configured. Hosting a center additionally needs Python 3.10 or later. There is no Node.js requirement for a member using the native CLI.

From a source checkout, build with Go:

```sh
go -C apps/cli build -o ../../dist/otoha ./cmd/otoha
./dist/otoha --help
```

## Join and check

```sh
otoha join --invite /private/team.otoha.json
otoha status
otoha team doctor TEAM
```

Use the returned project name, checkout and `origin_url`. Joining should establish one project context. Report each resource's actual readiness. A configured repository or a queued message is not proof that it is usable or received.

If an invitation needs a machine route, use the team's configured gateway or verified HTTPS endpoint. Do not change network settings, disable certificate checks or copy a captain's login. First-time OS permission and project onboarding are separate steps.

## Work without forwarding transcripts

```sh
otoha message send --team TEAM --summary 'Map v3 is ready; inspect the original before changing the entrance.' --text 'Complete source content goes here.'
otoha message list --team TEAM
otoha message get MESSAGE_ID --team TEAM
otoha message status --team TEAM --request-id REQUEST_ID
```

`list` returns short summaries and references. `get` fetches the selected original under current access permissions. Fetching it does not mark it read, adopt a decision or complete a task. `send` returns a durable request ID; `accepted` means the authority accepted the write. Preserve that ID when checking an uncertain result.

Use `otoha page help` to publish a document or a free-form HTML work page and read feedback. A work page is a presentation artifact; its layout is not a required task schema. Preserve the accepted source version when responding to feedback.

Use `otoha telegram help` for the optional Telegram transport. It needs a separately configured test or team Bot. Do not put Bot tokens or private invitations in Git, page content or model-facing notifications. One Bot update queue needs one coordinated consumer.

## When something is missing

Use `otoha team doctor TEAM` and `otoha daemon status`. Keep work and outgoing messages locally when the center is unavailable. Report offline, pending, unsupported and unknown results accurately. Never turn a transport receipt into a claim that another Agent read or adopted a change.

The current mathematical contract is a free Property Graph Core, generic Mutation, on-demand Projection and optional Template. Existing architecture views remain compatibility tools. Graph database migration, complete member message replicas and arbitrary Agent-host integration have separate release gates.

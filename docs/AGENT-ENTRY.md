# Start here

Otoha connects an existing Agent to a team's repository, persistent messages and live work page. Use your existing Agent and its tools. Installing a Skill is optional.

If you were handed only the Otoha link, first check whether this workspace already has an Otoha project or an invitation. A public product link does not identify a private team. If neither exists, ask for the team's short invitation; do not invent a project identity or request an administrator password.

## Get the CLI

The current build is a pre-release candidate. Published release assets will be linked from the repository's Releases page after the release gates pass. A local candidate archive contains `bin/otoha`, the optional Python center and this guide.

Extract the archive for your platform, then use `bin/otoha` below. Keep the archive directory in place. Team members need the native CLI and Git when a repository is configured. Hosting a center additionally needs Python 3.10 or later. There is no Node.js requirement for a member using the native CLI.

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

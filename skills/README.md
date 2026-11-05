# Otoha 配套 Skills

Otoha 当前以可下载的 CLI 为交付核心，配套 Skill 可按需选择下载，使用 CLI 不要求安装 Skill。具体 Skill 分发方式仍待设计。

这些 Skill 是 Agent 的操作模板，不是 Otoha 的状态存储或授权系统。它们生成的需求、实例、反馈和决策要写入共同工程状态，并通过 Otoha Graph 关联。

| Skill | 用途 | 当前样本 |
| --- | --- | --- |
| [otoha-cli](otoha-cli/SKILL.md) | 通过原生 CLI 编排团队初始化、邀请、接入、状态和本机渠道 | `team init/invite/join/status/doctor` |
| [otoha-requirement-instance](otoha-requirement-instance/SKILL.md) | 把抽象需求变成可操作、可评价的实例 | [transport-inbox-instance.json](otoha-requirement-instance/samples/transport-inbox-instance.json) |
| [otoha-human-alignment](otoha-human-alignment/SKILL.md) | 持续记录理解、提问、反馈、决策和影响范围 | [transport-alignment-session.json](otoha-human-alignment/samples/transport-alignment-session.json) |

共同对象样本见 [transport-mvp-objects.json](../docs/intent/transport-mvp-objects.json)。

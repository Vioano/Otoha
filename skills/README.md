# Otoha 配套 Skills

Otoha 当前以可下载的 CLI 为交付核心，配套 Skill 可按需选择下载，使用 CLI 不要求安装 Skill。具体 Skill 分发方式仍待设计。

这些 Skill 是 Agent 的操作与表达方法，不是 Otoha 的状态存储或授权系统。需求实例化与持续对齐生成的需求、实例、反馈和决策按项目约定写入共同工程状态，并通过 Otoha Graph 关联；可视化网站也可独立使用。

| Skill | 用途 | 当前样本 |
| --- | --- | --- |
| [otoha-cli](otoha-cli/SKILL.md) | 通过原生 CLI 编排团队初始化、邀请、接入、状态和本机渠道 | `team init/invite/join/status/doctor` |
| [otoha-visualize](otoha-visualize/SKILL.md) | 调用即把当前指令、需求与真实目标做成团队共享的可视化网站；默认复用中心及后台工作页同步，CLI 更新，风格随内容选择 | [可选网页起点](otoha-visualize/assets/starter.html) |
| [otoha-content-review](otoha-content-review/SKILL.md) | 文稿、PPT、PDF 的整体导演、分层推导、打样与真实成品独立审查；已提供两项有限的可执行检查，整体方法继续试用 | [审查依据样例](otoha-content-review/references/review-basis.md) · [三组可操作正反例](otoha-content-review/assets/review-cases.html) |
| [otoha-requirement-instance](otoha-requirement-instance/SKILL.md) | 把抽象需求变成可操作、可评价的实例 | [transport-inbox-instance.json](otoha-requirement-instance/samples/transport-inbox-instance.json) |
| [otoha-human-alignment](otoha-human-alignment/SKILL.md) | 持续记录理解、提问、反馈、决策和影响范围 | [transport-alignment-session.json](otoha-human-alignment/samples/transport-alignment-session.json) |

共同对象样本见 [transport-mvp-objects.json](../docs/intent/transport-mvp-objects.json)。

内容审查的[可执行检查](otoha-content-review/references/checks.md)包括 PPTX 实际图片比例检查和已提交审查结果／输入版本检查，均使用 Python 3 标准库；当前 28 项回归全部通过。三组虚构实例分别展示裁切与比例、图文边界、并列与选择。检查器不代替实际观看、审美判断或原生实播，通用语义审查引擎仍为计划。

需求解释、demonstration 与开发计划复用 [otoha-visualize 的按需参考](otoha-visualize/references/requirements-plan.md)，不另建一套网站 Skill。[系列开发计划](../docs/SKILLS-DEVELOPMENT.zh-CN.md)区分当前草稿、真实试水、独立复核与可选分发；结构检查通过不代表方法效果已验证。

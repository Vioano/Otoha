# Otoha

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/brand/otoha-mark.svg">
  <img src="assets/brand/otoha-mark-mono.svg" width="96" height="96" alt="Otoha">
</picture>

面向复杂项目的人与多 Agent 协作基础设施。

Otoha 面向跨 Session、跨设备、跨成员和跨 Agent 框架的长期工程协作。人通过具体体验持续形成和修正需求，独立执行的 Agent 通过共同工程状态、通信与协作协议推进工作。当前交付聚焦可下载的 CLI，配套 Skill 可选。新的 `0.1.0-alpha.3-rc.1` 候选加入自动初始化和内置 frp 默认中继，支持 macOS Apple Silicon 与 Linux x86_64；原 `0.1.0-alpha.2` 保留其发行范围。

**把这个链接交给 Agent：** 从[接入与工作入口](docs/AGENT-ENTRY.md)开始。新队长运行 `bin/otoha init`，Agent 根据 CLI 的反馈主动问清情况并继续配置；已有团队使用个人邀请加入。用户无需另配数据库或穿透工具。当前默认由队长电脑或已有机器保存团队数据，协作期间该机器需要在线。

**候选下载：** [macOS Apple Silicon](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.3-rc.1/otoha-0.1.0-alpha.3-rc.1-darwin-arm64.tar.gz) · [Linux x86_64](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.3-rc.1/otoha-0.1.0-alpha.3-rc.1-linux-amd64.tar.gz) · [SHA256SUMS](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.3-rc.1/SHA256SUMS)

校验 SHA-256 后解压，使用 `bin/otoha`。普通成员无需 Go、Node.js 或 Python；当前需要系统 Git，Linux 还需要可用的 user systemd 会话。新中心自动下载固定版本的 Python、SurrealDB 和 frpc，完成网络、通信、仓库与图谱初始化。[候选范围](docs/RELEASE-0.1.0-alpha.3.zh-CN.md)说明依赖、限制及验证边界。

普通团队的目标是从交给 Agent 产品链接到及时响应的队友全员可用，端到端五分钟以内；模型对话、冷下载及实际检查都计入。具体计时与较弱模型盲测要求见[接入体验](docs/ONBOARDING-EXPERIENCE.zh-CN.md)，实现存在不代表该目标已达标。

## 五项核心哲学

1. **分级异步通信**：完整内容持久保存，通知只带极简摘要和引用；按紧急程度与任务边界读取。
2. **全局工程图谱与策略化读取**：显式组织工程对象和关系，通过可复用查询取得所需局部上下文。
3. **持续人机对齐**：专门的对齐 Agent 在整个开发过程中理解、确认和更新人的意图。
4. **需求实例化**：先用低成本、可交互的具体体验帮助人判断，再据此推进实现。
5. **协议驱动的自适应协作与调度**：规划时明确协调策略，复用通用协议，随任务和阶段调整组织方式。

[详细说明](docs/PRINCIPLES.zh-CN.md)保留原讨论中的机制、网站与冲浪游戏例子，以及调度思想的演变。

当前先用[通信需求实例](docs/communication-requirement-instance.html)打磨具体流程：存完整消息、只提示摘要与引用、按权限取原件，再决定采用。页面全部使用虚构数据，不连接真实服务。[需求演化记录](docs/REQUIREMENTS-STATE.zh-CN.md)区分有效需求、被替代方案和未成熟构想，并说明聊天回读缺口；[通信契约](docs/COMMUNICATION-CONTRACT.zh-CN.md)规定目标行为。[两机实验](docs/TWO-HOST-LOOP.zh-CN.md)只报告实际验证范围，不能反过来定义需求。

<details>
<summary>开发者：从源码运行</summary>

需要 Node.js 18+、Python 3.10+；构建原生客户端需要 Go 1.26+。

```sh
git clone https://github.com/Vioano/Otoha.git
cd Otoha
npm run setup
node bin/otoha.mjs help
```

图谱与看板示例：

```sh
node bin/otoha.mjs graph preview packages/graph/examples/service.system.json --repo-root packages/graph
```

打开命令打印的本地地址。另一个终端可以查询同一接受状态：

```sh
node bin/otoha.mjs graph manifest packages/graph/examples/service.system.json
node bin/otoha.mjs graph query packages/graph/examples/service.system.json --mode overview
```

示例是虚构设计数据。预览可用 Ctrl-C 停止，不自动启动 Agent。

协作服务与原生 CLI：

```sh
node bin/otoha.mjs server --help
node bin/otoha.mjs message --help
npm run build:cli
node bin/otoha.mjs client version
```

黑客松路演入口：

```sh
npm run build:cli
./dist/otoha                 # 终端协作控制台与 ASCII 标志
./dist/otoha demo --slow    # 可录屏的多 Agent 事件流
./scripts/roadshow-demo.sh  # 临时目录启动本机服务并跑完整 CLI 片段
```

浏览器版动态演示在 [showcase](showcase/index.html)，六分钟节奏和边界说明见 [路演脚本](docs/ROADSHOW.zh-CN.md)。

通信闭环的本机收发：

```sh
node bin/otoha.mjs inbox check --channel PROJECT-general --session SESSION
node bin/otoha.mjs inbox list --channel PROJECT-general
node bin/otoha.mjs inbox read MESSAGE_UID --channel PROJECT-general --session SESSION
```

服务初始化、私有身份与 MCP 接入见 [Otoha Collaboration](services/collaboration/README.md)。原生下载版的当前用法以[Agent 接入入口](docs/AGENT-ENTRY.md)为准。

</details>

## 代码与文档

| 组件 | 入口 |
| --- | --- |
| 图谱、查询、看板与交互视图 | [Otoha Graph](packages/graph/README.md) |
| 协作服务、身份、查询、回执与 MCP | [Otoha Collaboration](services/collaboration/README.md) |
| 原生客户端、项目接入与后台接收 | [Otoha CLI](apps/cli/README.md) |
| GitHub 收信与操作指引 | [Otoha Mailbox](integrations/mailbox/README.md) |
| 图标方向打样，尚未定稿 | [图标探索](assets/brand/README.md) |

设计依据见[五项核心哲学](docs/PRINCIPLES.zh-CN.md)、[已定方向与边界](docs/DECISIONS.zh-CN.md)、[CLI 模块规格](docs/CLI-SPEC.zh-CN.md)、[架构起点](docs/ARCHITECTURE.zh-CN.md)与[开发计划](docs/DEVELOPMENT.zh-CN.md)。迁入代码、来源和检查结果见[组件说明](docs/FOUNDATIONS.zh-CN.md)、[验证记录](docs/MIGRATION.zh-CN.md)与[版权来源](NOTICE.md)。

系统图谱的当前设计见[数学模型与职责边界](docs/SYSTEM-GRAPH-MATH.zh-CN.md)：自由 Property Graph Core、通用 Mutation、按需 Projection 与可选 Template。原生协作服务已有基于 SurrealDB 的基础自由图接口、事务与回执；旧图谱预览仍使用原有架构模型，通用画布、完整 Projection 与 Template 继续开发。[旧可视讲解器](docs/system-graph-explainer.html)保留早期 L0–L6 推导，仅供历史参考。

[图数据库候选调研与最小试验](docs/GRAPH-DATABASE-EVALUATION.zh-CN.md)记录早期五个候选比较，以及 SurrealDB 3.3.5 在 Mac 与 Linux 的同一份虚构样本。新候选在此基础上接入原生 CLI 的基础图接口；旧用户图谱没有自动迁移。

[CLI、常驻服务与跨项目图谱](docs/CLI-DAEMON-AND-GRAPH-SCOPE.zh-CN.md)明确一键接入、断线续接、外部 Agent 操作本地网页及私有 Gitea 的边界，比较四种图组合方式。独立图加引用是当前实施建议，三库最小实验不等于完整功能已经实现。

## 验证与当前边界

```sh
npm test
npm run check:names
```

通用实现已经迁入并统一命名，组件组合不等于五项哲学已经完整落地。已有中心共享频道、摘要/原件分离、指定 Codex 宿主的 queue/steer、工作页反馈与离线续投的限定验证。新候选的自助初始化、默认中继与基础图接口分别做功能和端到端验收。完整成员副本、逐消息私发权限、通用图谱画布、Template 和自适应调度仍待开发；Windows 实机及其他宿主另行验证。

凭据、邀请、个人配置和真实运行数据保存在仓库外。构建与测试不迁移旧部署。

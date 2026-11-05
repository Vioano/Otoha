# Otoha

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/brand/otoha-mark.svg">
  <img src="assets/brand/otoha-mark-mono.svg" width="96" height="96" alt="Otoha">
</picture>

面向复杂项目的人与多 Agent 协作基础设施。

Otoha 面向跨 Session、跨设备、跨成员和跨 Agent 框架的长期工程协作。人通过具体体验持续形成和修正需求，独立执行的 Agent 通过共同工程状态、通信与协作协议推进工作。当前交付聚焦可下载的 CLI，配套 Skill 由使用者按需选择下载；使用 CLI 不要求安装 Skill。正式 CLI 下载发行尚待完成，当前通过源码构建使用。

**把这个链接交给 Agent：** 从[接入与工作入口](docs/AGENT-ENTRY.md)开始。公共产品链接帮助 Agent 发现用法，已有仓库上下文或队长的精简邀请确定具体项目。当前先在私人 Gitea 验证候选版本，通过[发布门槛](docs/RELEASE-GATES.zh-CN.md)后更新 GitHub。

## 五项核心哲学

1. **分级异步通信**：完整内容持久保存，通知只带极简摘要和引用；按紧急程度与任务边界读取。
2. **全局工程图谱与策略化读取**：显式组织工程对象和关系，通过可复用查询取得所需局部上下文。
3. **持续人机对齐**：专门的对齐 Agent 在整个开发过程中理解、确认和更新人的意图。
4. **需求实例化**：先用低成本、可交互的具体体验帮助人判断，再据此推进实现。
5. **协议驱动的自适应协作与调度**：规划时明确协调策略，复用通用协议，随任务和阶段调整组织方式。

[详细说明](docs/PRINCIPLES.zh-CN.md)保留原讨论中的机制、网站与冲浪游戏例子，以及调度思想的演变。

当前先用[通信需求实例](docs/communication-requirement-instance.html)打磨具体流程：存完整消息、只提示摘要与引用、按权限取原件，再决定采用。页面全部使用虚构数据，不连接真实服务。[需求演化记录](docs/REQUIREMENTS-STATE.zh-CN.md)区分有效需求、被替代方案和未成熟构想，并说明聊天回读缺口；[通信契约](docs/COMMUNICATION-CONTRACT.zh-CN.md)规定目标行为。[两机实验](docs/TWO-HOST-LOOP.zh-CN.md)只报告实际验证范围，不能反过来定义需求。

## 从源码运行

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

服务初始化、私有身份与 MCP 接入见 [Otoha Collaboration](services/collaboration/README.md)。当前没有正式二进制发行版；使用本仓库源码构建。

## 代码与文档

| 组件 | 入口 |
| --- | --- |
| 图谱、查询、看板与交互视图 | [Otoha Graph](packages/graph/README.md) |
| 协作服务、身份、查询、回执与 MCP | [Otoha Collaboration](services/collaboration/README.md) |
| 原生客户端、项目接入与后台接收 | [Otoha CLI](apps/cli/README.md) |
| GitHub 收信与操作指引 | [Otoha Mailbox](integrations/mailbox/README.md) |
| 图标方向打样，尚未定稿 | [图标探索](assets/brand/README.md) |

设计依据见[五项核心哲学](docs/PRINCIPLES.zh-CN.md)、[已定方向与边界](docs/DECISIONS.zh-CN.md)、[CLI 模块规格](docs/CLI-SPEC.zh-CN.md)、[架构起点](docs/ARCHITECTURE.zh-CN.md)与[开发计划](docs/DEVELOPMENT.zh-CN.md)。迁入代码、来源和检查结果见[组件说明](docs/FOUNDATIONS.zh-CN.md)、[验证记录](docs/MIGRATION.zh-CN.md)与[版权来源](NOTICE.md)。

系统图谱的当前设计见[数学模型与职责边界](docs/SYSTEM-GRAPH-MATH.zh-CN.md)：自由 Property Graph Core、通用 Mutation、按需 Projection 与可选 Template。后续优先复用图数据库，阶段与验收见[开发计划](docs/DEVELOPMENT.zh-CN.md)。这是新的设计基线，现有实现仍使用原有架构模型。[旧可视讲解器](docs/system-graph-explainer.html)保留早期 L0–L6 推导，仅供历史参考。

[图数据库候选调研与最小试验](docs/GRAPH-DATABASE-EVALUATION.zh-CN.md)比较五个候选，并用 SurrealDB 3.3.5 在 Mac 与 Linux 实机验证同一份虚构样本。它是独立后端实验，尚未接入正式 CLI 或迁移用户数据。

[CLI、常驻服务与跨项目图谱](docs/CLI-DAEMON-AND-GRAPH-SCOPE.zh-CN.md)明确一键接入、断线续接、外部 Agent 操作本地网页及私有 Gitea 的边界，比较四种图组合方式。独立图加引用是当前实施建议，三库最小实验不等于完整功能已经实现。

## 验证与当前边界

```sh
npm test
npm run check:names
```

通用实现已经迁入并统一命名，根入口可以启动组件；组件组合不等于五项哲学已经完整落地。分级通知、持续对齐、需求实例关联与自适应调度还需要继续开发。真实跨机器、Windows 宿主和空闲 Agent 唤醒有各自验收范围。

凭据、邀请、个人配置和真实运行数据保存在仓库外。构建与测试不迁移旧部署。

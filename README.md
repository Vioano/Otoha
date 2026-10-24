# Otoha

面向复杂项目的人与多 Agent 协作基础设施。

Otoha 面向跨 Session、跨设备、跨成员和跨 Agent 框架的长期工程协作。人通过具体体验持续形成和修正需求，独立执行的 Agent 通过共同工程状态、通信与协作协议推进工作。长期方向包含原生 Agent，同时兼容现有 Agent；插件、MCP 和 Skill 提供接入与操作指引。

## 五项核心哲学

1. **分级异步通信**：完整内容持久保存，通知只带极简摘要和引用；按紧急程度与任务边界读取。
2. **全局工程图谱与策略化读取**：显式组织工程对象和关系，通过可复用查询取得所需局部上下文。
3. **持续人机对齐**：专门的对齐 Agent 在整个开发过程中理解、确认和更新人的意图。
4. **需求实例化**：先用低成本、可交互的具体体验帮助人判断，再据此推进实现。
5. **协议驱动的自适应协作与调度**：规划时明确协调策略，复用通用协议，随任务和阶段调整组织方式。

[详细说明](docs/PRINCIPLES.zh-CN.md)保留原讨论中的机制、网站与冲浪游戏例子，以及调度思想的演变。

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

服务初始化、私有身份与 MCP 接入见 [Otoha Collaboration](services/collaboration/README.md)。当前没有正式二进制发行版；使用本仓库源码构建。

## 代码与文档

| 组件 | 入口 |
| --- | --- |
| 图谱、查询、看板与交互视图 | [Otoha Graph](packages/graph/README.md) |
| 协作服务、身份、查询、回执与 MCP | [Otoha Collaboration](services/collaboration/README.md) |
| 原生客户端、项目接入与后台接收 | [Otoha CLI](apps/cli/README.md) |
| GitHub 收信与操作指引 | [Otoha Mailbox](integrations/mailbox/README.md) |
| 图标方向打样，尚未定稿 | [图标探索](assets/brand/README.md) |

设计依据见[五项核心哲学](docs/PRINCIPLES.zh-CN.md)、[架构起点](docs/ARCHITECTURE.zh-CN.md)与[开发起点](docs/DEVELOPMENT.zh-CN.md)。迁入代码、来源和检查结果见[组件说明](docs/FOUNDATIONS.zh-CN.md)、[验证记录](docs/MIGRATION.zh-CN.md)与[版权来源](NOTICE.md)。

## 验证与当前边界

```sh
npm test
npm run check:names
```

通用实现已经迁入并统一命名，根入口可以启动组件；组件组合不等于五项哲学已经完整落地。分级通知、持续对齐、需求实例关联与自适应调度还需要继续开发。真实跨机器、Windows 宿主和空闲 Agent 唤醒有各自验收范围。

凭据、邀请、个人配置和真实运行数据保存在仓库外。构建与测试不迁移旧部署。

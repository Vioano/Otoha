# Otoha 图数据库候选与最小试验

> 调研与试验：2026-11-03 UTC。结论是继续试用 SurrealDB，不是已经把 Otoha 的权威存储迁移到它。现有 CLI、receiver、协作库与架构 IR 未改动。

**首个候选选 SurrealDB。** 决定这次选择的是：开放属性可以直接保存，缺失值与显式 `null` 可区分，边有可引用的记录身份，事务和图查询可复用，原生可执行文件能在 Mac 与 Linux 上作为受管子进程运行。相比为一个强类型后端重新编码自由属性，这条适配路径更直接。它仍有明确成本：二进制并不小，边重连和删除需要 Otoha 约束，BSL 许可也不同于 Otoha 自身的 MIT 许可。

## 1. 比较的依据

以[当前数学模型](SYSTEM-GRAPH-MATH.zh-CN.md)和 [CLI 规格](CLI-SPEC.zh-CN.md)为准：

| 边界 | 这次要确认的能力 | 仍由 Otoha 负责 |
| --- | --- | --- |
| Core | 空属性、多重边、自环、环、稳定身份、自由键和值、缺失与 `null` 的区别 | 对外值域和身份契约；不能让存储的保留字段占用业务属性名 |
| Mutation | 原子批次、失败回滚、条件检查、删除和重连的实际行为 | 权限、接受顺序、请求去重、回执与完整历史；业务操作的含义 |
| Projection | 属性筛选、分组、聚合、成员与原始边查询 | 投影规则、源版本、边界与完整性；商图不能替代原图路径证明 |
| Template | 不安装业务 Schema 也能创建和查询通用图 | 可选词汇、局部约束、预设及其启用范围 |
| CLI 部署 | 本机可启动、落盘和重启；Linux 实机可运行；运行时与分发成本明确 | 进程管理、凭据与目录管理、平台打包、认证后的远程访问 |

数据库中固定的 `node`、`edge` 容器是结构编码，不是固定业务类型。不能为了使用后端，重新要求每一种 `type` 或任务状态先建表。

## 2. 候选比较

下表除 SurrealDB 外均为文档与仓库核对，未在本轮安装或运行。各产品自己的性能宣传不作为排名依据。

| 候选 | Core 与 Mutation 的贴合程度 | Projection | CLI 部署与许可 | 本轮判断 |
| --- | --- | --- | --- | --- |
| **SurrealDB** | SCHEMALESS 对象属性；`NONE` 与 `NULL` 分开；边记录有身份；有事务。须显式启用端点存在检查；直接修改边端点和节点删除存在语义差异 | 有原生关系遍历、分组与聚合，能取回成员和边 ID；投影版本与历史不是自动完成的 | 原生进程，可本机落盘；本轮跑通 Mac ARM64、Linux x86_64。Core 为 BSL 1.1 | **首个实验候选**；适配缺口已有小样本验证 |
| **LadybugDB** | 嵌入式、MIT、Serializable ACID 有吸引力；文档要求预定义表与强类型属性列，开放键需要另选容器或编码并验证 | Cypher、图算法适合分析；动态 Properties 的查询映射还需试验 | 可嵌入；Go/C/C++ 等接口涉及平台原生库的分发 | 保留备选；若优先嵌入或 MIT 许可，可做第二个样本，不把业务 Schema 强加给 Core |
| **Neo4j Community** | 属性图与事务接口完备；原生 Properties 不能直接保存 `null`、嵌套对象或一般混合列表，需额外编码以满足本 Core | Cypher 遍历、分组和聚合可复用 | 服务端依赖 JVM；Go 客户端不消除服务器运行时。Community 为 GPLv3 | 团队已有 Neo4j 服务时值得考虑；本轮本机 CLI 成本较高 |
| **FalkorDB / Lite** | 自由属性图、Cypher；`NULL` 表示缺失/未定义。多个命令的失败全回滚必须另验，不能用 Redis `MULTI/EXEC` 直接代替本 Mutation 契约 | 能做图查询；官方列出多重边计数等具体语义差异 | Lite 实际管理 Redis/FalkorDB 子进程和 Unix socket，不是没有数据库进程；内核为 SSPLv1，包装层许可需分开看 | 保留备选；这次不先承担值编码、批次和两层部署的适配 |
| **CozoDB** | 嵌入式关系图；支持 Json、Null、事务与 `ensure` 条件；需要将节点和边映射为通用关系 | Datalog 适合递归读取与聚合 | MPL-2.0；本轮 API 核对最新 release 仍是 2023-12-11 的 `0.7.6`，仓库 `pushed_at` 为 2024-12-04，未标 archived | 表达与部署值得研究；发布记录使它暂不作为首个长期依赖。这不证明项目已停止维护 |

对应的一手依据：SurrealDB 的 [None/Null](https://surrealdb.com/docs/reference/query-language/language-primitives/data-types/none-and-null)、[关系](https://surrealdb.com/docs/reference/query-language/statements/relate)、[事务](https://surrealdb.com/docs/learn/querying/concepts-and-guides/transactions)、[许可](https://surrealdb.com/license)；Ladybug 的[建模要求](https://docs.ladybugdb.com/get-started/)、[事务](https://docs.ladybugdb.com/cypher/transaction/)及[MIT 说明](https://docs.ladybugdb.com/)；Neo4j 的[属性值限制](https://neo4j.com/docs/cypher-manual/current/values-and-types/property-structural-constructed/)、[NULL 限制](https://neo4j.com/docs/java-reference/current/java-embedded/property-values/)、[运行要求](https://neo4j.com/docs/operations-manual/current/installation/requirements/)及[Community 源码许可](https://github.com/neo4j/neo4j/blob/2026.10/LICENSE.txt)。

FalkorDB 的 [Cypher 支持范围](https://docs.falkordb.com/cypher/cypher-support)、[已知查询差异](https://docs.falkordb.com/cypher/known-limitations)、[Lite 部署](https://github.com/FalkorDB/falkordblite)、[内核许可](https://github.com/FalkorDB/FalkorDB/blob/main/LICENSE)与 Redis 的[运行时错误不回滚说明](https://redis.io/docs/latest/develop/using-commands/transactions/)支持表中的待验边界；这里没有据此断言所有 FalkorDB 单条查询都缺少原子性。Cozo 的[类型](https://docs.cozodb.org/en/latest/datatypes.html)、[事务与条件](https://docs.cozodb.org/en/latest/stored.html)、[MPL 说明](https://www.cozodb.org/)及[最新 release](https://github.com/cozodb/cozo/releases/tag/v0.7.6)支持其能力与版本记录。

## 3. 验证版本与部署

试验固定为 **SurrealDB 3.3.5 + SurrealKV**。开始调研时官方安装文档仍标 `3.3.5` 为稳定版本；GitHub release API 已在 2026-11-03 15:22:58 UTC 发布 `4.0.0`。本轮使用已取得的 3.3.5 构建完成同一份样本，不混用 4.0 的接口或承诺。3.3.5 是验证版本，不能再称为最新发布版本。[3.3.5 发布](https://github.com/surrealdb/surrealdb/releases/tag/v3.3.5)、[4.0.0 发布](https://github.com/surrealdb/surrealdb/releases/tag/v4.0.0)、[安装文档](https://surrealdb.com/docs/running/installation)。

两份原生发行包均按 GitHub release API 的 `digest` 校验后解压，未全局安装数据库、未注册常驻服务。探针只用 Python 标准库；它启动一个认证后的回环地址数据库进程，用独立临时目录保存虚构数据，结束后停止进程并删除该目录。Linux 通过已有 SSH 管理路径执行；这验证了 Linux 实机部署与执行，没有开放数据库 LAN 端口，也没有验证多个真实 Agent 或成员接入。

| 构建 | 发行压缩包 SHA-256 | 解压后数据库可执行文件 |
| --- | --- | --- |
| macOS ARM64 | `534140236e7246d86969015ba9ae8dd2f7f8f277ed1412cb17f3d397f95a1090` | 282.32 MiB |
| Linux x86_64 | `8f3bc163405085343fc6db9746a8e0cd55503c939802ad2b35a466675a9c09cf` | 336.04 MiB |

因此“一个原生可执行文件”并不等于小体积 CLI。可考虑由现有 Otoha 守护进程按需下载、启动它；这只是下一步打包建议。Go HTTP 客户端、Otoha 命令集、Windows 和发行安装流程尚未接通或验证。新增官方 `surreal` launcher 及组件自动下载流程也未在本轮验证。

## 4. 五个节点、五条边

完整输入见[fixture.json](../experiments/graph-db-surreal/fixture.json)。没有启用 Template；`group`、`status` 和 `type` 都只是任意业务属性。

```text
组 A          组 B                    组 C
a ──e1─────→ b1                 b2 ──e3──→ c
  ──e2─────→  │                              ↺ loop
  ←──back─────┘

loose：独立节点，Properties = {}
```

`b1` 和 `b2` 同属 B，但两者之间没有路径。节点还包含显式 `null`、缺失键、嵌套对象、混合数组、中文，以及名为 `id`、`in` 的普通 Properties。实际存储映射为：

```text
V        → node:<logical-id>  {p: <Properties>}
E,s,t,P  → edge:<logical-id>  {in: node:…, out: node:…, p: <Properties>}
```

`p` 将业务属性和数据库的 `id/in/out` 分开。`node:e1` 与 `edge:e1` 可以同时存在；不靠标签文字识别身份。数据库中的两张表只规定节点与边，不规定任务、模块、Agent 或成熟度。

投影直接用数据库查询形成 `A→B`、`B→A`、`B→C`、`C→C`，聚合边带原始边 ID，展开 B 带 `b1/b2` 与四条边界边。数据库两跳查询和独立的原图可达性校验共同确认：商图有 `A→B→C`，原图从 a 只能到 a、b1，不能到 c。

## 5. 结果与适配缺口

同一个最终探针在两个平台上均通过 **28 项预期检查**。这些检查包括确认后端限制的反例，不代表 28 项能力都能直接用原生命令实现。完整 SQL、响应、探针/输入/二进制哈希和最终状态在 [macOS 记录](evidence/graph-db-surreal-20261103/macos-arm64.json)与 [Linux 记录](evidence/graph-db-surreal-20261103/linux-x86_64.json)。两端最终图的规范化 SHA-256 相同：`1073dd0a19da614060c6c807453d354d0fe04542022bcccbe85985c8de334bd2`。

| 内容 | 实际观察 | 结论 |
| --- | --- | --- |
| Core | 空图、空 Properties、多重边、自环、环、身份空间、自由键、嵌套值与往返导出一致 | 此样本不需要业务类型表或必填业务字段 |
| 属性修改 | 动态加键无需 DDL；`SET … = NULL` 保留键，`UNSET` 删除键 | 可以直接表达这部分 `PROP_SET/PROP_UNSET` |
| 结构前提 | 重复节点被拒绝；`ENFORCED` 关系拒绝不存在的端点 | 端点存在检查需要配置；不应照搬默认的 `RELATE` 行为 |
| 原子批次 | 两次写入之后 `THROW`，回读没有任何部分结果 | 覆盖了样本中的失败回滚 |
| 条件修改 | 事务内检查状态后提交；旧条件再次提交被拒绝，状态及演示版本未推进 | 能承载条件检查；完整 Otoha 接受协议仍未实现 |
| 投影 | 属性分组、边数量/成员、展开与边界可回查；查询前后 Core 相同 | 复用数据库查询，不存第二份权威分组图 |
| 投影版本 | 演示版本变更后，带旧版本的展开被拒绝 | 这里只验证拒绝过期读取；没有实现按任意历史版本继续展开 |
| **原生边端点更新** | `UPDATE edge:e2 SET out = node:c` 返回 OK，读回仍是原端点 | **不能将它作为 `EDGE_REWIRE`** |
| **复合重连** | 同一事务中删除旧记录，再以相同逻辑 ID 和原 Properties 建立新边；新旧遍历结果随之改变 | 这一组合可以作为重连候选；不承诺保留数据库内部物理身份 |
| **节点删除** | 原生删除节点会级联移除相关边；带关联检查的事务则拒绝删除 | 若 Otoha 采用禁止隐式级联，必须在自己的 Mutation 入口先检查 |
| 持久化与交换 | 正常停止后重启，图完全一致；导出再导入另一数据库，逻辑 ID、端点和 Properties 一致 | 覆盖正常重启与交换；没有模拟断电或强制崩溃 |

最初三份失败记录保留在 [attempts](evidence/graph-db-surreal-20261103/attempts/README.md)：一份是探针对 `BEGIN/COMMIT` 响应索引的错误，两份是 Mac/Linux 对原生重连的失败断言。修正后的探针明确记录原生限制，再单独验证复合操作，没有把失败改写成原生命令可用。

重复创建已有边 ID 的行为尚未在本轮测试；3.3 系列 `RELATE` 对已有 ID 的行为也不能直接当作 Otoha 的 `EDGE_ADD` 契约。正式入口须明确区分新建、重连和同请求重试，校验身份是否存在，并由回执识别重试。[RELATE 的已有记录说明](https://surrealdb.com/docs/reference/query-language/statements/relate)。

## 6. 接着做什么

继续以 SurrealDB 为候选，先把已验证的映射接到一条最薄的 Otoha Go CLI → 受管后端链路。数据库原始写入口留在权威端内部，Otoha 的通用 Mutation 入口负责身份、权限、操作前提、删除策略与复合重连。正式选择 3.3 或 4.0 之前，要用相同契约重新验证所选构建；不根据当前文档中的 4.0 特性推定 3.3.5 已支持。

随后按[开发计划](DEVELOPMENT.zh-CN.md)验证应用层请求去重、接受历史、真实并发、千级有界查询与分页、历史投影展开、一个局部 Template，以及旧 IR 的兼容导入。当前两端的 `meta:head` 只是演示用版本记录，不能视为这些状态管理能力已经完成。

SurrealDB 官方许可说明其 Core 采用 BSL 1.1，允许自托管、嵌入等使用，并对面向第三方的数据库服务保留商业授权要求。实验不向 Otoha 仓库复制或发布数据库二进制；正式下载分发与未来托管形态应按所选发行版的实际 LICENSE 定稿，不能仅因为 Otoha 是 MIT 就把数据库标成 MIT。[官方许可说明](https://surrealdb.com/license)。

本轮完成的是候选比较和最小样本初验。Template 引擎、完整调度协议、生产迁移与正式数据库选型均未因它完成。

# 项目图谱 API

本文规定首个项目图谱接口的最小范围：成员使用已加入 Otoha 项目的身份读取和修改同一个后台图谱。仓库、消息、工作页和图谱共用项目身份；不要求成员另外安装 Skill、注册数据库账号或配置图谱服务器。本文是本轮接口合同，不代表真实团队部署或用户验收已经完成。

数学语义沿用 [SYSTEM-GRAPH-MATH.zh-CN.md](SYSTEM-GRAPH-MATH.zh-CN.md)：自由 Properties 的节点与边、通用 Mutation、按需 Projection。固定架构类型、任务状态和成熟度不是 Core 必填字段。本轮不提供 Template 引擎、跨图事务或旧架构模型的自动迁移。

## 身份、项目与后台绑定

所有 `/v1/graph/*` 请求都使用现有 `Authorization: Bearer …`。服务端先确认凭据有效、actor 未禁用、项目存在，再检查当前项目 grant。请求体中的 `actor`、Properties 的 `owner` 或模板均不授予权限。

| 调用者 | Manifest、Query、本人请求回执 | Mutation |
| --- | --- | --- |
| 无有效凭据 | `401` | `401` |
| 已认证但无项目权限 | `403` | `403` |
| 项目 `reader` | 允许 | `403` |
| 项目 `member`、`admin` | 允许 | 允许 |
| 全局 owner | 保留既有管理访问 | 允许 |

已认证请求指定不存在的项目返回 `404`。授权在每次请求时重新检查；旧的成功回执不保留已撤销的访问权。回执按认证 actor 与请求 ID 定位，不可用其他人的请求 ID 查询其操作结果。

管理员在 Collaboration 的私有 state 目录放置 `graph.json`：

```json
{
  "url": "http://127.0.0.1:8000",
  "username": "example-backend-user",
  "password": "EXAMPLE_ONLY_NOT_A_REAL_PASSWORD",
  "namespace": "otoha",
  "projects": {
    "sample-project": {
      "database": "sample_project",
      "graph_id": "sample-project-graph"
    }
  }
}
```

以上全部为虚构值。`graph.json` 在 Unix 上须仅其所有者可访问（如 `0600`）。后台 URL 限于本机 loopback；数据库凭据只供服务使用，不返回给 CLI 或浏览器，也不写入公开仓库。服务端根据已授权的项目读取绑定，不接受调用者指定数据库、命名空间、文件路径或后端 URL。本轮每个项目使用不同的 database；项目没有图谱绑定时明确报错，不自动指向其他项目的数据库。

认证、项目存在性和绑定读取在短锁中完成；后台请求在该锁之外执行，避免图查询等待占用消息服务的全局锁。旧 `packages/graph/design/server.mjs` 是本机预览入口，不能作为团队 API 整站公开；其本机 session token 不能代替项目认证。

## 共同格式与边界

成功响应使用 `schema: "otoha-graph/1"`，并带有：

| 字段 | 含义 |
| --- | --- |
| `graph_id` | 此项目绑定的稳定逻辑图身份 |
| `source_version` | 返回状态的整数接受版本 |
| `scope` | 本次项目、操作和读取范围 |
| `complete` | 本次声明范围是否完整返回 |
| `truncated` | 是否因结果上限省略后续结果 |

`complete` 不表示已经读取整张图。邻域查询只对其声明的一阶邻域负责；分页结果保留原始身份和端点，不能把部分边集误报为完整关系。返回的节点和边都是原图中的完整对象，省略的对象通过 `truncated` 与续读位置表达。

节点格式为 `{"id":"n1","properties":{}}`；边格式为 `{"id":"e1","source":"n1","target":"n2","properties":{}}`。`properties` 可为空，不预设业务字段。节点和边的身份空间分别区分；相同端点之间可有多条独立边，也可有自环。

对象 ID、项目 ID 和请求 ID 使用现有 `NAME` 规则：1–80 个 ASCII 字符，第一个字符为字母或数字，其余可含字母、数字、`_`、`.`、`-`。Property 键为非空 UTF-8 字符串，最多 256 bytes。值限于有限 JSON：整数绝对值不超过 `2^53-1`，不接受 NaN 或 Infinity。每个 Property 值自身的嵌套深度最多 32，从该值的根部计数；HTTP 请求、后台存储和响应信封的层数不计入这个上限。Property 键和值属于数据，不因其名称获得执行或授权能力。

JSON 请求体最多 65,536 bytes。查询结果最多 512 KiB；裁剪以完整对象为单位，不截断某个对象的 Properties。结果截断时返回 `next_after`，用于继续读取。

错误使用 HTTP 状态与 JSON `error` 代码。返回 `409` 时必须重读相关状态并判断后续修改；不能只替换版本号覆盖重试。网络中断或响应丢失属于结果未知，不能推断请求没有接受。

## Manifest

```http
GET /v1/graph/manifest?project=sample-project
Authorization: Bearer …
```

返回项目图身份、当前接受版本、范围和后端已提供的能力。响应不包含数据库登录信息。Manifest 是读取操作，`reader` 可以调用。

`counts` 给出当前版本的节点数和边数。当前 `capabilities.queries` 为 `inspect`、`filter`、`neighborhood`，`capabilities.mutations` 为下文六个基本操作；`templates` 与 `history_query` 均为 `false`。`limits` 返回 `query_limit: 500`、`operations: 100`、`request_bytes: 65536`、`result_bytes: 524288`。`ready: true` 表示这次请求已经取得后台图状态，不表示整套产品验收完成。

## Query

```http
POST /v1/graph/query
Authorization: Bearer …
Content-Type: application/json
```

查询体包含 `project`、`mode` 和该模式的字段。可选非负整数 `source_version` 要求当前接受版本恰好等于该值；传入较旧或尚未存在的版本都返回 `409 graph_revision_conflict`，不静默把分页切到新版本。本轮不提供历史版本读取或任意后台 SQL。首次读取省略版本时取得该次事务看到的版本；后续分页携带它，图变化后重新开始读取或重新判断范围。

| `mode` | 参数 | 范围 |
| --- | --- | --- |
| `inspect` | `kind: "node" \| "edge"`、`id` | 一个原始对象及完整 Properties |
| `filter` | `kind`、可选 `where`、`limit`、`after` | 按 Properties 条件与 ID 顺序取得一页对象 |
| `neighborhood` | 节点 `id`、`direction`、`limit`、`after` | 指定节点的入边、出边或两者，以及相关节点 |

`where` 是 Property 键到等值匹配值的对象；例如 `{"status":"review"}`。匹配 `null` 时，只有明确存在且值为 `null` 的键匹配，缺失的键不匹配。`direction` 为 `in`、`out` 或 `both`。`after` 是上一页最后一个对象/边的 ID；继续分页时传入上一页 `source_version`。邻域深度为 1，按边 ID 分页。`limit` 默认为 100，最大 500，限定本页对象或邻域边的数量，不授权全量导出。

```json
{
  "project": "sample-project",
  "mode": "filter",
  "kind": "node",
  "where": {"status": "review"},
  "limit": 20,
  "source_version": 3
}
```

Query 虽然使用 POST，仍是只读操作，`reader` 可以调用。

`kind` 省略时为 `node`；`neighborhood` 只接受节点作为起点。各模式仅接受表中列出的字段以及 `project`、`mode`、`source_version`；例如 `inspect` 不接受 `limit`、`where` 或 `after`，不会默默忽略这些字段。

## Mutation 与回执

```http
POST /v1/graph/mutations
Authorization: Bearer …
Content-Type: application/json
```

```json
{
  "project": "sample-project",
  "request_id": "sample-change-1",
  "expected_revision": 3,
  "operations": [
    {"op": "node.add", "id": "question", "properties": {"label": "确认接入体验"}},
    {"op": "edge.add", "id": "asks", "source": "question", "target": "existing-node"}
  ]
}
```

`expected_revision` 是调用方依据的整数接受版本。一次请求包含 1–100 个操作，按给定顺序在一个后台事务中执行；任一前提不满足时整组拒绝，不留下部分修改。成功后版本递增并保存持久请求回执。

| 操作 | 必需字段 | 行为 |
| --- | --- | --- |
| `node.add` | `id` | 增加节点；可选 `properties`，省略为 `{}` |
| `node.remove` | `id` | 删除节点；仍有关联边时拒绝，先显式删边 |
| `edge.add` | `id`、`source`、`target` | 两端节点须存在；可选 `properties`，省略为 `{}` |
| `edge.remove` | `id` | 删除该边，保留端点 |
| `prop.set` | `kind`、`id`、`key`、`value` | 创建或替换属性；`kind` 为 `node` 或 `edge` |
| `prop.unset` | `kind`、`id`、`key` | 移除属性键，区别于设为 JSON `null` |

本轮没有 `edge.rewire`，改变端点需显式删旧边、创建新边。Properties 值采用能可靠往返的 JSON 数据，接口不接受可执行对象。

发送前生成并保留 `request_id`。同一 actor 以相同请求 ID 和相同内容重试时返回原回执并标记 `duplicate: true`，不重复修改；相同 ID 对应不同内容返回 `409`。查回已有回执也返回 `duplicate: true`。未知结果优先查询：

```http
GET /v1/graph/requests/sample-change-1?project=sample-project
Authorization: Bearer …
```

不存在的本人回执返回 `404`。成功回执只证明图变更已进入接受状态；它不证明 Agent 读过、接手任务、实现通过或用户验收。

以下为响应格式示例，不是真实运行回执：

```json
{
  "schema": "otoha-graph/1",
  "graph_id": "sample-project-graph",
  "source_version": 4,
  "scope": {"project": "sample-project", "mode": "mutation"},
  "complete": true,
  "truncated": false,
  "state": "accepted",
  "request_id": "sample-change-1",
  "expected_revision": 3,
  "request_sha256": "0000000000000000000000000000000000000000000000000000000000000000",
  "duplicate": false
}
```

回执中的 `source_version` 是原操作接受后的版本，即原请求的 `expected_revision + 1`。重试或查回回执保留这个版本，即使图已经有更新；需要知道当前版本时另读 Manifest。`request_sha256` 是服务端对规范化请求内容计算的摘要，用于核对操作，不是用户验收签名。

## 错误与恢复

错误响应形如 `{"error":"graph_revision_conflict"}`，不返回后台 SQL、登录信息或内部异常全文。

| HTTP | 代码 | 调用者需要处理的情况 |
| --- | --- | --- |
| `400` | `invalid_identifier`、`invalid_graph_integer`、`invalid_graph_request`、`invalid_graph_query` | 字段、模式、身份格式或版本参数错误 |
| `400` | `invalid_graph_operations`、`invalid_graph_operation`、`invalid_graph_kind`、`invalid_graph_properties` | 修改操作数量、名称或形状错误 |
| `400` | `invalid_graph_property_key`、`invalid_graph_value`、`graph_value_too_deep`、`graph_number_out_of_range` | 数据超出可往返值域 |
| `401` | `unauthorized` | 凭据缺失、已撤销或对应身份已禁用 |
| `403` | `project_forbidden`、`graph_write_required` | 当前身份不能访问项目，或不能修改图 |
| `404` | `project_not_found`、`graph_not_bound`、`graph_route_not_found` | 项目、绑定或路由不存在 |
| `404` | `graph_object_not_found`、`graph_request_not_found` | 对象或本人请求回执不存在 |
| `409` | `graph_revision_conflict` | 版本前提不成立，重读后重新判断 |
| `409` | `graph_request_id_conflict` | 同一请求 ID 对应不同内容，不能覆盖旧请求 |
| `409` | `graph_object_exists`、`graph_endpoint_not_found`、`graph_incident_edges` | 对象存在性、端点或显式删除前提不成立 |
| `409` | `graph_identity_conflict` | 私有后台绑定与已保存的逻辑图身份不一致，交管理员核对 |
| `413` | `body_too_large`、`graph_result_too_large` | 请求过大，或无法在结果上限内返回一个完整对象/邻域；缩小范围或数据后再读 |
| `503` | `graph_configuration_not_private`、`graph_configuration_invalid` | 管理员配置的权限或内容有误 |
| `503` | `graph_backend_unavailable`、`graph_backend_invalid_response`、`graph_backend_query_failed` | 后台连接或执行失败；修改结果未知时先用原请求 ID 查回 |

HTTP 层还会拒绝跨源请求、错误 JSON、非 JSON POST 或超过上限的请求体。拒绝不会变成另一种修改操作，也不通过调用者猜测的默认项目继续执行。

## 邀请信息发现

```http
POST /v1/invitation-info
Content-Type: application/json

{"invitation":"EXAMPLE_UNUSED_INVITATION"}
```

此接口无需 Bearer，持有有效邀请的人可在兑换身份前发现接入上下文。它只读现有邀请，不消费邀请、不创建身份、不生成长期凭据。返回字段仅为 `protocol`、`url`、`actor`、`project`、`channel`、`grants`、`expires`；不返回 invitation、凭据、个人配置或数据库信息。邀请须只授权一个项目 `P`；频道优先采用该项目对邀请身份可见的 `P-general`，否则必须只有一个可见频道。

- 邀请无效、过期或撤销：`403`。
- 邀请已经 claimed：`409`。
- 无法唯一确定一个项目和频道：`400`；不猜测默认项目或频道。

产品链接、短凭据的编码和安装分发不由此接口自动完成。它提供的是现有一次性邀请的只读上下文发现能力。

## 验证范围

授权测试使用隔离临时身份与虚构项目，并替换后台调用：覆盖匿名读取、跨项目读取、reader 写入拒绝、凭据和 grant 撤销、请求内容不能冒充身份、后台操作不持有全局锁，以及邀请发现不消费邀请。它们验证 API 授权边界，不代替真实数据库事务、持久回执、跨机器接入或真实浏览器验收；真实后台结果须另行记录。

2026-11-06，实现者报告本机 Mac 的后端最终验证 **53/53 通过，耗时 33.029 秒**：其中 [12 项使用隔离 SurrealDB 3.3.5](../services/collaboration/tests/test_graph.py)，[11 项替换后台验证 HTTP 授权与邀请发现](../services/collaboration/tests/test_graph_auth.py)，另有 30 项既有后端测试。

真实数据库测试覆盖自由属性、多重边与自环、null/缺键、修改回滚与删除、actor 范围回执、版本分页、一阶邻域、含点与连字符的对象身份、数据库重启后恢复、真实 HTTP 身份链、完整对象字节截断与续读、输入拒绝，以及 29 层 Property 值往返和 33 层拒绝。替换后台的授权测试与真实数据库测试分别证明其覆盖的边界。这是本机 Mac 隔离环境的测试结果，不是 Linux 私服部署验收、跨机器接入验收、真实团队采用或用户验收。

# 传输层设计:统一信封、双 Transport 与主备切换

状态:统一 Transport 仍是演进中的设计；Telegram Gateway、中心服务 bridge 和本机 inbox 已有 FakeChannel 风格联调。下文区分三类内容:**现状**(已读代码确认)、**设计**(本文提出)、**待验证**(依赖真实 Telegram 或服务器行为)。

当前实现位于 `apps/cli/internal/otoha/telegram.go` 与 `transport_bridge.go`：提供 `Channel` 接口、Telegram `sendMessage`/`getUpdates`、本机 JSON 状态持久化、chat 到 Session 路由、更新与 envelope ID 去重，以及中心服务事件 ↔ Channel ↔ 本机 inbox 的双向 bridge。CLI 入口包括 `otoha telegram send|receive|status|bridge`。SQLite 本机库、名册发送者校验、Codex 会话投递、长消息分片、真实应用层回执和多 Transport 主备仍未实现。

## 目标

- 一套上层协议,两种可替换的 Transport:中心化服务器(Otoha Collaboration)和 Telegram Bot。
- 消息有两种范围:群发(频道)与私发(指定接收者)。
- 摘要与正文分离:正文完整保存,通知只带一句摘要和引用,接收方按紧急程度与任务边界取正文。
- 同一项目可同时配置两个 Transport,指定默认项;默认只用一个,另一个故障时顶上。
- 两个 Transport 对 Agent 呈现同样的效果,Agent 不感知底层。

## 现状

- 服务端消息表只有 `payload` JSON,没有独立摘要字段。
- `to_sessions` 只是投递意图,不是私聊权限。需要限制可见性时要另建带成员名单的频道(`channels.members`)。
- 发送已有 `client_id` 幂等,CLI 在传输前先持久化 ID。
- `apps/cli/internal/otoha/gateway.go` 仍是 HTTPS 反向代理；Transport 抽象和 Telegram Gateway 位于独立的 `telegram.go`，bridge 不改变中心服务的权威消息模型。

## 统一信封

信封与 Transport 无关,是跨 Transport 去重和补同步的依据。

```json
{
  "v": 1,
  "id": "稳定操作 ID,发送前生成并先落盘",
  "project": "项目 ID",
  "scope": "channel | direct",
  "channel": "scope=channel 时的频道",
  "to": ["scope=direct 时的接收者 actor/session"],
  "from": {"actor": "...", "session": "..."},
  "kind": "message | task | result | question | status",
  "urgency": "interrupt | boundary | idle",
  "summary": "一句话以内,通知只携带这个",
  "body": "完整正文,仅存于正文存储,通知中不出现",
  "reply_to": "另一条信封的 id,或 null",
  "refs": [],
  "part": {"seq": 1, "total": 1}
}
```

- `id` 是全局去重键。同一条消息经两个 Transport 到达也只算一条。
- `summary` 必填。发送方没给时由 CLI 取正文首句截断,并标记为 `auto`。
- `urgency` 决定接收时机:`interrupt` 立即通知,`boundary` 在任务边界查收,`idle` 空闲时查收。它不改变权限,也不改变保存方式。
- 通知对象是 `{id, from, scope, summary, urgency, ref}`,不含正文。`ref` 由 Transport 解析:服务器为消息 ID,Telegram 为 `chat_id/message_id` 列表。

## 群发与私发

| | 中心化服务器 | Telegram |
| --- | --- | --- |
| 群发 | 频道消息,按频道授权读取 | 发到项目群 |
| 私发 | **设计**:`scope=direct` 一等语义,服务端按 `to` 限制可见,发送方、接收方和服务所有者可读 | 发到接收者与 Bot 的私聊 |

- 私发不能降级为群发。接收者不可达时发送失败并返回明确原因,不会改发到群里。
- 服务端新增 `direct` 范围后,`to_sessions` 继续只表示投递意图,语义不变。
- Telegram 私聊要求接收者先对 Bot 发过 `/start`。接入流程需要引导这一步,并在未完成时报 `recipient_unreachable`。
- 群里 Bot 默认受 privacy mode 限制,可能看不到普通群消息。**待验证**:关闭 privacy mode 或设为管理员后的实际可见范围。

## 正文存储

两种配置下,完整正文都在本地库中有一份,通知再从本地库的摘要和引用生成。

- 服务器模式:正文在服务端消息表,引用是消息 ID。
- Telegram 模式(已选定):Telegram 原消息承载完整正文,各节点同时写入本地 SQLite。引用是 Telegram 消息坐标。
- Telegram 消息首行为机器可读头(`id`、`scope`、`kind`、`urgency`、`part`),随后是摘要行,再是正文。头部格式在编码前单独定稿。
- 超过单条上限(4096 字符,来自调研文档,**待验证**)时拆成多条,每条带 `part`,接收端按 `id` 与 `seq` 重组后才视为收到。
- Telegram 对未确认 update 只保留 24 小时,所以不能当长期邮箱。接收端先写本地 inbox,再确认游标。

## Transport 接口

```text
Send(envelope)          -> 成功坐标 | 明确失败 | 结果未知
Poll(cursor)            -> 信封列表, 新游标
FetchBody(ref)          -> 完整信封
Health()                -> up | degraded | down, 原因
Capabilities()          -> 是否支持 direct、最大单条长度、速率限制
```

- 「结果未知」是独立的返回值,例如网络超时。处理方式见下一节,不能当作失败直接重发。
- Telegram 实现内置限流器:同一聊天默认间隔不低于 1 秒,遵守 429 的 `retry_after`,超限进入有容量上限的持久队列。
- 一个 Bot Token 对应本地一个常驻进程,多个 Agent Session 通过它统一收发,再按信封路由。

## 路由与主备

配置示例,不含任何凭据:

```json
{
  "transports": {
    "server": {"type": "collab", "url": "https://example.invalid"},
    "telegram": {"type": "telegram", "token_file": "PRIVATE/telegram-token", "group": "项目群 ID"}
  },
  "default": "server",
  "standby": "telegram",
  "failover": {"after_failures": 3, "probe_seconds": 30, "fail_back": true}
}
```

- 发送:默认只走 `default`。`standby` 不发送,除非发生切换。
- 接收:两个 Transport 都监听,所有信封按 `id` 去重。这样 `default` 掉线时已经在备用侧到达的消息不会漏。
- 切换:`default` 连续失败达到阈值,或 `Health` 为 `down`,后续发送改走 `standby`,并写一条本地事件记录切换原因。
- 回切:`default` 探测恢复后回切。回切前对两边按各自游标补拉,再按 `id` 合并。
- 结果未知:同一信封 `id` 原样重试,不生成新 ID。服务器有幂等保护。Telegram 没有服务端幂等,可能出现重复消息,所以接收端按 `id` 丢弃重复。
- 只配置一个 Transport 时,上述逻辑退化为单通道,行为不变。
- 另一种方式「双写」不作默认,留作可选策略。

## 收到、阅读与回执

沿用现有分层:收到、投递、阅读、接手、交付、验收分别记录。

- 服务器回执沿用现有 `received`/`read` 游标。
- Telegram 没有回执,**设计**:本地记录 `received`/`read`,发送方可选地用一条 `status` 信封回传阅读确认。是否默认开启待定。
- 实际阅读之后才记 `read`,通知到达不算阅读。

## 实现范围与顺序

1. 确认本文,定稿信封字段与 Telegram 头部格式。
2. 服务端:`summary` 字段与 `direct` 范围,含迁移和测试。
3. Go CLI:Transport 接口、服务器实现、本地 outbox/inbox、去重。
4. Telegram 实现:收发、拆分重组、限流、`/start` 引导。
5. 路由器:主备配置、健康探测、切换与回切。
6. 集成测试使用独立临时目录、测试身份和 Telegram 测试 Bot,不触碰真实用户状态与已有 receiver。

## 未决与风险

- Telegram 私聊必须由接收者先 `/start`,且 Bot 无法读到自己发出的历史,重装恢复要依赖本地库。
- 大量机器内部消息走 Telegram 有违反 Bot 条款的风险。限流只能降低,不能消除。默认只让需要人或其他节点看到的消息进入 Telegram。
- 真实 Telegram 行为(privacy mode、Bot 之间互相可见、4096 上限)尚未核对,编码前需要用测试 Bot 验证。
- 两个 Transport 的访问控制模型不同:服务器按身份授权,Telegram 按群成员与 Bot 私聊。权限不能靠信封里的字段获得,每个 Transport 各自执行。

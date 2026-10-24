# 研究 Telegram 通信适配

> Conversation URL: https://chatgpt.com/g/g-p-6ad87e33003881919e41568cb06c56b8-agent-orchestration/c/6ad86c07-9f94-83e8-bff2-afafd6b859f9?src=history_search
> Export time: 2026-10-24 19:41:19

---

**Q：**

Agent能不能借用 Telegram 或者类似的社交软件通信？Telegram 有没有 headless 模式？避免让 Agent 像人操作那样用鼠标去点来点去，那样效率太低了。 Telegram 上的机器人不是有个 token 吗？现在很多产品比如 hermes 都可以利用上那个 token。能不能研究一下，让它能适配我 GitHub 上 team mailbox 或者 system Atlas 那种通信协议？还有 Atlas CLI，也就是那几个项目里面的通信协议，能不能做到？

**A：**

完全可以，而且 Telegram 可以直接作为你现有多 Agent 系统的通信传输层（Transport Layer）。 不需要让 Agent 打开 Telegram 界面，更不需要模拟鼠标点击。

我已经查到一个特别值得注意的功能：Telegram 官方现在提供了 Bot-to-Bot Communication Mode，允许符合条件的机器人互相通信。这让你设想的多 Agent 协作比传统 Telegram Bot 方案更容易实现。

但我建议区分两个目标：

1. 让 Telegram 承载 Agent 消息：非常容易，现成 API 就能完成。
2. 让 Telegram 成为 Agent 系统的通信基础设施：可以实现，但消息格式、身份、可靠投递、任务状态仍应由 Atlas 管理。

对你的项目而言，最值得研究的不是重新发明 Telegram Agent，而是给 Atlas 添加一个可插拔的 Telegram Transport Adapter，同时保留现有协议。

## 一、Telegram 本身就支持 Headless

Telegram 不需要提供一个专门的“Headless 客户端模式”，因为它的 Bot API 本来就是供程序直接调用的 HTTP 接口。

![](data:image/svg+xml;charset=utf-8,%3Csvg%20font-family%3D%22-apple-system-body%2C%20ui-sans-serif%2C%20-apple-system%2C%20system-ui%2C%20%26quot%3BSegoe%20UI%26quot%3B%2C%20Helvetica%2C%20%26quot%3BApple%20Color%20Emoji%26quot%3B%2C%20Arial%2C%20sans-serif%2C%20%26quot%3BSegoe%20UI%20Emoji%26quot%3B%2C%20%26quot%3BSegoe%20UI%20Symbol%26quot%3B%22%20font-weight%3D%22400%22%20data-d-component%3D%22svg%22%20fill%3D%22currentColor%22%20style%3D%22color%3Argb(221%2C%20218%2C%20213)%22%20viewBox%3D%220%200%20340%20180%22%20width%3D%22100%25%22%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%3E%3Crect%20x%3D%2210%22%20y%3D%2210%22%20width%3D%22132%22%20height%3D%2246%22%20rx%3D%229%22%20fill%3D%22%232563eb%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%232563eb%22%2F%3E%3Ctext%20x%3D%2276%22%20y%3D%2229%22%20font-size%3D%2212%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%20font-weight%3D%22600%22%3EAgent%20A%3C%2Ftext%3E%3Ctext%20x%3D%2276%22%20y%3D%2244%22%20font-size%3D%2210%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%3EAtlas%20CLI%3C%2Ftext%3E%3Crect%20x%3D%22198%22%20y%3D%2210%22%20width%3D%22132%22%20height%3D%2246%22%20rx%3D%229%22%20fill%3D%22%232563eb%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%232563eb%22%2F%3E%3Ctext%20x%3D%22264%22%20y%3D%2229%22%20font-size%3D%2212%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%20font-weight%3D%22600%22%3EAgent%20B%3C%2Ftext%3E%3Ctext%20x%3D%22264%22%20y%3D%2244%22%20font-size%3D%2210%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%3EAtlas%20CLI%3C%2Ftext%3E%3Cpath%20d%3D%22M76%2056V75%20M264%2056V75%22%20fill%3D%22none%22%20stroke%3D%22%2364748b%22%20stroke-width%3D%221.5%22%2F%3E%3Crect%20x%3D%2210%22%20y%3D%2275%22%20width%3D%22132%22%20height%3D%2238%22%20rx%3D%227%22%20fill%3D%22%230d9488%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%230d9488%22%2F%3E%3Ctext%20x%3D%2276%22%20y%3D%2298%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%20font-size%3D%2212%22%3EBot%20Token%20A%3C%2Ftext%3E%3Crect%20x%3D%22198%22%20y%3D%2275%22%20width%3D%22132%22%20height%3D%2238%22%20rx%3D%227%22%20fill%3D%22%230d9488%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%230d9488%22%2F%3E%3Ctext%20x%3D%22264%22%20y%3D%2298%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%20font-size%3D%2212%22%3EBot%20Token%20B%3C%2Ftext%3E%3Cpath%20d%3D%22M76%20113V137H145%20M264%20113V137H195%22%20fill%3D%22none%22%20stroke%3D%22%2364748b%22%20stroke-width%3D%221.5%22%2F%3E%3Crect%20x%3D%22112%22%20y%3D%22132%22%20width%3D%22116%22%20height%3D%2239%22%20rx%3D%228%22%20fill%3D%22%2364748b%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%2364748b%22%2F%3E%3Ctext%20x%3D%22170%22%20y%3D%22149%22%20font-size%3D%2212%22%20font-weight%3D%22600%22%20text-anchor%3D%22middle%22%20fill%3D%22currentColor%22%3ETelegram%20API%3C%2Ftext%3E%3Ctext%20x%3D%22170%22%20y%3D%22162%22%20font-size%3D%229%22%20text-anchor%3D%22middle%22%20fill%3D%22currentColor%22%3EMessage%20Transport%3C%2Ftext%3E%3C%2Fsvg%3E)

全部通过 API 请求和事件接收完成，不需要 GUI。

最核心的三个操作：

| 目的 | Telegram Bot API |
| --- | --- |
| 发送消息 | `sendMessage` |
| 接收消息 | `getUpdates`（Long Polling） |
| 服务端推送 | `setWebhook` |

例如，Agent 发送一条消息，只需要：

Bash

```
curl -sS -X POST \
  "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage" \
  -d "chat_id=${CHAT_ID}" \
  --data-urlencode "text=TASK_COMPLETED"
```

这里的 Token 本质是机器人身份凭证，不是一个自动运行的 Agent。Agent 进程依然运行在你的 Mac、Linux 服务器或其他计算节点上。

官方文档：[Telegram Bot API](https://core.telegram.org/bots/api?utm_source=chatgpt.com)。

## 二、我实际检查了你 GitHub 上的三个协议

我从你的 GitHub 账号 `Vioano` 读取了相关 README、协议文档和部分实现代码。

| 项目 | 当前通信方式 | Telegram 适配判断 |
| --- | --- | --- |
| [Team Mailbox](https://github.com/Vioano/huaweicup2026/blob/main/.agents/skills/team-mailbox/SKILL.md?utm_source=chatgpt.com) | GitHub Issues、评论、@成员、手动或 Hook 查收 | 容易适配，可增加实时通知 |
| [System Atlas](https://github.com/Vioano/system-atlas?utm_source=chatgpt.com) | Git 传输签名变更请求与权威快照 | 可适配，但不应直接替换快照存储 |
| [Atlas CLI](https://github.com/Vioano/atlas-cli?utm_source=chatgpt.com) | 私有 Atlas 服务、HTTP 长轮询、持久游标、Codex 投递 | 最适合作为 Telegram 桥接基础 |

### 1. Team Mailbox：可以直接增加 Telegram 接收器

我检查了你的 `mailbox.py` 和 `references/protocol.md`。

它的核心协议其实很简单：GitHub Issue 是一个话题，评论是消息，`@LOGIN` 是寻址规则，PR 负责代码交付。

目前最大的缺口是 持续接收与空闲唤醒。你的 Codex Hook 只在特定工作事件发生时检查 GitHub，而不是持续监听。

Telegram 可以填补这个缺口：

GitHub Issue / Comment

保留原始讨论、任务和 PR 链接

GitHub Webhook → Telegram Bot

产生新消息通知，附带 Issue 和 Comment ID

Agent Receiver → Codex Session

按消息 ID 去重，并投递给指定会话

不必推翻原有的 Issue 交流协议。Telegram 先作为低延迟通知通道，GitHub 仍是历史记录来源。

### 2. System Atlas：保留签名、版本和权威状态

我特别检查了：

[team/protocol.mjs](https://github.com/Vioano/system-atlas/blob/main/team/protocol.mjs?utm_source=chatgpt.com)、[team/transport.mjs](https://github.com/Vioano/system-atlas/blob/main/team/transport.mjs?utm_source=chatgpt.com) 和 [references/collaboration.md](https://github.com/Vioano/system-atlas/blob/main/references/collaboration.md?utm_source=chatgpt.com)。

你的设计已经包含：

- Ed25519 签名和成员公钥身份。
- `requestId`、`baseCursor` 和字段版本检查。
- 权限白名单、原子处理、冲突拒绝。
- 队长电脑维护权威图，远程成员只提交变更请求。

其中 `GitTransport` 实现的是一套基于 Git 对象和同步分支的传输机制。

我不建议把这种机制直接改成“Telegram 消息就是权威图版本”。

更合理的实现是，Telegram 发送变更通知和请求引用，由 System Atlas 原有协议验证签名、检查字段版本、执行变更，再通过同一权威后端查询结果。

这能保留 System Atlas 的核心性质：传输来源不决定事实是否被接受。

### 3. Atlas CLI：其实已经有接近完整的通信底座

这是我认为最有价值的发现。

阅读 [internal/atlas/receive.go](https://github.com/Vioano/atlas-cli/blob/main/internal/atlas/receive.go?utm_source=chatgpt.com) 后，我发现你的 Atlas CLI 不只是发消息的工具。

它已经实现了常驻 receiver、长轮询、持久事件游标、重连退避、发送者授权检查，以及向 Codex Desktop/CLI 投递消息的适配逻辑。

更重要的是，它区分了三个阶段：

消息接收

投递排队

Agent 已读

你的代码明确规定：Codex queue 接受了消息，不代表 Agent 真正阅读了它。需要独立的 `atlas receipt --read`。

这是通信可靠性中很有实际意义的设计。

因此，Atlas CLI 已有的接收与投递机制可以直接复用，Telegram 只需要接入它的消息输入/输出边界。

## 三、推荐架构：让 Atlas 与 Telegram 分工

推荐的多 Agent 通信结构

Agent A

Codex / Hermes

Agent B

Codex / Hermes

Atlas Communication Layer

身份 · 路由 · ACL · 去重 · Receipt · 历史记录

Telegram

Bot API

Atlas Native

SSH / HTTPS

GitHub / Gitea

Issues / Git

System Atlas

共享工程图谱、任务板、版本和权威状态

这里 Telegram、原生 HTTP 和 Git 都只是 Transport。

例如，Agent A 想让 Agent B 检查一个模块，Agent A 不需要知道 B 在用 Telegram、Discord 还是原生 Atlas CLI。

它只发标准化消息：

JSON

```
{
  "id": "msg-001",
  "from": "agent-a",
  "to": "agent-b",
  "type": "review.request",
  "project": "demo",
  "payload": {
    "module": "parser",
    "graph_cursor": 42,
    "commit": "a1b2c3"
  }
}
```

这是我建议的统一消息信封示例，不是你仓库里现成的 Schema。

Adapter 负责将它转换为 Telegram 消息或 Atlas 原生请求。接收者仍要检查授权、版本与引用的真实内容。

尤其应该保留你已有的 `client_id`、`channel`、`session` 等字段语义，而不是为了 Telegram 重新发明身份模型。

## 四、Telegram 有几个需要注意的技术限制

Bot-to-Bot 现在确实支持

根据 Telegram 最新的官方功能说明，机器人私聊可以通过 `@username` 寻址，但双方都需要开启 Bot-to-Bot Communication Mode；群聊可以通过命令提及或者回复进行路由。

不能把 Telegram 当作无限持久化的队列

官方 Bot API 只保证未领取的 Update 保留不超过 24 小时。普通 `sendMessage` 文本也有 4096 字符限制。大量通信还会遇到限流。

因此离线超过一天的 Agent 不能只靠 Telegram 的 `getUpdates` 恢复完整事件历史。对于可靠补收，Atlas 自己的事件存储更合适。

Token 不是项目权限

Telegram Bot Token 只是控制机器人身份的凭证。不能仅凭某条消息宣称自己来自 `agent-a` 就授予相应权限。

可以校验 Telegram 的发送者 Bot ID，将其映射到已经注册的 Atlas actor，再应用原有 ACL；System Atlas 的敏感图变更还应继续验证签名。

另外，Telegram Bot API 不是端到端加密通信通道。代码密钥、生产环境凭证和高敏感内容不宜直接作为普通 Bot 消息发送。

一个 Bot Token 不应对应多个独立 Poller

如果你给每个 Agent 都使用同一个 Token，然后同时执行 `getUpdates`，这些接收器会相互竞争。更好的方式是使用一个接收网关路由给多个 Agent，或者为需要独立 Telegram 身份的 Agent 创建不同的 Bot。

## 五、Hermes 其实证明了这条路线很实用

你提到的 Hermes Agent 已经提供 Telegram Gateway。

它支持 BotFather Token、群聊、Topic、用户授权和多 Bot 配置，还提供 `hermes send` 一次性发送命令，不需要启动完整 Agent 推理循环。

不过它主要解决的是 如何把消息送到 Hermes Agent，并不会自动兼容你现有的 Atlas 身份、会话、已读回执与图版本机制。

适配可以发生在两个位置：Hermes 作为 Agent Runtime 接收 Atlas 消息，或者 Hermes 现有 Telegram Gateway 作为 Atlas 的入口。这样无需重写 Hermes 的 Telegram 客户端。

## 六、我建议的实际开发顺序

第一阶段可以只做一个很小的验证：两个 Telegram Bot 开启 Bot-to-Bot 模式，通过 HTTP API 发收一条带消息 ID 的结构化消息，检查断线、重复更新和机器人互相回复。无需 Codex，也无需完整 Atlas 服务器。

第二阶段，把这个最小收发器接入 Atlas CLI。推荐新增独立 `telegram` Adapter，并复用 `receive.go` 的消息投递流程和现有回执机制。优先实现从 Telegram 收取消息并投递到指定 Codex 会话，再补 Atlas 消息向 Telegram 的出站桥接。

第三阶段，再实现 Team Mailbox 与 System Atlas 的事件集成：Issue 有新评论时通知 Agent、Atlas 接受图变更时推送版本事件，让 Agent 可以重新查询对应图节点。

这一阶段还需要验证 Telegram 的送达回执、Atlas 的实际已读回执，以及跨网络中断的重放行为，没有跑通前不应宣称达到端到端可靠投递。

## 最终判断

如果你只是想让两个 Agent 通过 Telegram 对话，Telegram Bot API 就足够了，甚至不需要 Atlas CLI。

但如果目标是你之前构想的 跨 Agent、跨机器、跨模型、可以动态改变调度策略的通用通信协议，我认为值得把 Atlas CLI 继续发展成独立的通信层，并让 Telegram 成为第一个外部 Transport。

最关键的产品设计原则是：

Agent 应该通过统一协议通信，而不是通过统一聊天软件通信。

聊天软件提供现成的消息网络、账号体系以及方便人类观察的界面；Atlas 提供真正的 Agent 身份、权限、任务上下文、可靠性和可替换的路由策略。

这三个仓库已经有不少可以直接复用的基础。当前不需要引入 Kafka、复杂分布式调度器或者重新建一套完整的消息中间件。更值得做的是以现有 Atlas CLI 为核心，完成一个真正跨网络、跨 Agent Runtime 的 Telegram 双向通信实验。

以上是基于仓库源码和 Telegram 官方文档的架构审查；我还没有修改仓库或执行真实双 Bot 联调。

![](data:image/svg+xml;charset=utf-8,%3Csvg%20font-family%3D%22-apple-system-body%2C%20ui-sans-serif%2C%20-apple-system%2C%20system-ui%2C%20%26quot%3BSegoe%20UI%26quot%3B%2C%20Helvetica%2C%20%26quot%3BApple%20Color%20Emoji%26quot%3B%2C%20Arial%2C%20sans-serif%2C%20%26quot%3BSegoe%20UI%20Emoji%26quot%3B%2C%20%26quot%3BSegoe%20UI%20Symbol%26quot%3B%22%20font-weight%3D%22400%22%20data-d-component%3D%22svg%22%20fill%3D%22currentColor%22%20style%3D%22color%3Argb(221%2C%20218%2C%20213)%22%20viewBox%3D%220%200%20340%20180%22%20width%3D%22100%25%22%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%3E%3Crect%20x%3D%2210%22%20y%3D%2210%22%20width%3D%22132%22%20height%3D%2246%22%20rx%3D%229%22%20fill%3D%22%232563eb%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%232563eb%22%2F%3E%3Ctext%20x%3D%2276%22%20y%3D%2229%22%20font-size%3D%2212%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%20font-weight%3D%22600%22%3EAgent%20A%3C%2Ftext%3E%3Ctext%20x%3D%2276%22%20y%3D%2244%22%20font-size%3D%2210%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%3EAtlas%20CLI%3C%2Ftext%3E%3Crect%20x%3D%22198%22%20y%3D%2210%22%20width%3D%22132%22%20height%3D%2246%22%20rx%3D%229%22%20fill%3D%22%232563eb%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%232563eb%22%2F%3E%3Ctext%20x%3D%22264%22%20y%3D%2229%22%20font-size%3D%2212%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%20font-weight%3D%22600%22%3EAgent%20B%3C%2Ftext%3E%3Ctext%20x%3D%22264%22%20y%3D%2244%22%20font-size%3D%2210%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%3EAtlas%20CLI%3C%2Ftext%3E%3Cpath%20d%3D%22M76%2056V75%20M264%2056V75%22%20fill%3D%22none%22%20stroke%3D%22%2364748b%22%20stroke-width%3D%221.5%22%2F%3E%3Crect%20x%3D%2210%22%20y%3D%2275%22%20width%3D%22132%22%20height%3D%2238%22%20rx%3D%227%22%20fill%3D%22%230d9488%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%230d9488%22%2F%3E%3Ctext%20x%3D%2276%22%20y%3D%2298%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%20font-size%3D%2212%22%3EBot%20Token%20A%3C%2Ftext%3E%3Crect%20x%3D%22198%22%20y%3D%2275%22%20width%3D%22132%22%20height%3D%2238%22%20rx%3D%227%22%20fill%3D%22%230d9488%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%230d9488%22%2F%3E%3Ctext%20x%3D%22264%22%20y%3D%2298%22%20fill%3D%22currentColor%22%20text-anchor%3D%22middle%22%20font-size%3D%2212%22%3EBot%20Token%20B%3C%2Ftext%3E%3Cpath%20d%3D%22M76%20113V137H145%20M264%20113V137H195%22%20fill%3D%22none%22%20stroke%3D%22%2364748b%22%20stroke-width%3D%221.5%22%2F%3E%3Crect%20x%3D%22112%22%20y%3D%22132%22%20width%3D%22116%22%20height%3D%2239%22%20rx%3D%228%22%20fill%3D%22%2364748b%22%20fill-opacity%3D%22.12%22%20stroke%3D%22%2364748b%22%2F%3E%3Ctext%20x%3D%22170%22%20y%3D%22149%22%20font-size%3D%2212%22%20font-weight%3D%22600%22%20text-anchor%3D%22middle%22%20fill%3D%22currentColor%22%3ETelegram%20API%3C%2Ftext%3E%3Ctext%20x%3D%22170%22%20y%3D%22162%22%20font-size%3D%229%22%20text-anchor%3D%22middle%22%20fill%3D%22currentColor%22%3EMessage%20Transport%3C%2Ftext%3E%3C%2Fsvg%3E)
> Image：A · 283x150

---

**Q：**

Telegram 的 BOT 有没有什么限制？我怕通信太多会不会把我给封号了。之前 GitHub 我怀疑就是因为类似的原因用了太多 issue 给我封号了。还是说它承载的能通信量比较充足，一般情况下不用担心触碰到它的限制？

**A：**

Telegram 的 Bot API 容量对于普通 Agent 协作是比较充足的，但如果你准备让几十个 Agent 高频交换内部事件，我不建议把 Telegram 当作主要消息总线。

我查了 Telegram 官方的限流文档和 Bot 开发者服务条款。你的担心有道理，而且需要区分 API 限流 和 账号封禁：二者并不是同一回事。

## 一、Telegram 官方的发送速率限制

| 通信场景 | 官方限制 | 我的评价 |
| --- | --- | --- |
| 单个私聊 | 建议不超过 1 条/秒 | 一对一 Agent 通信足够 |
| 单个群组 | 每个 Bot 最多约 20 条/分钟 | 高频群聊容易碰到 |
| Bot 广播 | 免费约 30 条/秒 | 足以进行大量事件通知 |
| 付费广播 | 最高 1000 条/秒 | 不适合解决内部消息总线问题 |

这些是官方公布的发送限制，并不意味着系统保证你可以长期、持续地以这个最大速率运行。尤其广播的 30 条/秒不能等同于向同一个聊天无限发送 30 条/秒。

## 二、具体到你的 Agent 场景

假设你安排 5 个 Agent 在 Telegram 群里交流，下面是几种不同的流量情况。

Agent 通信量估算器

Agent 数量

## 5

每个 Agent 平均发送一条消息的间隔

60 秒 — 低频协作30 秒 — 日常协作10 秒 — 活跃讨论2 秒 — 高频自动通信

整个群组每分钟

# 10 条

每个 Agent 每分钟

# 2 条

从公开速率限制来看，有较充足余量；仍须遵守平台的反滥用规则。

假设所有 Agent 持续按固定间隔发送且各自使用独立 Bot。此处估算的是平均流量，不是 Telegram 的封号概率。

对于任务下发、技术讨论、状态报告和完成通知，这个容量一般已经很充足了。

但如果你把每一次 Agent 工具调用、心跳、Token 生成进度、任务内部状态变化全部发送到 Telegram，消息量很快就会失去控制。

## 三、超过限制会不会直接封号？

正常的超速行为通常先表现为 HTTP 429，而不是立刻封号。

Telegram Bot API 有 `retry_after` 字段，告知客户端在超出 Flood Control 后应该等待多少秒再试。

例如：

JSON

```
{
  "ok": false,
  "error_code": 429,
  "parameters": {
    "retry_after": 12
  }
}
```

这意味着客户端需要等待，再重新投递；不能把这类响应当作消息发送成功。

不过，你更应该关注的是 Telegram 的 Bot Developer Terms。官方明确禁止垃圾消息、绕过限流，以及将 Bot 平台用于严重偏离其预期用途的外部服务（例如云存储服务）。违反条款可能导致 Bot 被停用，甚至关联的 Telegram 账号受到处罚。

因此：

- 私有、正常速率的 Bot-to-Bot Agent 交流：Telegram 官方已经明确支持这种功能。
- 大量、持续、纯机器内部事件传输：是否被视为偏离 Bot 平台用途，没有公开的明确安全阈值。
- 通过创建大量 Bot、轮换 Token 或账号来规避限制：属于官方明确禁止的行为。

至于你此前的 GitHub 封号，单凭 Issue 使用量无法确定原因。Telegram 也没有公开一个“低于多少条消息就绝对不会封号”的数值。

## 四、我更推荐你采用双层通信架构

你现有 Atlas CLI 已经具备自己的通信服务和消息持久化机制，所以可以采用：

Agent A / B / C / D

任意模型与 Agent Runtime

Atlas Native Transport

高频消息、任务事件、状态同步、可靠队列主要数据通道

Telegram Bot Gateway

任务开始、重要讨论、错误、完成通知、人类干预

我建议先采用下面这些工程默认值，而不是把它们理解为 Telegram 官方的封号安全标准：

- Telegram 单群所有 Agent 合计控制在平均 5–10 条/分钟，短时突发排队。
- 同一个 Bot 向同一个私聊发送消息，默认最多 1 条/秒，并留出余量。
- 对 429 严格遵守 `retry_after`，失败消息留在本地持久队列，不进行无休止重试。
- 为 Bot-to-Bot 通信设置去重、最大回复深度和循环检测。Telegram 官方也特别强调这一点。
- 不通过增加 Bot 数量来规避平台限流；需要更高吞吐时走 Atlas 原生通信。

另外 Telegram 对未接收的 Bot Update 最多保留 24 小时，所以也不能把它当作可靠的长期离线邮箱。

## 我的结论

你完全可以放心地把 Telegram 用于低频、面向人类可观察的 Agent 协作通信。 但如果你的长远目标是几十到上百个 Agent 并发工作，Atlas 原生协议应该承载主要流量。

事实上，这可能比全部使用 Telegram 更好：Agent 之间可以以机器速度交流，而 Telegram 只展示经过筛选的重要信息。

这样既减少平台封禁风险，又能避免你的 Telegram 群被数万条没有阅读价值的内部日志淹没。

---

**Q：**

那我直接在通信协议的脚本层设置限制，不让 Agent 以过高的频率发消息，事实上也用不到这么高的频率。大部分情况下都是几分钟甚至几十分钟才会发一条

**A：**

对。如果你的 Agent 通常几分钟甚至几十分钟才通信一次，那么 Telegram 的 Bot API 完全可以满足这个需求。 在这种场景下，我甚至认为没必要为了通信吞吐量额外部署一套消息服务器。

而且，你提出的做法很合理：直接在通信协议的脚本层强制限流，而不是要求 Agent 自觉控制发送频率。

这有一个重要优势：无论 Agent 使用什么模型、出现什么异常行为，最终都绕不开通信层的限制。

我建议把限流器放在 Atlas CLI 的 Telegram Adapter 中：

Plain textAgent A  │  │ atlas send  ▼ Atlas Communication Adapter  │  ├── 检查发送权限  ├── 检查频率限制  ├── 消息去重  ├── 必要时加入等待队列  │  ▼ Telegram Bot API  │  ▼ Agent B

例如，你可以默认限制同一个 Bot 向同一个聊天每 5 秒最多发送一条消息，超过限制时进入队列，而不是立即报错或者直接丢弃消息。

即使 Agent 因为程序错误突然生成 100 条消息，通信层也会逐步发送，不会产生瞬间请求洪峰。

Telegram 官方建议单个聊天避免超过每秒一条、单个 Bot 在群组内不超过每分钟 20 条。你设置的 5 秒间隔比这些限额保守得多。

不过，实施时有三个细节值得保留：

1. 全局统一限流：如果同一个 Token 被多个 Agent 使用，必须通过共享限流器发送，不能让每个进程各自计时。
2. 持久队列与优先级：普通消息可以排队，紧急停止指令可以优先发送；队列需要有容量上限，避免异常情况下无限积压。
3. 处理服务端限流：收到 `429` 时遵守 `retry_after`，不能只依赖固定的 5 秒间隔。

还有一个我觉得很适合你的优化：按消息类型决定是否发送，而不是只控制消息频率。

例如，Agent 内部计算进度不需要发 Telegram；任务分配、请求协作、重要发现、完成报告需要发。这样不仅减少 Telegram 调用，更重要的是减少 Agent 被无意义消息打断的次数。

在你描述的通信频率下，正常私有协作触碰速率限制的风险很低。不过，低频不等于平台保证不封号，仍须避免垃圾消息、未经请求的群发及违反平台条款的行为。

所以我的建议是：先直接把 Telegram 接入 Atlas CLI，不必因为担心吞吐量而建设额外的通信基础设施。 等真实运行数据证明 Telegram 成为瓶颈，再考虑将高频流量分流到原生 Atlas 通道。

这也符合你之前强调的工程原则：先建立通用协议和必要边界，不为尚未出现的问题提前制造复杂性。

---

**Q：**

Agent 应该是通过一个程序持续地收发消息。 1. 发消息： 正常情况下没有消息积压，都是瞬间发过去。大部分情况都是这样，只有少部分可能出故障了，程序会自动拆分，自动设置好时间间隔，避免超过它的频率限制。 2. 收消息： 收消息也是一样的。因为不同人会有不同的 session，不同 Agent 也会有不同 session，程序是收完之后再统一路由，统一解析消息的字段，然后再路由或者填入数据库中。 这样即使只用一个 token，也能帮助本地运行的所有（比如 10 个）Agent session 收发消息。

**A：**

对，你描述的其实就是一个**单 Bot Token、多 Agent Session、统一消息网关（Message Gateway）**的架构。

而且我认为这是目前最适合你的方案。10 个本地 Agent Session 不需要 10 个 Telegram Bot。一个 Bot Token，加上一个常驻收发程序，就可以统一管理所有 Session 的通信。

关键是把 Telegram 的账号身份和 Atlas 的 Agent 身份分开。

## 一、整体架构

Telegram Gateway — 单 Token 多 Session

Telegram Bot API

一个 Bot Token，负责全部对外通信

Gateway Daemon（常驻进程）

独立运行，不消耗模型推理 Token

Receiver

监听 Telegram Updates

Router

解析字段、分发 Session

Sender

发送、限流、失败重试

SQLite

持久队列、去重、回执

Codex 01

Codex 02

Hermes 03

Agent 04

Agent 05

··· Agent 10

每个 Agent 都有独立身份和 Session，但无需独立持有 Telegram Token。

## 二、你的两个通信过程可以这样实现

### 发送：默认即时，异常时才限流

Agent 调用 `atlas send` 时，Gateway 做一次快速检查。

如果当前没有达到限制，就直接调用 Telegram `sendMessage`，成功后记录 Telegram 返回的消息 ID。

如果多条消息集中到达，则按限速规则排队。网络异常时保留消息，遇到 `429` 就遵守 Telegram 返回的 `retry_after`。长消息超过 Telegram 文本限制时才拆分内容，并保留分片顺序。

这里有个值得注意的细节：不要把所有消息都强制延迟 5 秒。 只要正常通信没有超过配额，就立即发送。限流器应该限制突发速率，而不是人为增加所有消息的延迟。

### 接收：统一监听，持久化后路由

Gateway 可以持续调用 `getUpdates`，采用 Long Polling。它不需要每个 Agent 分别查询，也不需要你有公网服务器。

Telegram 官方允许一次最多获取 100 条 Update，未确认的 Update 最多保留 24 小时。Long Polling 和 Webhook 不能同时使用。

接收处理流程是：

Plain textTelegram 返回新 Updates  ↓ 检查 update_id，过滤重复  ↓ 写入本地 SQLite Inbox  ↓ 确认 Telegram 更新游标  ↓ 解析消息字段及来源身份  ↓ 查找目标 Agent / Session  ↓ 投递到对应 Session 的队列  ↓ Agent 真正读取后记录 read receipt

注意这个顺序：先可靠地持久化消息，再确认 Telegram 更新。否则程序可能已经向 Telegram 确认收到，却在路由之前崩溃，导致消息丢失。

## 三、最重要的问题：怎样区分 10 个 Session？

这需要 Atlas 维护自己的寻址表，而不是让 Telegram 负责理解 Agent Session。

例如：

| Atlas 身份 | Session | Telegram |
| --- | --- | --- |
| agent-alice | session-001 | 共享 Bot |
| agent-bob | session-002 | 共享 Bot |
| agent-charlie | session-003 | 共享 Bot |

你现有的 Atlas CLI 已经使用了 `actor`、`session`、`channel`、`to_sessions` 等字段。这部分应该继续复用。

当远程成员发送一条消息时，Gateway 根据消息信封里的目标 Session 寻址；也可以根据 Telegram 的 `chat_id`、`message_id` 和 `reply_to_message` 查找原来的会话关联。

如果没有明确目标，则使用事先配置的默认 Session，或者要求发送者指定目标，不能随机投递。

唯一的实际区别是：外部看到的 10 个 Agent 都是同一个 Telegram Bot 身份。 如果你希望 Telegram 界面里每个 Agent 都有独立头像、名称和账号，才需要分别创建 Bot。

另外，即使这 10 个 Session 共享一个 Bot Token，本地也不应该默认让它们互相拥有执行权限。Telegram 身份通过可信来源映射到 Atlas actor 后，仍需执行你自己的授权检查。

## 四、这与现有 Atlas CLI 的关系

我之前检查的 [receive.go](https://github.com/Vioano/atlas-cli/blob/main/internal/atlas/receive.go?utm_source=chatgpt.com) 已经具有常驻接收器、多个 Binding、持久游标、发送者检查、消息投递日志等基础功能。

因此我建议在其上增添一个 `TelegramGateway`，而不是建立另一个完整通信框架。

它只需要处理 Telegram 特有的接收、发送、外部消息 ID 映射与限流，其他职责尽量复用 Atlas 的实现。

这样你就同时获得了三种能力：

- 本地多个 Session 共享一个 Telegram Bot。
- 远程的人类或其他 Bot 可以通过 Telegram 与 Agent 通信。其他 Bot 需要符合 Telegram 的 Bot-to-Bot 模式要求。
- 以后换用 Discord、Matrix 或自建传输服务，也不需要改 Agent 的寻址和任务协议。

## 一个进一步的建议

我认为你可以把 Telegram Gateway 设计成一个完全不依赖 LLM 的轻量常驻程序。

它只负责通信，Agent 只负责思考和决策。即使所有 Agent Session 退出或暂停，Gateway 仍继续收消息、持久化和等待恢复。

甚至可以做到：一个 Agent 结束当前 Session 后，未来启动新的 Session，仍然能够通过 Atlas 的持久 Inbox 读取此前发给它的消息。

但这需要明确区分长期的 Agent 身份和临时 Session 身份：Agent 身份负责长期收件箱，Session 是当前的执行入口。

这一点我认为比 Telegram 本身更重要。 它让通信生命周期与模型运行生命周期解耦，真正支持你的多 Agent 系统在任务间歇休眠、恢复和重新组织，而不是要求所有 Agent 始终保持在线。

---

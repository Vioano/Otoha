# 多人协作：队长电脑是权威端

Otoha Graph 0.4 提供一个小而完整的 Git 协作协议。无需自建公网服务：
队长和队员各运行本机预览；GitHub（或普通 Git 远端）只传递请求和已签名发布。
队长不在线时，请求等待；恢复后继续处理。它是最终一致的同步，不承诺硬实时。

## 权威边界

- 队长的私有状态目录必须在所有 Git 工作区之外。`model.json` 是本机设计源，
  `graph/` 保存校验后生效的版本、策略、批注与回执，私钥留在 `private.pem`。
  初始化复制现有模型，此后编辑的是这个私有目录的 `model.json`。
- Git 远端的 `otoha/<project>/snapshot.json` 是**可重建的发布副本**。
  队长只读取远端请求，绝不从远端发布副本更新本机模型或授权表。
  即使有人把直接修改快照的 PR 合并了，队长仍使用自己的有效版本重新发布。
  实现使用普通、可追溯的提交；不使用 force-push，不抹去远端历史。
- 队长以本机保存的成员公钥校验请求签名。请求中的 `actor`、文件名、Git 作者、
  PR 发起者和 Agent 自述都不能单独证明身份；远端也不能新增权限。
- 队员事先信任队长邀请中的公钥、项目和 authority epoch。只有匹配这个身份的
  签名快照才能进入本机已确认状态；伪造、篡改、已知旧版本、同版本不同内容均拒绝。
- Human viewer 与 Agent query 读取同一个已确认快照和 cursor。请求排队不会改变图。
  拓扑等效指**相同版本、相同查询范围**；短暂网络延迟允许不同电脑持有不同版本。

协议不会阻止 GitHub 上创建违规 PR，也不代替整个代码仓库的分支保护。
它保证这样的 PR 不能成为队长的图谱权威状态。`AGENTS.md` 是协作约定，
签名、权限、版本前提和本机写入边界才是实际执行机制。

## 建立一个团队

以下命令从 Otoha Graph 安装目录运行。示例使用 `parser` 节点；实际项目替换为
自己的稳定节点 ID。每个成员使用独立的私有目录和密钥，不共享队长目录。

队长：

```bash
OTOHA_LEADER_STATE="$HOME/.local/state/otoha-graph/demo-leader"
node bin/otoha-graph.mjs team init-leader \
  --state "$OTOHA_LEADER_STATE" --model examples/service.system.json \
  --repo-root "$PWD" --project demo --remote https://github.com/OWNER/PROJECT.git \
  > invitation.json
node bin/otoha-graph.mjs team serve --state "$OTOHA_LEADER_STATE" --interval 10
```

初始化生成公钥邀请；通过可信渠道交给队员，并核对公钥，不能盲信可被任意成员
替换的远端邀请。邀请不含私钥。默认同步分支为 `otoha-sync`，可在初始化时指定
`--branch`。沿用本机 Git 的认证；团队成员需要该同步分支的推送权限。

队员：

```bash
OTOHA_MEMBER_STATE="$HOME/.local/state/otoha-graph/demo-alice"
node bin/otoha-graph.mjs team init-member \
  --state "$OTOHA_MEMBER_STATE" --actor alice --invite invitation.json > alice.identity.json
```

把 `alice.identity.json` 中的公钥交给队长核对。队长创建如下 `grant.json`，
填入真实公钥；公钥不能使用下方说明文字代替：

```json
{
  "actor": "alice",
  "publicKey": "从 alice.identity.json 复制的完整 Ed25519 公钥 PEM",
  "agentId": "codex",
  "sessionId": "parser-design-session",
  "grants": [
    {"nodes": ["parser"], "fields": ["inputs", "outputs"], "comments": true}
  ]
}
```

```bash
node bin/otoha-graph.mjs team grant --state "$OTOHA_LEADER_STATE" --payload grant.json
node bin/otoha-graph.mjs team sync --state "$OTOHA_LEADER_STATE"
```

队员随后启动自己的预览：

```bash
node bin/otoha-graph.mjs team serve --state "$OTOHA_MEMBER_STATE" --interval 10
```

打开各自命令打印的本机 URL。网页“协作”入口与 CLI 使用同一后端：队员选择
节点和字段，保存请求；可立即同步，也可等待后台周期。队长自动校验并处理权限
范围内的请求，队员下次同步收到已生效或拒绝回执。**不要求队长逐次人工审批。**

`serve` 必须保持运行才会持续同步；没有偷偷安装常驻服务。失败时逐步退避到
最多 60 秒的重试间隔，另有 Git 命令超时。一次端到端变更可能经过队员上传、
队长处理、队员下载三个周期；网络与构建也影响延迟。

## 精细权限与请求格式

队员目前可提交下列限定操作：

| 动作 | 权限与行为 |
|---|---|
| `field.set` | 显式节点 ID + 白名单字段 + 读取时的字段版本；替换这一个字段 |
| `comment.add` | 显式节点的批注权限；追加一条有成员与会话来源的批注 |
| `task.set` | 显式任务 ID + `taskGrants` 白名单字段 + 读取时的字段版本；替换这一个任务字段。见[任务板协议](task-board.md)。 |

可授权字段为 `label`、`purpose`、`inputs`、`outputs`、`steps`、`openIssues`。
没有任意 JSON Pointer、执行脚本、节点删除、拓扑修改、权限修改、证据或成熟度修改
的成员操作。输入输出只是设计文本，不能借此修改可执行协议或声称验收通过。

- `fields: []` + `comments: true`：只可批注。
- `onlyIfEmpty: true`：只可填写真正为空字符串或空数组的字段，不能覆盖已有内容。
  “待定”这样的占位文字不是空；需要队长明确调整。
- 对某个 actor 重新 `team grant` 会替换其授权；`grants: []` 只撤销模块写入权限。
  任务字段另由 `taskGrants` 授权。要撤销全部写入权限，显式同时设
  `grants: []` 和 `taskGrants: []`，并检查生效后的策略。
  已排队请求按**执行当时**的策略校验。
- 队长仍可本机增删节点、连线、视图，修改字段和授权，但设计必须通过模型及渲染校验。
- `agentId` / `sessionId` 是分工和来源标签，不是在线状态、独占锁或额外身份凭证。
  请求附带的会话标签是签名成员自行声明的；它不扩大该成员的权限。

Agent 先读取已确认图和权限：

```bash
node bin/otoha-graph.mjs team manifest --state "$OTOHA_MEMBER_STATE"
node bin/otoha-graph.mjs team query --state "$OTOHA_MEMBER_STATE" --mode local --target parser --detail full
node bin/otoha-graph.mjs team state --state "$OTOHA_MEMBER_STATE"
```

从 `team state` 的 `fieldVersions["parser:inputs"]` 取得读取时的字段版本。
将以下请求中的 `17` 换成实际版本，保存在 `change.json`：

```json
{
  "requestId": "alice-parser-inputs-001",
  "context": {"agentId": "codex", "sessionId": "parser-design-session"},
  "changes": [
    {"operation": "field.set", "nodeId": "parser", "field": "inputs", "expectedVersion": 17,
     "value": ["PCM 音频与来源时间戳"]},
    {"operation": "comment.add", "nodeId": "parser", "text": "已补充输入定义，真实实现仍待验证。"}
  ]
}
```

```bash
node bin/otoha-graph.mjs team request --state "$OTOHA_MEMBER_STATE" --payload change.json
node bin/otoha-graph.mjs team sync --state "$OTOHA_MEMBER_STATE"
```

CLI 会补齐 `version/projectId/epoch/actor/baseCursor` 并用本机成员私钥签名。
省略 `requestId` 会生成 UUID；省略 `expectedVersion` 会使用本机当前已确认字段版本。
**Agent 对先前读过的内容做修改时应显式传入当时的版本**，不能在冲突后盲目换成新版。
网页草稿会保留当时的字段版本，热更新不会悄悄替换它。

每个请求包含 1–20 个操作、最多 64 KiB，整个批次全部成功或全部拒绝。两个成员
改不同字段不因全图更新而冲突；同字段变过、甚至改回原值也会触发版本冲突。
重试相同 ID 和内容返回原请求/回执；相同 ID 换内容会拒绝。冲突后重读并重新判断，
有意修改应使用新的请求 ID。收到拒绝不等于请求未送达。

## 存储与 Agent API

```text
队长私有目录/                  队员私有目录/
  config.json                    config.json
  private.pem                    private.pem
  identity.json                  identity.json
  model.json                     replica/<cursor>.json  # 已验签快照
  graph/                         outbox/<requestId>.json
  pending-source.json            transport.git/         # 私有裸 Git 缓存
  quarantine/                    writer.lock            # 单写者约束
  remote-observations/
  transport.git/
```

远端仅需如下数据；请求文件是精确操作的签名信封，不是整图副本：

```text
otoha/<project>/snapshot.json
otoha/<project>/requests/<actor>/<requestId>.json
```

传输使用隔离的裸 Git 对象读取与树写入，不 checkout、不执行远端文件或 hooks、
不把远端源码/授权表 merge 到本机权威目录。并发推送被 Git 拒绝后，重新取远端树，
仅把本次输出数据叠上去重试；不会拿这个树覆盖本机模型。可疑快照与非法请求留存
供队长检查。默认不自动删除历史请求；超过 10,000 个传输条目会要求显式归档。
这是小团队初版，不是高吞吐事件队列。

本机 Agent HTTP 继续使用 [Agent interface](agent-interface.md) 的
`/api/manifest`、`/api/query`、`/api/diff`、`/api/history`、`/api/events`、
`/api/bundle` 和 `/api/status`；额外提供：

| 接口 | 作用 |
|---|---|
| `GET /api/team` | 角色、成员权限、字段版本、批注、回执、同步错误；队员也有 outbox |
| `POST /api/team/request` | 队员保存签名请求；正文为上方 `change.json` |
| `POST /api/team/sync` | 同步一次；正文 `{}` |
| `POST /api/team/grant` | 本机队长授权；正文为 `grant.json` |

POST 需同源 Origin、JSON 和 `/api/session` 的会话 token；只监听 loopback。
队员不能通过旧的设计请求、Agent 回执或 rollback 路由绕过团队权限。
正在运行的预览拥有写锁；CLI 写命令转发给它。没有预览时，CLI 可独立操作。
成员缓存只含同步到过的 cursor；缺失版本明确返回 `authority/reset-required`，
Agent 应重读 manifest，不自行拼接不同版本的数据。

成员 CLI 的 `connection: local-accepted-snapshot` 表示本地已验签缓存，不能
据此认定 `serve` 在线或已同步最新远端版本；`lastSync: null` / `syncFailure: null`
也不是同步成功证明。需要新版本时使用已有服务同步，或显式 `team sync` 后再读。
队长的 `live` 仅证明本机权威端可达，不证明所有队员已经回读。

`team request` 成功是本地签名并排队，`sync.submitted` 是上传数量；两者都不是
队长接受。按 requestId 查 outbox 中的 receipt，只有 `receipt: null` 才待确认。
accepted 后回读目标；conflict 重读字段并以新 ID 提交重新决定的意图；forbidden
停止该写入，不绕过授权。结果未知保留原 ID/载荷，不忙等或反复制造新请求。
损坏、超限或身份不匹配的本地请求原文件保留，`outboxErrors` 逐项列明；有效
请求仍可上传。`sync.ok: false, partial: true` 表示存在需人工修复的本地请求，
即使 `submitted` 大于零也不能把整批当作完成。修复原文件后按原 ID 重试。
收尾先停止新请求，收回执或明确保留 pending，再撤权并同步、停止成员服务，
最后停止队长服务。保留私有目录与历史；只停止本次拥有的进程。

## 出错与恢复

- 非法请求：签名、身份、格式或路径错误进入隔离记录；合法签名但越权、冲突或模型
  不合法的请求留下明确拒绝回执，不产生部分修改。
- 网络中断或远端篡改：继续读本机上一有效版本，显示同步失败。成员 outbox 保留，
  可重试；队长恢复后以自己的已确认版本修复远端发布。
- 队长源文件非法：保留上一有效图，修正本机源文件或使用现有冲突检查 rollback。
  回退图不撤销外部代码副作用，也不会自动恢复旧授权；权限由队长另行明确修改。
- 提交后、镜像源文件写入前崩溃：`pending-source.json` 与持久提交的 transactionId
  在重启时完成镜像写入，不重复执行请求。出现额外本机编辑时停止恢复以保留它。
- 队员某份缓存损坏：保留损坏文件用于检查，尝试此前有效签名快照；下次成功同步可修复。
  队长持久历史损坏则遵循原有 fail-closed 机制，不能随意删掉记录继续写。
- 队员某份 outbox 请求损坏：保留原文件与错误诊断，不发送它、不遮蔽其他有效请求；
  同一 ID 的损坏文件不能被新请求覆盖。核实原意后显式修复，再等待回执。
- 队长电脑损坏：需要恢复**整个私有目录备份**，包括私钥、历史和源文件。
  GitHub 的发布副本不是自动权威恢复源。丢失密钥/完整历史时，应明确建立新 epoch，
  重新分发可信邀请与授权，不能冒充原权威悄悄续写。

信任假设是队长电脑、安装的执行代码及本机操作系统可靠。协议防的是远端提交和
成员请求越界，无法防拥有队长电脑或私钥访问权的人，也无法阻止有远端写权限的人
删除请求或阻断同步。新成员首次连接时，签名证明出处而非“绝对最新”；没有独立
可信时间/版本锚点时，不能证明服务器没有隐藏更新。之后本机拒绝已知版本回放。

源文件内容不会随证据路径自动同步。成员看到的是队长签名的证据评估与设计，
不能把它当作自己电脑上的运行验证。公开仓库会公开提交的模型、批注、成员标签、
公钥和证据路径；私钥与本机状态目录始终不入仓库。

## 基本验收

`npm run test:team` 用独立临时目录与真实本地 bare Git 仓库模拟队长和两位成员，
覆盖授权/拒绝、签名冒充、字段冲突与 ABA、原子批次、幂等、撤权、CLI/HTTP
旁路、并发推送、篡改发布后的修复、网络失败、缓存损坏及事务崩溃恢复。
这不是多台真实电脑、真实 GitHub 网络故障或长时间压力测试的替代。

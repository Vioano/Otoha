# 移植前的来源盘点记录

以下保留 2026-10-24 初次盘点时的历史状态；当前组件与迁入状态见 [组件说明](../FOUNDATIONS.zh-CN.md)。

# 已有基础与复用入口

盘点日期：2026-10-24。下列版本来自本机 Git checkout；三个 checkout 盘点时均无未提交修改。本记录用于固定设计参考，不代表已导入、已运行回归测试或已完成 Otoha 集成。

## System Atlas

- 仓库：[Vioano/system-atlas](https://github.com/Vioano/system-atlas)。GitHub API 已确认公开可访问。
- 参考提交：`dae140aeb04d2e5b77e23bb5ff20234f5062fc3d`。
- 当前源码包版本：`0.5.1`，Node.js 项目。
- 许可：MIT；包含 System Atlas、Archify、Cocoon AI 的署名。导入相关代码时需保留相应许可和来源。

| 能力 | 参考源码 | 复用时需要保留的语义 |
| --- | --- | --- |
| 接受版本、历史、差异与恢复 | `design/authority.mjs` | 候选修改经过校验才发布；损坏历史阻止发布；保留最近有效版本 |
| 人机共享的有界查询 | `design/query.mjs`、`design/server.mjs`、`design/cli.mjs` | 返回版本与范围；分页和过滤不能被误解为全量读取 |
| 交互式系统视图 | `design/viewer.html`、`design/deliver.mjs` | 人和 Agent 的投影来自同一接受模型；阅读状态不改写系统事实 |
| 独立任务与模块关联 | `design/tasks.mjs`、`references/task-board.md` | 任务可不关联模块；完成任务不会自动提升模块验证状态 |
| 小团队签名变更 | `team/`、`references/collaboration.md` | 身份、字段权限、版本冲突与请求结果分别表达 |

System Atlas 的看板目前是协作视图，不是调度器。图的接受版本也不证明图中的设计已实现。其 loopback 预览服务不能直接当作已认证的多人服务部署。

## Atlas CLI

- 仓库：[Vioano/atlas-cli](https://github.com/Vioano/atlas-cli)。GitHub API 已确认公开可访问。
- 参考提交：`4c1771d6d9157ceb7afcbf3ca5ad2d24f9320b1b`。
- Go 模块：`github.com/Vioano/atlas-cli`；当前 `go.mod` 要求 Go `1.26.0`。
- 许可：MIT，Copyright 2026 Vioano。

| 能力 | 参考源码 | 复用时需要保留的语义 |
| --- | --- | --- |
| 项目创建、成员接入 | `internal/atlas/admin.go`、`member.go`、`bundle.go` | 项目授权与身份凭据分开；邀请包含的凭据必须私下交付 |
| 传输、连接与 Git 接入 | `internal/atlas/client.go`、`git.go`、`gateway.go` | TLS 校验、SSH 主机固定、路由边界和凭据保管 |
| 持续收信与游标恢复 | `internal/atlas/receive.go` | 消息去重、持久游标、绑定配置与接收进度分离 |
| 会话投递 | `internal/atlas/codex.go`、`desktop.go` | 排队、投递、实际阅读是不同结果；未知结果先核对再重试 |
| 后台服务 | `internal/atlas/service.go` | 已有 receiver 的升级和恢复不能靠启动第二个 receiver 代替 |

当前 CLI 依赖已有 Personal Atlas 服务端接口和 Gitea 等外部服务，源码不包含这些服务端。Codex Desktop 适配依赖宿主版本；跨客户端、跨机器和真实空闲唤醒需要单独验证。Otoha 的后端与客户端选择尚未被这些历史依赖锁定。

## Team Mailbox

- 本机 checkout 的 origin：`https://github.com/NikolaStarx/team-mailbox.git`。
- 参考提交：`77581d464fa8d1b0d2182c31bee4fb0a8e03c83a`。
- 2026-10-24 使用当前 Vioano 账号读取该远端时，GitHub API 返回 HTTP 404。该结果不能单独区分仓库迁移、删除、权限或账号差异；目前以本机源码为盘点依据。
- 实现：Python 3.10+、Git、GitHub CLI 与可选客户端 Hook。
- 许可：MIT，Copyright 2026 NikolaStarx and contributors。

| 能力 | 参考源码 | 复用时需要保留的语义 |
| --- | --- | --- |
| 通用协作协议 | `skills/team-mailbox/references/protocol.md` | 身份、寻址、话题、回复、交付与事实来源分别约定 |
| Agent 行为指引 | `skills/team-mailbox/SKILL.md` | 收信不等于接手；安装 Skill 不增加用户授权 |
| 收信与完整历史导出 | `skills/team-mailbox/scripts/mailbox.py` | 分页完整性与失败可见；抓取成功不等于 Agent 已读 |
| 安装和项目集成 | `scripts/install.py` | 保留原有规则和修改；默认不替用户开启 Hook |

收信脚本只读，不发消息、不调用模型，也不唤醒空闲 Agent。GitHub Issues 是该实现的消息载体；可复用的协作语义不应因此与唯一传输方式绑定。

## 复用方式

先以固定提交阅读和验证，再为具体场景选择依赖、适配或提取代码。导入代码时记录源仓库、源提交、原路径和本地修改，保留适用的许可与署名，并运行相关上游测试和集成测试。

当前没有复制旧 checkout、迁移真实凭据、接管 receiver 或更改旧项目服务。旧项目的验证报告仍只证明其各自记录的版本与范围。

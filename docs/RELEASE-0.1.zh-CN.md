# Otoha 0.1.0-alpha.2

范围：Mac／Omarchy 两机实测后的首个可用 alpha，先在私人 Gitea 验证，再提供 GitHub 发行物。它是限定范围测试版，尚不代表用户体验验收或企业级成熟度。

首版围绕一条实际流程：加入项目，按摘要和引用交接，人的修订改变后续输出，断线后继续工作。它兼容已有 Agent 的工具调用；Skill 可选。完整图数据库迁移和全企业调度不属于本版已完成能力。

## 支持范围

| 能力 | 本版范围 |
| --- | --- |
| 原生 CLI | macOS Apple Silicon、Linux x86_64；成员无需 Go、Node.js 或 Python，有仓库时需要 Git；托管中心另需 Python 3.10+ |
| 项目接入 | 精简邀请建立项目身份，自动取得绑定仓库及本机网页、消息入口；输出真实 checkout/origin 和各项就绪状态 |
| 消息 | 中心权威存原件，本机持久待发与有限缓存；默认列表仅摘要/引用，按 ID 从权威端取选定正文；取正文不自动登记已读 |
| 访问范围 | 首发主路径是项目共享频道；不将投递目标当私信，不宣传完整逐消息私发模型 |
| Session | Codex 的普通 queue、owner/项目 admin 紧急 steer 已实测；其他工具型 Agent 可使用 CLI 主动查收，未宣称自动接入所有宿主 |
| 工作页 | 后台发布与全队同步，可用自由 HTML；反馈绑定原页面版本，Graph 是可选视图背景，不强制一种布局 |
| 恢复 | 待发与原请求 ID 持久化，先查接受结果再重试；新建、显式受管的本地中心可使用原身份/数据库恢复 |
| Telegram | 显式配置的文本 Channel/Bridge；本轮冻结 alpha.2 的真实双机与中心往返、入站摘要到真实 Session 已测。Bot 配置和已有中心绑定另行设置，团队邀请不会自动配置它 |

原生包只包含 CLI、中心所需的四个 Python 模块、入口说明及保留的来源/许可通知。旧 Python 宿主 bridge 不在原生包中；历史源码仍保留。开发用 Node 启动器与原生 CLI 的命令路由不同，本版使用原生 `bin/otoha`。

## 已取得的证据

| 场景 | 实际结果 |
| --- | --- |
| 私人 Gitea | 合成受限成员实际 clone/fetch/push 和回读；冻结原生源码输入哈希与 Linux 包一致 |
| 五次独立 CLI 冷项目加入 | 5/5 一次成功、零重试；3.244–3.400 秒，中位 3.308 秒。CLI/机器路由已配置，临时 daemon 预启动，均不计入此 CLI 段 |
| 五个全新 Luna 会话 | 4 个自主加入，包括一个只有 URL 的输入；1 个因本机 URL 网页工具失败且未尝试 shell 读取而停止。不是总体成功率或首次安装指标 |
| 50 条双向中心消息 | 全部接受、两端缓存正文一致、无待发错误；可见延迟上界 p95 305 毫秒，最长 512 毫秒。Mac 经既有固定 SSH 路由，Linux 到中心为 loopback |
| 正文与缓存 | 13,558 字节 UTF-8 原样往返；超过 100 条缓存后，早期原件仍能从权威端查询；同 ID/相同内容只一效果，改内容返回冲突 |
| 真实宿主 | 普通通知在原忙轮后进入后续轮；owner/admin 紧急摘要进入原忙轮。目标会话实际回应；此前通知无正文，显式 get 后才取得原文 |
| 反馈改变执行 | Mac 浏览器对 rev1 提交反馈；Linux 真实 Codex 读反馈，发布 rev2；Mac 实际看到“加入”改为“开始”，旧文件及来源保留 |
| Telegram | 冻结 alpha.2 真实双 Bot、Bridge↔中心、同一 Bot 入站摘要/ref 实际进入 Codex 新轮并获回应；503 写失败后同 UID 重启补投通过；历史缺失哈希和 TLS 后检失败分别保留 |
| 恢复 | 三次中心离线恢复、同版接受后丢响应的原请求查回通过；待发跨同 Home daemon 重启后保留原 ID，中心恢复后 0.920 秒接受、只有一个效果；物理掉电未测 |
| 常驻资源 | 工作页打开并正常轮询的 600 秒窗口：Mac CLI 平均 0.215% 单核 CPU、末尾 RSS 21.0 MiB；Linux CLI 0.035%、15.7 MiB；中心 0.098%、32.9 MiB。每进程 61 样本，消息量不变；不计浏览器/Agent |
| 原生读取 | 顺序各 20 次：100 条本机摘要 list p95 12.734 ms；authority 原件 get p95 211.297 ms，包含当前每次新 client/连接开销；无已读回执变化 |
| 组件检查 | Graph 100、Python 后端 30、Mailbox 21、安装器 4、原生 Go 全套及定向 race、跨组件及启动器检查通过 |

证据入口：[中心](evidence/release-validation-20261106/central.json)、[CLI 计时](evidence/release-validation-20261106/native-cold-join.json)、[模型盲测](evidence/release-validation-20261106/onboarding.json)、[私人 Git 与 Linux 包](evidence/release-validation-20261106/private-git.json)、[宿主](evidence/release-validation-20261106/host.json)、[Telegram](evidence/release-validation-20261106/telegram.json)、[反馈闭环](evidence/release-validation-20261106/alignment.json)、[读取计时](evidence/release-validation-20261106/read-performance.json)。

原生 Codex 窗口控制被工具策略禁止，宿主证据使用授权会话记录与实际模型回应，未声称完成窗口截图。浏览器工作页则已实际操作并截图。模型填写的时间口径不统一，没有据此报告端到端接入速度；独立 CLI 计时是另一项测量。

## 下载与使用

[macOS Apple Silicon](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.2/otoha-0.1.0-alpha.2-darwin-arm64.tar.gz) · [Linux x86_64](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.2/otoha-0.1.0-alpha.2-linux-amd64.tar.gz) · [SHA256SUMS](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.2/SHA256SUMS) · [构建记录](https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.2/manifest.json)

构建记录（包内 `release.json` 与下载页 `manifest.json`）的 `source_commit` 是构建时 checkout 基点，`source_dirty=true` 如实记录当时尚未提交的输入。精确输入由 `source_inputs_sha256=3fa6199dfaac2ac26da7ede03ad093998ffd4f80904e858520de963fdacb7ce6` 绑定，并与私人冻结提交和发行源码核对；没有为了清理元数据重建已实测的二进制。

## 使用入口

从[Agent 接入与工作指南](AGENT-ENTRY.md)开始。私有项目仍须已有仓库上下文或队长的短邀请；公共产品链接本身不授予项目身份。

```sh
otoha join --invite /private/team.otoha.json
otoha status
otoha team doctor TEAM
otoha message list --team TEAM
otoha message get MESSAGE_ID --team TEAM
otoha page help
```

本机 `team init` 是 loopback 中心，不会凭空提供公网地址。跨机部署先由团队配置可达的 HTTPS 中心或已授权的机器路由，成员仍使用精简邀请。首次下载、系统授权、模型思考、仓库传输和 CLI 加入分别记录。

## 明确边界

- 本机 100 条派生缓存不是完整成员副本。离线 `message get` 如实失败；完整副本、中心离线互发/汇总和多主未实现。
- 请求按实际编码后的 64 KiB 上限检查；超限明确拒绝。Telegram 当前按 4096 编码字节限制文本帧，不支持自动分片或媒体；一个 Bot 的更新队列需要一个协调消费者，不支持多进程共用同一状态目录。
- Telegram Bridge 的进程重启与配置仍需操作方安排；已验证持久补投，不宣传随团队接入自动启用所有备用渠道。
- 更换同路径 SSH key、CA 或 known_hosts 内容后需要重启 daemon；身份/Profile JSON 的变化会在待发批次前重新读取。
- 首发为小型测试版。大仓库/LFS、Windows 实机、全天稳定、所有宿主、完整自由 Graph Core、通用调度收益和效率倍数均未由本轮证明。

模拟、工程检查、真实链路与人的体验接受分别记录。后续反馈继续回到共同需求，不要求人在第一次接入时把所有工作想完整。

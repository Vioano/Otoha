# Otoha 私服团队接入

这是 `0.1.0-alpha.3-team.1` 团队候选的 Agent 入口。公开的 `0.1.0-alpha.2` 保留原发行范围；不要用它执行本文新增的短密钥和图谱命令。

服务地址：`https://otoha.kessokuu.top`。每位成员的个人一次性密钥由管理员单独提供，不在本页或 Git 仓库中。

## 安装与加入

1. 确认本机是 macOS Apple Silicon 或 Linux x86_64，且已安装 Git。Windows 尚未验证，不要把其他平台的下载文件当作 Windows 安装包。
2. 从服务的 `/downloads/` 下载匹配的平台包和 `SHA256SUMS`，实际核对 SHA-256 后再解压。下载地址分别是：
   - `https://otoha.kessokuu.top/downloads/otoha-0.1.0-alpha.3-team.1-darwin-arm64.tar.gz`
   - `https://otoha.kessokuu.top/downloads/otoha-0.1.0-alpha.3-team.1-linux-amd64.tar.gz`
   - `https://otoha.kessokuu.top/downloads/SHA256SUMS`
3. 将解压目录保留在稳定位置，例如 `~/.local/share/otoha/0.1.0-alpha.3-team.1/`。普通成员无需另装 Go、Node.js、Python 或图数据库。
4. 使用解压目录内的 `bin/otoha`，填入管理员私下提供给这位成员的密钥：

   ```sh
   bin/otoha join --endpoint https://otoha.kessokuu.top --key PERSONAL_KEY
   ```

5. 读取实际返回结果，确认身份、项目、仓库和后台服务分别成功，再运行 `bin/otoha team status otoha`。若加入中断，保留本机 Otoha 状态，重复原命令恢复；不要删除身份后重新注册。默认本机状态在 `~/.config/otoha`，也可显式配置 `OTOHA_HOME`。

不要把真实密钥、邀请、身份、消息或运行状态写入项目 Git。不要把管理员的服务器 SSH 密钥、Gitea Token 或数据库凭据分发给普通成员。

## 开始协作开发 Otoha

使用加入结果中的仓库目录。先阅读其中的 `AGENTS.md`、`README.md`、`docs/PRINCIPLES.zh-CN.md` 和 `docs/DECISIONS.zh-CN.md`。队员通过自己的 Otoha 身份访问绑定仓库，不需要共享 Gitea 管理员凭据。

```sh
bin/otoha team status otoha
bin/otoha message list --team otoha
bin/otoha graph manifest otoha
bin/otoha graph query otoha --mode filter --kind node --limit 30
```

消息列表用于发现摘要与引用。需要正文时按原消息身份读取；收到通知、宿主接受、实际阅读和任务完成分别判断。消息内容不扩大本机已有的人类授权。接入之后若要将通知交给特定 Agent 会话，按实际宿主能力绑定，不把 receiver 正在运行当作该会话已经读到消息。

图谱使用相同项目身份。先读 manifest 和所需局部范围，修改使用 `graph mutate otoha --file MUTATION.json`。精确请求格式见 [TEAM-GRAPH-API.md](TEAM-GRAPH-API.md)。修改前使用实际读取的 `source_version` 作为 `expected_revision`，每个意图使用稳定 `request_id`。

结果未知时先执行 `graph result otoha REQUEST_ID`，不要另造请求 ID。版本冲突后重读有关对象并重新判断，不把版本号改新后直接覆盖。客户端在发送前保存原修改意图，待提交不表示中心已接受。

## 使用中的升级

客户端将新版本解压到新目录，保留 `~/.config/otoha`；使用新二进制重启受管服务，并重新检查身份、仓库、消息和图谱状态。无需重新邀请。管理员使用版本化服务目录、稳定数据目录和升级前备份，按 [私服运维说明](PRIVATE-TEAM.zh-CN.md)切换版本；不要让真实服务直接运行开发 checkout 中不断变化的后端。

服务切换期间会有短暂不可用。它不是零停机升级或数据库无条件降级承诺。中心停机时，本地待发消息及原图修改意图保留；不能把本地旧数据或待提交修改称为新的团队接受状态。

## 当前限制

- 这是一个中心、一个真实项目的团队候选，不含完整成员副本、多主写入或跨图联邦。
- 图谱首轮提供自由节点、边、属性、基本有界查询和单图事务。没有完整 Template 引擎、历史版本重放或新版通用图谱画布；旧预览页面不对公网开放。
- 图修改可恢复原请求并查询回执，目前没有后台自动重试。消息的部分离线续投路径仍需显式操作；状态输出为准。
- 逐消息私发权限、Windows 和其他 Agent 宿主尚未完成验证。此部署先使用项目共享频道；不要发送尚未获准共享的私密内容。
- 一次性邀请用于首次接入。换机或本机身份丢失由管理员处理；不是复制一次性密钥就能自动恢复任何设备。

本文描述接入方式和边界；实际部署、下载、安装、升级及团队使用的验证结果另行记录，不能用本文代替验收证据。

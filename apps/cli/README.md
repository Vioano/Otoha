# Otoha CLI

原生客户端：自动初始化、项目接入、身份导入、消息、持续接收、Git 代理与 Codex 会话适配。使用从[Agent 接入入口](../../docs/AGENT-ENTRY.md)下载的 `0.1.0-alpha.3-rc.1`，无需构建源码。新团队运行 `bin/otoha init`；已有团队使用个人邀请 `bin/otoha join --invite FILE`。当前需要系统 Git，Linux 后台接收需要可用的 user systemd 会话。

直接运行 `otoha` 会进入人类可读的本地协作控制台；显式子命令仍保持 JSON 输出，便于 Agent 调用。`otoha demo --slow` 播放不需要凭据的多 Agent 录屏序列。

以下是开发者从源码构建的方法，需要 Go 1.26+。从仓库根目录：

```sh
npm run build:cli
node bin/otoha.mjs client version
node bin/otoha.mjs client help
```

产物位于 `dist/otoha`，Windows 为 `dist/otoha.exe`。也可在本目录运行 `go build ./cmd/otoha`。当前[候选范围](../../docs/RELEASE-0.1.0-alpha.3.zh-CN.md)与历史 [alpha.2 发行说明](../../docs/RELEASE-0.1.zh-CN.md)分别保留。

## 本机中心的运行时发行包

`0.1.0-alpha.3-rc.1` 候选采用小客户端包与按需下载的 `center-runtime` 平台包。后者包含固定版本的 Python 3.12.12、SurrealDB 3.3.5、frpc 0.72.0、公共 CA 和同版中心后端；普通使用者无需分别安装或配置这些依赖。系统 Python 不满足条件时，包内独立 Python 提供运行环境。组件由软件发现，实际启动不依赖开发 checkout 或当前工作目录。

目录约定为 `runtimes/frpc`、`runtimes/surreal`、`runtimes/python/bin/python3`、`runtimes/ca-bundle.pem` 和 `services/collaboration/`。公开 `runtime-manifest.json` 记录平台包的固定 URL、完整字节数、SHA-256、解包根与路径；客户端包内带同一清单。运行时包保留 Python 原有符号链接，以及 frp、SurrealDB、Python/依赖和 CA 的来源、许可与 NOTICE。SurrealDB 当前适用上游 BSL 条款，不因被本项目打包而改成 MIT。

维护者构建：

```sh
node scripts/package-cli.mjs --version 0.1.0-alpha.3-rc.1 \
  --with-center-runtime --relay-dial-ip PUBLIC_RELAY_IP \
  --out /private/fresh-release-output
```

脚本从 [`runtime-assets.json`](../../scripts/runtime-assets.json) 的固定官方来源下载并校验组件，缓存仅供构建复用；输出目录已有 manifest 或同名包时拒绝覆盖。`--runtime-cache DIR` 指定构建缓存，`--runtime-only` 仅用于先制作运行时包。上例地址参数由部署者确认，不能填写猜测的中继 IP。

初次打包测量约为 Mac 109 MiB、Linux 138 MiB，最终字节数以冻结清单为准。这只是压缩体积；五分钟内完成首次下载和初始化，必须从公开入口、空缓存实际测量。构建机缓存、系统已有 Python 或上游组件下载速度不能代替该验收。

`OTOHA_HOME` 指定独立私有客户端目录，默认是用户目录下 `.config/otoha`。`otoha1_` 邀请前缀、`otoha-api`/`otoha-git` 路由、环境变量与后台服务使用 Otoha 名称。旧邀请、旧后台服务和旧状态没有自动接管；生产迁移需明确匹配服务端与客户端。

协作服务在 [services/collaboration](../../services/collaboration/README.md)。新的 `init` 默认使用本机私有 Git 与内置中继，不要求用户提供 Gitea 或服务器账号。旧 server/Gitea 模式仍需相应资源和管理员授权，源码存在不代表外部资源已经部署。

本目录运行 `go test ./...` 和 `go vet ./...`。忙时投递、空闲唤醒和 Windows 原生宿主行为仍需对应机器的实际验收。

## Telegram Gateway（最小版本）

Telegram 是可替换的渠道。最小 Gateway 使用 Bot API 长轮询，不删除已有 webhook；token 只从本机私有文件读取，状态写入 `state_dir/state.json`。

配置文件示例：

```json
{
  "token_file": "/private/telegram/bocchi.token",
  "state_dir": "/private/otoha/telegram",
  "routes": {"8250132650": "nijika-session"},
  "recipients": {"nijika-session": "@Nijika_F0_bot"}
}
```

常用命令：

```sh
otoha telegram status --config /private/telegram.json
otoha telegram send --config /private/telegram.json --to nijika-session --text TEXT --id UID
otoha telegram receive --config /private/telegram.json
```

接收顺序是“写入本机状态和去重记录，再推进 Telegram offset”。当前路由只把 Telegram chat 映射到本地 Session 名称；真正投递到 Codex 或其他 Agent 宿主仍由后续模块完成。

通信闭环的本机命令：

```sh
otoha inbox check --channel PROJECT-general --session SESSION
otoha inbox list --channel PROJECT-general
otoha inbox read MESSAGE_UID --channel PROJECT-general --session SESSION
otoha telegram bridge --config PRIVATE/telegram.json
```

`inbox check` 先把可见事件写入 `OTOHA_HOME/inbox`，再提交逐条“已收到”回执；
`inbox read` 读取本机持久副本后提交“已读”回执。游标、消息和回执状态都在私有目录中，
进程重启后可以继续处理。后台 `otoha receive` 对已绑定的 Agent 使用相同的持久化步骤，
然后才尝试投递到宿主会话。

`telegram bridge` 同时运行两个方向：中心服务事件经过 Telegram Gateway 发出，Telegram 信封先写回中心服务，再由同一条 inbox 游标和回执路径落盘。真实 Bot、跨机器和外部发送者身份映射仍需单独验收。

## Team bootstrap（本机队长模式）

`team` 命令把本机协作服务、项目频道、一次性邀请和成员身份接入编排成一条可审计流程：

```sh
otoha team init demo --min-free-bytes 0
otoha team status demo
otoha team doctor demo
otoha team invite demo alice --out /private/alice-invite.json
otoha team join --invite /private/alice-invite.json
```

`team init` 默认在 `OTOHA_HOME/teams/NAME/` 下创建 SQLite 服务状态、队长身份和服务日志，并在本机回环地址启动协作服务。`team invite` 写出权限受限的接入包；接入包包含服务地址、项目/频道、一次性邀请和过期时间，不包含任何私钥、队长凭据或 Telegram Token。`team join` 在成员本机生成凭据并保存本机身份，重复执行会恢复已有身份。

服务器模式也支持通过固定 HostKey 的 SSH 做远端初始化/启动，再用 HTTPS/TLS 访问：

```sh
otoha team init demo --mode server \
  --url https://team.example.com \
  --ssh-target captain@team.example.com \
  --ssh-key /private/ssh/id_ed25519 \
  --known-hosts /private/ssh/known_hosts \
  --remote-script /opt/otoha/otoha_server.py \
  --remote-state /srv/otoha/demo \
  --remote-cert /srv/otoha/tls/fullchain.pem \
  --remote-tls-key /srv/otoha/tls/privkey.pem \
  --ca-file /private/ssh/team-ca.pem
```

SSH 只用于远端初始化和启动，成员通过 HTTPS/TLS 接入；邀请包可以携带公开 CA 证书，但不会携带 SSH 私钥。远端 systemd/launchd 服务注册和真实服务器验收仍需单独完成。

## Doctor and daemon

`otoha doctor` 是统一的只读检查入口：它检查本机私有目录、Go CLI、Python/Node 运行时、协作服务源文件、本机团队服务、SQLite 完整性、受管 receiver 和旧版 profile。它只报告问题，不自动修复或接管旧服务。

后台接收沿用已有的一个 receiver。使用 `otoha daemon install|status|start|stop|restart|uninstall` 管理当前平台的 launchd、systemd user service 或 Windows Task Scheduler；`otoha daemon logs [--follow]` 读取同一个 receiver 日志。没有旧版 profile 或绑定时，daemon 不会凭空创建第二个 receiver。

[MIT 许可](LICENSE)，代码来源见根目录 [NOTICE.md](../../NOTICE.md)。

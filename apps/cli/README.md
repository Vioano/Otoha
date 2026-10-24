# Otoha CLI

原生 Go 客户端：项目接入、身份导入、消息、持续接收、Git 代理与 Codex 会话适配。需要 Go 1.26+。

从仓库根目录：

```sh
npm run build:cli
node bin/otoha.mjs client version
node bin/otoha.mjs client help
```

产物位于 `dist/otoha`，Windows 为 `dist/otoha.exe`。也可在本目录运行 `go build ./cmd/otoha`。正式二进制发行版尚未发布，邀请安装地址已切换到本仓库，不复用历史产品发布包。

`OTOHA_HOME` 指定独立私有客户端目录，默认是用户目录下 `.config/otoha`。`otoha1_` 邀请前缀、`otoha-api`/`otoha-git` 路由、环境变量与后台服务使用 Otoha 名称。旧邀请、旧后台服务和旧状态没有自动接管；生产迁移需明确匹配服务端与客户端。

协作服务在 [services/collaboration](../../services/collaboration/README.md)。项目创建还需要可访问的 Gitea、配置好的接入通道与相应管理员授权；源码存在不代表这些真实资源已经部署。

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

当前 `--mode server`、远程 SSH provisioning、TLS 证书安装和宿主服务管理尚未实现；本阶段只保证队长电脑本机模式和 FakeChannel 联调。

[MIT 许可](LICENSE)，代码来源见根目录 [NOTICE.md](../../NOTICE.md)。

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

[MIT 许可](LICENSE)，代码来源见根目录 [NOTICE.md](../../NOTICE.md)。

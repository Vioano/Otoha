# Otoha Collaboration

Python 标准库与 SQLite 实现的协作服务：身份、项目与频道、持久消息、事件、回执和获准状态上的只读查询。包含 Python 客户端、stdio MCP、成员接入及会话桥接。

需要 Python 3.10+。从本仓库根目录运行，PRIVATE 替换为仓库外的私有目录：

```sh
node bin/otoha.mjs server init --state /PRIVATE/otoha-state --owner owner --url http://127.0.0.1:8788 --credentials /PRIVATE/otoha-owner.json
node bin/otoha.mjs server serve --state /PRIVATE/otoha-state --host 127.0.0.1 --port 8788
node bin/otoha.mjs message --profile /PRIVATE/otoha-owner.json manifest
node bin/otoha.mjs message --profile /PRIVATE/otoha-owner.json protocol
```

默认监听 loopback；远程使用需配置 TLS 或经过鉴权的 SSH 通道。`--mount` 可绑定实际挂载卷；初始化和访问有存储空间检查。测试夹具使用临时目录，不代表生产部署。

MCP 使用 `node /ABSOLUTE/Otoha/bin/otoha.mjs mcp --profile /PRIVATE/identity.json --session SESSION`。原生客户端见 [Otoha CLI](../../apps/cli/README.md)。实际读取之后才提交 read 回执；工具投递和模型执行分别验证。

协议见[协作协议](docs/协作协议.md)，API 见[接口文档](docs/API.md)，权限与查询见[读取策略](docs/通信权限与读取策略.md)。普通私有配置默认位于独立的 Otoha 目录；真实旧身份和服务不会自动迁移。

后台接收、桌面 IPC、SSH 接入和 Gitea 集成均依赖具体宿主或基础设施。本轮没有执行部署脚本，也没有为真实团队发送消息或安装 receiver。`deploy/` 是通用接入工具源码，运行前需为目标环境配置并验证。

本目录执行 `python3 -m unittest discover -s tests -v`。根目录 `npm test` 另包含原生 CLI 与此服务的真实本地互通测试。

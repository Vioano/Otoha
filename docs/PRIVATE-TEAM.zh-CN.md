# 独立私服：中心、图数据库与升级

这套部署用于一个独立的 Linux Otoha 中心。系统管理员通过 systemd 管理中心与 SurrealDB；团队成员仍使用 Otoha CLI。它不修改已有 Otoha、Gitea、receiver 或它们的数据，也不替代成员接入与真实宿主验收。

入口为 [`scripts/private-team/manage.py`](../scripts/private-team/manage.py)，只依赖 Python 3.10+ 标准库。脚本需要 root、systemd、`useradd`、与服务器架构一致的 Otoha 原生候选包，以及已核对来源和校验值的 SurrealDB **3.3.5** 二进制。脚本不下载二进制、不安装公网代理、不改防火墙，也不自动邀请成员。

以下地址、项目和版本名均为虚构示例。真实入口、owner 凭据、邀请和成员记录放在私有配置中，不进入本仓库。

## 目录与服务

```text
/opt/otoha-private/
  releases/0.1.0-alpha.3-team.1/     # root 所有，只读代码与固定后端二进制
    bin/otoha
    bin/surreal
    services/collaboration/*.py
    libexec/manage.py
    private-team-release.json       # 本次复制文件的 SHA-256
  current -> releases/0.1.0-alpha.3-team.1

/var/lib/otoha-private/             # 稳定数据，不在版本目录中
  config/service.json              # 项目、端口与对外地址
  config/owner.json                # root 可读的 owner bearer 凭据
  state/server.json
  state/messages.sqlite3           # 消息、身份、项目与授权
  state/graph.json                 # 私有 DB 凭据与项目绑定
  graph-data/                      # SurrealDB 持久数据
  backups/TIMESTAMP/               # root 私有备份，包含凭据
```

systemd 单元为 `otoha-private-surrealdb.service` 和 `otoha-private-center.service`，使用独立的 `otoha-private` 系统账号。中心默认只监听 `127.0.0.1:28788`；SurrealDB 默认只监听 `127.0.0.1:28000`。只有中心应经过另行配置的 HTTPS 入口对外服务；数据库与 root 数据库账号不向队员开放。

需要采用其他路径或单元名时，在每条管理命令的子命令**之前**一致指定 `--root`、`--data-root`、`--unit-prefix`、`--user`。路径使用没有空格的独立绝对目录，版本目录与数据目录不能相互嵌套。默认使用 `otoha-private` 名称是为了与历史部署隔离；不要把参数指向已有服务或真实旧状态。

## 首次安装

先把已验证的 Linux 原生候选包解压到暂存目录，顶层须包含 `bin/otoha`、`services/collaboration`。后端必须包含新增的 `otoha_graph.py`；旧 alpha.2 包没有该文件，不能直接用作此图后端候选。此处的版本名不代表一个已经公开发布的发行版。

```sh
sudo python3 scripts/private-team/manage.py install \
  --source /srv/staging/otoha-0.1.0-alpha.3-team.1-linux-amd64 \
  --version 0.1.0-alpha.3-team.1 \
  --surreal-bin /srv/staging/surreal-3.3.5

sudo python3 scripts/private-team/manage.py init \
  --version 0.1.0-alpha.3-team.1 \
  --public-url https://collab.example.invalid \
  --owner owner --project sample-project --channel sample-general \
  --database otoha_sample --graph-id sample-project
```

`install` 只生成不可变的版本目录并记录文件校验值，不切换或启动服务；相同版本名再次安装会拒绝，不能悄悄替换旧字节。`init` 仅用于首次初始化：数据目录或 `current` 已存在就拒绝，不重建 owner、不覆盖数据库、不轮换旧凭据。

`init` 创建私有数据库口令，调用中心的初始化入口，建立 systemd 单元并开机启用；启动 SurrealDB 和中心后，通过 owner 认证创建指定项目/频道，再检查该项目的图绑定。中心初始化本身保留默认 `team/general`；指定另一个项目时，不自动删除这个默认项目。

owner 是中心管理员；普通团队成员使用项目 `member` 角色，不授予 owner 或 root 数据库账号。脚本没有真实成员名单，不生成邀请。由管理员在已授权的独立任务中建立成员邀请，并分别验证其项目权限。GitHub 仓库权限不自动成为 Otoha 身份授权。

初始化中途失败会保留数据和诊断现场。单元已经生成时，先读日志修复，再用 `start` 启动：它沿用原凭据，补齐配置中缺少的项目/频道后检查图绑定；不会重建身份。不要删除数据后重跑 `init`。若失败早于单元生成，先检查私有文件完成到哪一步，再由管理员处理，脚本不猜测部分初始化的结果。服务运行与公网入口可用是不同的检查。

## 私有图配置

中心在认证和项目授权后读取 `state/graph.json`，将项目绑定到数据库与图身份。下面仅展示结构；安装时生成的口令不可复制进文档或提交 Git。

```json
{
  "url": "http://127.0.0.1:28000",
  "username": "otoha",
  "password": "REPLACE_IN_PRIVATE_CONFIGURATION_ONLY",
  "namespace": "otoha",
  "projects": {
    "sample-project": {
      "database": "otoha_sample",
      "graph_id": "sample-project"
    }
  }
}
```

图数据库只负责持久存储；项目身份、授权和对外图操作由中心检查。首次获准的图请求会初始化必要存储结构；它不是导入旧 Graph JSON 或迁移真实工程图。成员不需要知道数据库用户名或口令。

## 检查与日常运维

安装后可以使用版本目录自带的管理脚本，避免依赖原仓库 checkout：

```sh
sudo python3 /opt/otoha-private/current/libexec/manage.py health
sudo systemctl status otoha-private-center otoha-private-surrealdb
sudo journalctl -u otoha-private-center -u otoha-private-surrealdb --since '-15 min'
sudo python3 /opt/otoha-private/current/libexec/manage.py start
```

`health` 检查本机两个 `/health`，再使用私有 owner 凭据检查中心身份和 `/v1/graph/manifest?project=...`，核对 `ready` 与配置的 `graph_id`。它证明本机中心、认证和项目图绑定可用；不证明公网 TLS、成员权限、仓库访问、工作页同步或 Session 实际接收已验收。

公网入口由部署者另行配置并验证。先确认它只转发这个独立中心的 loopback 端口，再以一个受限测试身份验证加入、消息存取、图查询/修改、工作页和重连。不要复用旧 receiver 的身份或把旧服务指到新数据库。这里没有实现自动更新或公网入口管理。

## 备份与升级

```sh
sudo python3 /opt/otoha-private/current/libexec/manage.py backup

sudo python3 /opt/otoha-private/current/libexec/manage.py install \
  --source /srv/staging/otoha-0.1.0-alpha.3-team.2-linux-amd64 \
  --version 0.1.0-alpha.3-team.2 \
  --surreal-bin /srv/staging/surreal-3.3.5

sudo python3 /opt/otoha-private/current/libexec/manage.py switch \
  --version 0.1.0-alpha.3-team.2
```

单独运行 `backup` 会短暂暂停新中心与数据库：先停中心，后干净停止 SurrealDB，再复制 `state/`、`config/`、`graph-data/`，校验复制后的 SQLite 完整性。完成后恢复原来运行中的单元。备份含完整消息、身份和凭据，目录为 root 私有；不上传公开发行物，不自动清理旧备份。

备份范围限于上述三个目录。外部 Git/Gitea 远端、成员 checkout 与目录外的仓库内容需要它们自己的备份，不因本中心备份就视为已保护。

`switch` 自带同样的备份，因此不要求先手动运行一次 `backup`。备份成功后，两项服务保持停止，脚本原子切换 `current`，依次启动数据库与中心并执行健康检查。稳定数据路径不变，旧代码目录和切换前备份保留。

健康失败时停止新中心，保留数据、选中的新版本和日志，返回失败。**不自动回滚数据库，不用旧备份覆盖切换后可能产生的新消息。** 如需切回旧代码，先核对数据库/协议兼容性，再显式 `switch --version OLD`；它也会备份当前数据。数据库引擎版本不同会拒绝切换；SurrealDB 升级和数据格式迁移需要单独方案，不夹在普通应用更新中。

备份目录有 `INCOMPLETE` 时不是可用备份。`backup.json` 记录复制方式、SQLite 检查和源版本，但不会声称已经做过恢复演练。恢复必须在独立目录与端口中演练，确认消息、权限与图状态后再决定正式切换；本脚本不提供覆盖真实状态的 `restore` 命令。

## 本轮验证范围

本轮检查脚本语法、命令参数和候选包打包列表，并核对配置与后端接口。没有由这些本地检查证明远程 systemd 启动、备份恢复、公网接入或用户体验已经通过。实际部署与每条真实链路的结果需要另行记录。

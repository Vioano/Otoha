# SG relay 网络部署

`deploy_sg.py` 供中继运维者在 SG 主机以 root 执行。普通团队成员通过 Otoha CLI 接入，不直接运行此脚本。

脚本只安装 frps，并向已有 `/etc/nginx/sites-available/otoha-private` 的 HTTPS server 增加一个 include。它不创建域名或证书，不管理 bootstrap 服务、路由数据库、中心、SSH 隧道或 Gitea。

## 固定输入

- Python 3.10+，只用标准库；应用阶段要求 Linux amd64、systemd、NGINX、`useradd`/`userdel`。
- [frp v0.72.0 官方 archive](https://github.com/fatedier/frp/releases/download/v0.72.0/frp_0.72.0_linux_amd64.tar.gz)。脚本不联网下载。
- 文件：`frp_0.72.0_linux_amd64.tar.gz`。
- SHA-256：`daf719b32ca2778770bb92955e231a07414d754378b9af328aa2642862479804`。
- 已存在且已启用的 Otoha HTTPS vhost，只能有一个监听 443 的 `server`。现有证书必须覆盖该中继域名。

只从已校验 archive 读取 `frps` 与上游 `LICENSE`，保存到 `/opt/otoha-relay/frp-0.72.0/`。不解压 archive 中的其他条目。升级版本时一并审阅来源、hash、许可和接口兼容性，不自动追随 latest。

## 执行

先在 SG 检查计划；dry-run 只读 archive 和 vhost，不执行系统命令或写入配置：

```sh
python3 scripts/relay/deploy_sg.py \
  --frp-archive /var/cache/otoha-relay/frp_0.72.0_linux_amd64.tar.gz
```

确认计划后由 root 应用：

```sh
sudo python3 scripts/relay/deploy_sg.py \
  --frp-archive /var/cache/otoha-relay/frp_0.72.0_linux_amd64.tar.gz \
  --apply
```

本地 dry-run 可用 `--nginx-site /tmp/otoha-vhost` 检查副本；真实应用固定修改上述 Otoha vhost。脚本依据语法块定位 443 server，不靠第一个 `}` 或字符串替换；遇到零个或多个 443 server 会停止。重复执行不会重复插入 include。

## 写入范围与服务

| 资源 | 内容 |
| --- | --- |
| 系统用户/组 `otoha-relay-frps` | 无交互 shell、无新 home；frps 以此身份运行 |
| `/opt/otoha-relay/frp-0.72.0/frps`、`LICENSE` | 固定版本二进制及原始上游许可 |
| `/etc/otoha-relay/frps.toml` | frps 与认证插件的固定配置 |
| `/etc/systemd/system/otoha-relay-frps.service` | 新独立 frps 服务 |
| `/etc/nginx/otoha-relay-locations.conf` | 新路由 snippet |
| `/etc/nginx/sites-available/otoha-private` | 仅在 HTTPS server 增加 snippet include，保留其他文本 |

frps 只监听环回：控制连接 `28880`、HTTP 业务 `28881`。它关闭强制内层 TLS，外部 WSS 的 TLS 在现有 NGINX 443 终止；没有 dashboard。`auth.method=token`、空 `auth.token` 配合 HTTP 插件使用。真正的 Login/NewProxy 授权由独立的 `127.0.0.1:28882/frp` 处理，配置参考[固定版本 frps 示例](https://github.com/fatedier/frp/blob/v0.72.0/conf/frps_full_example.toml)。不要删除插件后把空 token 当成鉴权方案。

插件服务由另一部署步骤提供。脚本不会改它的代码、状态或 unit，也不会用假响应顶替它。frps 启动成功不证明路由注册、授权、到中心的传输已经可用。

独立 API 的代码与初始化合同见 [services/relay](../../services/relay/README.md)，当前 systemd 模板为 [otoha-relay-api.service](otoha-relay-api.service)。该模板使用专用账号 `otoha-relay`，只允许写 `/var/lib/otoha-relay`；部署者先创建账号、按合同初始化 `0700` 状态目录，并将只读程序放到模板中的固定路径，再安装启用这个新单元。它与 `otoha-relay-frps` 账号分开，不向公网转发 `/frp`。

## HTTPS 路由合同

| 入口 | 上游 | 行为 |
| --- | --- | --- |
| 精确 `/~!frp` | `127.0.0.1:28880` | WebSocket upgrade，保留 URI，读写超时 3600 秒 |
| `/t/` | `127.0.0.1:28881` | 保留 Host 与完整路径前缀，不缓冲请求/响应，读取超时 75 秒 |
| `/relay/v1/` | `127.0.0.1:28882` | 保留接口前缀与 Host |

所有 `proxy_pass` 都没有尾部 URI，不剥除上述前缀。路由 location 必须是 `/t/r` 加 24 位十六进制 ID 再加 `/`，例如 `/t/r0123456789abcdef01234567/`。裸路由会以 308 跳转至尾 `/` 并保留查询参数。snippet 的新命名空间不改旧中心的 `/` location。

frpc 客户端使用 WSS 443，必须验证域名和公共 CA：

```toml
serverAddr = "relay.example.com"
serverPort = 443
transport.protocol = "wss"
transport.tls.useSystemCa = true
transport.tls.serverName = "relay.example.com"
```

`serverName` 必须与证书域名一致，不能改成网关 IP 或关闭验证。客户端必须具备公共 CA 信任；缺少可信根时修复 CA bundle，不降低校验。固定版本的 `useSystemCa` 行为见[frpc 配置示例](https://github.com/fatedier/frp/blob/v0.72.0/conf/frpc_full_example.toml)。路由 token 和 metadata 由 bootstrap/CLI 提供，本脚本不生成它们。

Otoha 发行包提供 `runtimes/ca-bundle.pem`，由 CLI 将其绝对路径写入 `transport.tls.trustedCaFile`，同时保留 `useSystemCa=true`。普通用户不需要自行配置证书。当前固定公共根文件来自 [curl 的 Mozilla CA 提取](https://curl.se/docs/caextract.html)，数据日期为 2026-11-05，SHA-256 为 `427c064216761a1e72a8b30f0d954d08cb6e4435a82091fb865afd20c8497285`；许可为 MPL 2.0，发行物保留来源和许可。它用于信任公网 TLS 证书，不是 frps 的自签 CA。

为绕过部分机器的代理 Fake-IP，默认部署可把 frpc `serverAddr` 设为经信任的固定网关 IP，同时保持 `serverName` 为证书域名。这个物理连接选项不改公开项目 URL、系统 DNS 或代理配置。更换网关时必须一起更新可信发行配置并重新核验；不从不可信输入接收任意替代地址。

## 校验与失败恢复

应用先执行临时二进制的 `frps verify`，再安装自有文件，执行已安装二进制的 `frps verify` 和 `nginx -t`。通过后才 enable/start 或 restart 独立 frps，确认其 active，最后 reload NGINX；不 restart NGINX。

失败时恢复本轮写入文件原有的字节、权限和归属，撤去新文件/空目录，并恢复独立 frps 原有启用与运行状态。若已尝试 NGINX reload，只恢复磁盘配置并执行 `nginx -t`，不再次 reload；脚本会明确要求核查 NGINX 运行态，不宣称运行配置已恢复。只有本轮新建的账号才会清理。恢复失败会报告 `rollback incomplete`；进程被强制终止或主机掉电不在进程内回滚的保证范围。

实际部署后另行验证原有 NGINX 域名仍响应、TLS/WSS 证书校验、路由注册与拒绝、Host/前缀保留、中心请求及断线重连。这些不由 dry-run 或语法通过自动证明。

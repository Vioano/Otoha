# Otoha Relay

这是默认中继的注册表与 frps 授权插件，使用 Python 标准库和私有 SQLite。它给每个队长中心分配独立地址及中继凭据；项目成员仍用中心自己的身份和权限，relay token 不授予项目管理员权限。

服务只监听 `127.0.0.1:28882`。现有 HTTPS 入口仅转发 `/relay/v1/`，`/frp` 留给本机 frps。它不提供账户平台，不管理 Cloudflare/Tailscale 用户账号，也不允许客户端申请任意 TCP 端口。

## 部署

状态目录必须在仓库外，且只有服务用户能访问。首次初始化不覆盖已有配置或数据库：

```sh
python3 otoha_relay.py init \
  --state /var/lib/otoha-relay \
  --public-url https://relay.example.com \
  --server-addr relay.example.com \
  --server-name relay.example.com \
  --frp-version 0.72.0
python3 otoha_relay.py serve --state /var/lib/otoha-relay --port 28882
```

`init` 创建 `relay.json`，权限 `0600`；SQLite 保存路由描述、幂等请求、到期和撤销状态，以及凭据的 SHA-256。恢复材料和 route token 的明文不入库。状态目录要求 `0700`；备份须包含整个私有目录，不能只复制仍在写入的 SQLite 主文件。

默认配置面向 alpha：最多 64 个未到期且未撤销的租约、4096 条保留记录，每个租约 7 天。注册体最多 8192 bytes。到期记录继续保留幂等身份；公开 API 不提供无限路由或无限历史存储。此容量限制不等于按用户计费或完整反滥用系统。

`server_addr` 可以是可达域名或现有中继的固定 IP；`server_name` 保留受信任证书的域名。frpc 必须启用 WSS 和系统 CA 验证，不能因为使用 IP 拨号关闭 TLS 验证。

## HTTP 合同

响应 JSON 不缓存。错误格式为 `{"error":"code"}`；以下示例全部使用虚构值。

### 发现

`GET /relay/v1/manifest` 无需凭据，返回：

```json
{
  "schema": "otoha-relay/1",
  "default_endpoint": "https://relay.example.com",
  "registration_url": "https://relay.example.com/relay/v1/routes",
  "server_addr": "relay.example.com",
  "server_port": 443,
  "server_name": "relay.example.com",
  "protocol": "wss",
  "frp_version": "0.72.0",
  "limits": {
    "request_bytes": 8192,
    "active_routes": 64,
    "retained_routes": 4096,
    "route_ttl_seconds": 604800
  }
}
```

Manifest 也说明支持的操作和撤销范围，不包含 token、已注册路由列表或团队标签。`GET /health` 检查本机注册表可读；不能证明公网、frps 或某个团队中心已就绪。

### 注册与未知结果恢复

客户端**先持久保存**下面的请求，再发送 `POST /relay/v1/routes`。`enrollment_secret` 必须由客户端随机生成 32 bytes，使用无 padding 的规范 base64url 编码，共 43 个字符。不能把它作为 URL 参数。

```json
{
  "request_id": "example-setup-1",
  "enrollment_secret": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
  "label": "Example team"
}
```

示例 secret 只展示格式，真实客户端须使用密码学随机源。`request_id` 为 1–80 个 ASCII 字母、数字、`_`、`.` 或 `-`，首字符为字母或数字。`label` 去除两端空白后为 1–120 个字符，UTF-8 最多 256 bytes。

成功返回 `200`：

```json
{
  "schema": "otoha-relay/1",
  "recovered": false,
  "route": {
    "id": "r0123456789abcdef01234567",
    "public_url": "https://relay.example.com/t/r0123456789abcdef01234567",
    "server_addr": "relay.example.com",
    "server_port": 443,
    "server_name": "relay.example.com",
    "protocol": "wss",
    "proxy_name": "otoha-center",
    "token": "EXAMPLE_ONLY_NOT_A_REAL_ROUTE_TOKEN",
    "custom_domain": "relay.example.com",
    "location": "/t/r0123456789abcdef01234567/",
    "frp_version": "0.72.0",
    "created_at": 1800000000,
    "expires_at": 1800604800,
    "state": "active"
  }
}
```

`public_url` 没有尾 `/`；`location` 有尾 `/`，只授权这一条路径。中心适配层负责从收到的请求路径去掉自己的前缀；frps 不替中心做这个转换。中心 API 路径按 `public_url + "/v1/..."` 组成。

相同 request ID、secret 和规范化后的 label 返回同一地址、token 和当前状态，`recovered: true`，不创建第二条路由，也不延长到期时间。已有 ID 改 secret 或 label、已有 secret 改 ID，均返回 `409 request_conflict`，原状态保留。容量耗尽返回 `429 relay_capacity_reached`；保留记录耗尽返回 `429 relay_registration_limit_reached`。

token 是用 enrollment secret、route ID 和独立协议域作 HMAC-SHA256 得到的 43 字符 base64url 值，因此相同请求可以恢复它，而无需在数据库保存明文。它只供这个中心连接中继，不能转发给普通成员，也不能替代中心的 owner credential。

### 查看、续租与撤销

以下请求均使用 `Authorization: Bearer ROUTE_TOKEN`：

| 请求 | 结果 |
| --- | --- |
| `GET /relay/v1/routes/ID` | 返回 `{schema,route}`，不重复返回 token |
| `POST /relay/v1/routes/ID/renew` | 从当前时刻续租一个 TTL，保持原 ID/地址；body 可为空或 `{}` |
| `POST /relay/v1/routes/ID/revoke` | 幂等撤销，body 可为空或 `{}` |

`state` 为 `active`、`expired` 或 `revoked`。已过期且未撤销的路由可以在容量允许时续租；被撤销的路由续租返回 `403 route_revoked`，不能借恢复接口复活。重复注册会返回已有路由的真实状态，客户端不能把 `recovered` 当作在线就绪。

受管客户端在重启和正常运行期间自动续租，不要求用户每七天重新初始化。续租响应不返回 token，客户端保留自己的私有 token。无凭据或不匹配的 token 返回 `401 route_unauthorized`。

**撤销和过期阻止后续 Login/NewProxy 授权，当前版本不会主动拆除已经建立的控制连接或数据流。** 不把它报告成即时断开或现有连接已终止；需要即时终止时，须另行明确运维动作与范围。

## frps 插件

使用 frp 官方的 [Server Plugin](https://gofrp.org/en/docs/features/common/server-plugin/) HTTP 合同。实际请求为 `POST /frp?version=0.1.0&op=Login` 或 `op=NewProxy`，body 为 `{content:{...}}`。也接受 body 中的 op；如果 query 和 body 不一致则拒绝。

```toml
[[httpPlugins]]
name = "otoha-route-auth"
addr = "127.0.0.1:28882"
path = "/frp"
ops = ["Login", "NewProxy"]
```

frpc 必须使用：

```toml
serverAddr = "relay.example.com"
serverPort = 443
user = "r0123456789abcdef01234567"
metadatas.otoha_token = "EXAMPLE_ONLY_NOT_A_REAL_ROUTE_TOKEN"
transport.protocol = "wss"
transport.tls.enable = true
transport.tls.useSystemCa = true
transport.tls.serverName = "relay.example.com"

[[proxies]]
name = "otoha-center"
type = "http"
localIP = "127.0.0.1"
localPort = 12345
customDomains = ["relay.example.com"]
locations = ["/t/r0123456789abcdef01234567/"]
```

其中 `localPort` 由 CLI 填入当前中心的 loopback 端口，不由注册 API 指定。真实 `NewProxy.proxy_name` 带 frp user 前缀，即 `ID.otoha-center`。插件在 Login 读取 `content.user` 与 `content.metas.otoha_token`，在 NewProxy 重新检查 `content.user.user` 和 `content.user.metas.otoha_token`。

NewProxy 只允许该路由的固定代理名称、`http` 类型、唯一 custom domain 和唯一 location；其他团队路径、根路径、额外域名、TCP/UDP、remote port、subdomain、group/group key、STCP key 和 multiplexer 都被拒绝。允许响应为 `{"reject":false,"unchange":true}`，拒绝仍以 HTTP 200 返回 frp 的 `reject` 与稳定错误码；未知插件操作也拒绝。

## 验证

```sh
python3 -m unittest discover -s services/relay/tests -v
python3 -m py_compile services/relay/otoha_relay.py
```

测试使用虚构域名、随机凭据和临时状态，覆盖重启恢复、同请求并发幂等、不同意图冲突、容量与到期、续租、撤销、跨团队代理拒绝、实际插件 HTTP 形状和请求大小限制。真实 WSS 穿透、跨设备、中心关闭后的行为与端到端接入时间需另行验证。

# Contract: 服务端传输模式 (server-transport)

本特性不新增 / 不改动任何 HTTP API 端点契约——所有 REST 路由、请求/响应、鉴权头、限流在两种传输模式下**逐字节一致**（FR-008）。本契约描述新增的**配置键**与**启动行为**。

## 配置契约：`[server] tls`

```toml
[server]
listen   = "127.0.0.1:8080"   # host:port，两模式都必填且须合法
# tls 缺省 = HTTPS（安全默认）。仅在 TLS 终止反代后才设 false 走明文。
tls      = false              # 可选布尔；缺省/true = HTTPS，false = 明文 HTTP
tls_cert = "/etc/lanweave/cert.pem"   # 仅 TLS 模式要求可读；明文模式忽略
tls_key  = "/etc/lanweave/key.pem"    # 同上
data_dir = "/var/lib/lanweave"
```

| 输入 | 解析 | 启动行为 |
|------|------|----------|
| 无 `tls` 键 | HTTPS | 校验并加载 cert/key；HTTPS 监听 |
| `tls = true` | HTTPS | 同上 |
| `tls = false` | 明文 HTTP | 忽略 cert/key；明文监听 |

**安全不变量**：未显式写 `tls = false` 的任何配置一律 HTTPS。升级既有（无 `tls` 键）配置不降级。

## 启动行为契约

### TLS 模式（缺省 / `tls = true`）

- cert/key 缺失、不可读或无效 → `Validate()` / `app.Run` **返回错误，进程退出非 0**（硬失败，不退回明文）。
- cert/key 就绪 → 加载证书 → `tls.NewListener` 包裹 → 日志 `level=info msg="https listening" addr=<bound>`。
- 行为与本特性引入前完全一致（回归基线）。

### 明文 HTTP 模式（`tls = false`）

- **不**读取 / **不**要求 cert/key；其缺失或不可读**不**阻止启动。
- 监听裸 TCP，明文 HTTP；日志 `level=info msg="http listening (plaintext)" addr=<bound>`。
- 若 `listen` host 为非回环（`0.0.0.0` / `::` / 空(全接口) / 真实 IP / 主机名）：**额外**一条
  `level=warn msg="plaintext HTTP on a non-loopback address; terminate TLS at a reverse proxy and do not expose this listener publicly" addr=<bound>`。
  该 WARN **不拦截**启动（兼容反代独立容器绑 `0.0.0.0`）。
- `listen` host 为回环（`127.0.0.1` / `::1` / `localhost`）：无此 WARN。

## 调用方契约（客户端 / 反代）

- **客户端零改动**：始终连反代公网 `https://` 地址；TOFU / 证书 / `--insecure` 逻辑不变（FR-009）。客户端不感知服务端是否明文。
- **反代**：终止外部 TLS，将明文请求转发至服务端 `tls=false` 监听地址。控制面无状态 + bearer token，不依赖自身 scheme；数据面 WireGuard 始终加密，独立于本传输模式。
- **`X-Forwarded-For` / 可信代理**：不在本特性范围；限流全局无安全影响；明文模式访问日志记录代理 IP（已知小限制）。

## 不破坏的既有契约

- 所有 `/api/v1/*` 端点的方法、路径、请求/响应体、状态码不变。
- 鉴权（`Authorization: Bearer <jwt>`）、全局限流、`/api/v1/healthz` 不变。
- 数据面 WireGuard、nftables 隔离不变。
- 打包：postinstall 仍生成自签证书、默认 HTTPS（FR-009）。

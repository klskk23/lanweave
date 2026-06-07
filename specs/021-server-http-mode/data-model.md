# Data Model: server-http-mode (Phase 1)

本特性不涉及数据库实体，仅有一个配置侧概念 + 一张解析表 + 一张 `Validate()` 行为矩阵。

## 实体

### 传输模式开关 (Transport Mode Setting)

- **存储位置**：服务端 TOML 配置 `[server]` 段，键 `tls`（`ServerConfig.TLS *bool`）。**不入 SQLite**（启动期配置，非持久状态）。
- **取值（三态指针）**：
  | TOML | 解码 (`TLS`) | `TLSEnabled()` | 含义 |
  |------|--------------|----------------|------|
  | （无 `tls` 键） | `nil` | `true` | HTTPS（安全默认，既有配置升级不变） |
  | `tls = true` | `&true` | `true` | HTTPS（显式） |
  | `tls = false` | `&false` | `false` | 明文 HTTP（仅此一态） |
- **读**：恒经 nil 安全方法 `TLSEnabled() bool`（`s.TLS == nil || *s.TLS`）。任何「是否 TLS」判断不得直接解引用 `s.TLS`。
- **不变量**：未表态（nil）一律解析为 HTTPS；只有显式 `tls = false` 才明文（FR-002，绝不静默降级）。

### 关联既有字段（行为受模式影响，本身不改类型）

- `ServerConfig.TLSCert` / `TLSKey`：TLS 模式下要求可读 + 可加载（FR-004 硬失败）；明文模式下被忽略（FR-005）。
- `ServerConfig.Listen`：两模式都要求合法 `host:port`；其 host 部分参与「明文绑定告警」判定。

## 有效传输模式解析 (Effective Transport Resolution)

启动期由 `app.Run` 定一次：

```
TLSEnabled() == true  → 加载证书 → tls.NewListener → srv.Serve(tlsLn)   [HTTPS]
TLSEnabled() == false → 跳过证书 → srv.Serve(裸 net.Listener)           [明文 HTTP]
                         └─ 若 WarnPlaintextExposure(): log.Warn(...)（不拦截）
```

## 明文绑定告警判定 (Plaintext Exposure Warning)

纯函数 `WarnPlaintextExposure() bool == !TLSEnabled() && !listenIsLoopback()`：

| 模式 | `listen` host | 告警 |
|------|---------------|------|
| 明文 | `127.0.0.1` / `::1` / `localhost` | 否 |
| 明文 | `0.0.0.0` / `::` / `""`(如 `:8080`) | 是 |
| 明文 | 真实网卡 IP / 主机名 | 是 |
| TLS（任意） | 任意 | 否 |

- `host == ""`（绑全部接口）、解析失败、非 IP 主机名 → 保守视为非回环（告警侧偏安全）。

## `Validate()` 行为矩阵（证书相关部分）

| 模式 | tls_cert/key 状态 | `Validate()` 结果 |
|------|-------------------|-------------------|
| TLS（缺省 / `tls=true`） | 可读且可加载 | 通过（行为同本特性前） |
| TLS（缺省 / `tls=true`） | 缺失 / 不可读 / 无效 | **失败**（硬失败，FR-004，不降级） |
| 明文（`tls=false`） | 任意（含缺失/不可读） | 该项**不校验**（FR-005，忽略证书） |

> `server.listen` 合法性、`data_dir`、`ratelimit`、`wireguard`、`auth`、`admin` 等其余校验在两模式下均保持无条件，与本特性前一致。

## 状态流转

无运行时状态流转——传输模式在启动期一次性确定，进程生命周期内不变。修改 `tls` 需改配置并重启（与所有 `[server]` 配置项一致）。

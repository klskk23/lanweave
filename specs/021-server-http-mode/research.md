# Research: server-http-mode (Phase 0)

源码核验基于当前仓库 `internal/server/config/config.go`、`internal/server/app/app.go`、`internal/server/app/app_test.go`、`config.toml.example` 与 `DESIGN.md`（实读）。

## R1 — 开关如何承载「零值=HTTPS、显式 false 才明文」？（本特性最关键决策，FR-002）

**结论：用 `TLS *bool`（三态指针），不可用裸 `bool`。**

- 裸 `bool toml:"tls"`：TOML 缺该键时解码为 `false`。这会让**所有未写 `tls` 的既有配置**升级后解析成「明文」——正是 FR-002 / SC-002 严禁的静默降级。证伪。
- `*bool toml:"tls"`：`go-toml/v2`（仓库已用 `github.com/pelletier/go-toml/v2`）对指针字段的语义为「键缺省→保持 `nil`；键存在→分配并赋值」。于是三态可分：
  | TOML | 解码后 | 含义 |
  |------|--------|------|
  | （无 `tls` 键） | `nil` | HTTPS（安全默认，既有配置不变） |
  | `tls = true` | `&true` | HTTPS（显式） |
  | `tls = false` | `&false` | 明文 HTTP（仅此一态） |
- 取值经 **nil 安全方法** `func (s ServerConfig) TLSEnabled() bool { return s.TLS == nil || *s.TLS }` 统一读取——即便某路径漏调 `applyDefaults` 也绝不把 nil 误判成明文，且永不解引用 nil。

**决策**：`ServerConfig` 增 `TLS *bool toml:"tls"`；所有「是否 TLS」判断只经 `TLSEnabled()`。保留 `tls = false` 这一直观配置 UX（与 ROADMAP §021 / spec 验收措辞一致）。

**Rationale**：三态指针是 Go/TOML 区分「未表态」与「显式 false」的惯用法；nil 安全方法是「不降级」不变量的兜底。

**Alternatives considered**：(a) 裸 `bool`——证伪，零值=明文=降级；(b) 反转字段 `listen_plaintext bool`（零值 false=TLS-on）——也安全，但配置键不如 `tls = false` 直观，且与 ROADMAP/spec 既有措辞、`config.toml.example` 既有 `[server]` 风格偏离，弃用；(c) 字符串枚举 `mode = "https"|"http"`——需额外解析/校验，比单 `*bool` 重，弃用。

## R2 — 证书校验如何条件化？（FR-004 硬失败 / FR-005 明文忽略）

**结论：把 `config.Validate()` 现有证书三步包进 `if TLSEnabled()` 分支。**

- 现状 `Validate()`（config.go:149-155）**无条件**对 `tls_cert`/`tls_key` 调 `requireReadable` + `tls.LoadX509KeyPair`。
- 改为：仅 `TLSEnabled()` 为真时执行这三步（缺/坏证书 → 收集错误 → `Run` 在 `cfg.Validate()` 处 `return`，硬失败，FR-004）。`TLSEnabled()` 为假（明文）时**完全跳过**，证书字段缺失或不可读都不报错（FR-005）。
- `server.listen` 的 host:port 校验保持无条件（两模式都要合法监听地址）。

**决策**：证书校验条件化；其余 `Validate()` 逻辑不动。

**含义**：`app.Run` 里第二处证书加载（app.go:84-91 的 `tls.LoadX509KeyPair`）也须移进 TLS 分支，否则明文模式仍会因加载缺失证书而失败——见 R3。

## R3 — `app.Run` 监听器如何按模式分叉？

**结论：在已有「显式 `net.Listen` + `tls.NewListener`」结构上分叉，明文模式跳过证书加载与 TLS 包裹。**

现状（app.go:84-142）恒：加载证书 → 建 `tls.Config` → `net.Listen` → `tls.NewListener` → `srv.Serve(tlsLn)` → 日志 `"https listening"`。改为：

```
tlsOn := cfg.Server.TLSEnabled()
var tlsConfig *tls.Config
if tlsOn {
    cert, err := tls.LoadX509KeyPair(cfg.Server.TLSCert, cfg.Server.TLSKey) // 明文模式不走这步
    ... 建 tlsConfig；srv.TLSConfig = tlsConfig
}
ln, err := net.Listen("tcp", cfg.Server.Listen)   // 两模式共用
serveLn := ln
if tlsOn {
    serveLn = tls.NewListener(ln, tlsConfig)
    log.Info("https listening", "addr", ln.Addr().String())
} else {
    if cfg.Server.WarnPlaintextExposure() {       // 见 R4
        log.Warn("plaintext HTTP on a non-loopback address; terminate TLS at a reverse proxy and do not expose this listener publicly", "addr", ln.Addr().String())
    }
    log.Info("http listening (plaintext)", "addr", ln.Addr().String())
}
... srv.Serve(serveLn)
```

- `http.Server.TLSConfig` 仅在 TLS 分支设置；明文分支留 nil，`srv.Serve(裸 ln)` 即明文 HTTP。
- `Ready(ln.Addr().String())` 回调位置不变（测试发现端口，两模式一致）。
- 控制面 handler / 限流 / 鉴权完全不动（FR-008）：已核验限流为单一全局 `rate.NewLimiter`（app.go:99，非按-IP），无 cookie / secure flag / `r.TLS` / 绝对 URL 逻辑；`RemoteAddr` 仅用于访问日志。反代终止 TLS 不破坏任何 handler 语义。

**决策**：监听器分叉如上；明文模式不加载证书、不设 `TLSConfig`、`Serve` 裸 listener。

## R4 — 「明文 + 非回环」告警决策（FR-006 / FR-007，下沉为可测纯函数）

**结论：在 `config` 包加纯方法 `WarnPlaintextExposure() bool`，`app` 只负责据其记 WARN。**

```
func (s ServerConfig) WarnPlaintextExposure() bool { return !s.TLSEnabled() && !s.listenIsLoopback() }

func (s ServerConfig) listenIsLoopback() bool {
    host, _, err := net.SplitHostPort(s.Listen)
    if err != nil { return false }          // 解析不了 → 当作非回环（告警侧偏安全）
    if host == "" { return false }          // ":8080" 绑全部接口 → 非回环
    if host == "localhost" { return true }
    ip := net.ParseIP(host)
    if ip == nil { return false }           // 主机名 → 保守视为非回环
    return ip.IsLoopback()
}
```

真值（明文模式下）：`127.0.0.1`/`::1`/`localhost` → 不告警；`0.0.0.0`/`::`/`""`(全接口)/真实网卡 IP/主机名 → 告警。TLS 模式恒不告警（`!TLSEnabled()` 为假）。

**决策**：决策逻辑纯函数化置于 `config`，表驱动单元测试穷举；`app` 仅 `if cfg.Server.WarnPlaintextExposure() { log.Warn(...) }`，不拦截启动（FR-006「不拦」）。

**Rationale**：把「是否告警」从 IO/启动流程剥离成纯函数，符合宪法 II「可测核心下沉」；`net.SplitHostPort`/`net.ParseIP`/`IP.IsLoopback` 均标准库确定性行为。

**Alternatives considered**：在 `app` 内联判断——不可无头单元测，弃用。解析主机名做 DNS 判回环——引入网络依赖与不确定性，弃用（保守告警即可，运维看到告警自行确认）。

## R5 — 测试策略（宪法 II 三层，复用既有骨架）

**无头单元**（`config_test.go`，无特权）：
- `TLSEnabled()` 三态：解码无 `tls` 键 → true；`tls=true` → true；`tls=false` → false。
- `Validate()`：明文模式（`tls=false`）下 `tls_cert`/`tls_key` 为空或不可读 → **无**证书相关错误；TLS 模式（缺省 / `tls=true`）下缺证书 → 有错误（硬失败）。
- `WarnPlaintextExposure()` 真值表：覆盖 R4 全部分支。

**集成/验收**（`app_test.go`，真 WG/nft，`unshare -rUn`，复用 `writeConfig`/`newCerts`/`Ready` 骨架）：
- **US1**：`writeConfig` 变体写 `tls = false`，启动后用**裸 `http.Client`** 打 `http://addr` 完成 login → 携 token 调受保护端点（`/api/v1/nodes` 或 `/me`）成功；证明明文模式端到端可用、无需证书。
- **US2-1**：缺省配置（不写 `tls`）启动 → HTTPS 客户端成功；可附「裸 http 打同端口失败/被拒」断言强化「确实是 HTTPS」。（既有 `TestRunServesAndShutsDown` 已覆盖缺省=HTTPS 主路径。）
- **US2-2**：`tls = true`（或缺省）+ 删除/指向不存在的证书 → `app.Run` 返回错误（纯配置校验失败，**无需特权**，仿 `TestRunRejectsMissingAdminPassword`）。
- **US3**：`tls = false` + `listen = "0.0.0.0:0"` 启动 → 成功 `Ready`（证明告警**不拦截**，end-to-end 非阻断）。「WARN 出现」由 R4 纯函数单元测确定性覆盖。

**回归（SC-005）**：`unshare -rUn sh -c 'ip link set lo up && go test ./...'` 全绿；既有 TLS 测试不改。

**关于 US3 的「日志出现」**：唯一可观测副作用是一条 WARN 日志，决策为纯函数（单元穷举）。集成层证明「非回环明文绑定不阻断启动」即 US3 的端到端要义；不为「捕获日志行」而给 `app.Options` 加注入 logger（避免仅为测试扩面）。此口径在 quickstart.md 写明。

## R6 — DESIGN.md / config 示例同步（FR-010，宪法强制）

待改三处（同 PR）：
- `DESIGN.md:53`「**控制面**：HTTPS REST + JSON」→「HTTPS REST（或经 TLS 终止反代后的明文 HTTP）+ JSON」。
- `DESIGN.md:221`「> 全部 HTTPS。Content-Type...」→「> HTTPS，或 TLS 终止反代后的明文 HTTP。Content-Type...」。
- `DESIGN.md §11`（表，line 368 起）新增一行接受风险：「服务端明文 HTTP 监听（`tls=false`） | 仅显式开启才明文；须置于 TLS 终止反代之后；非回环绑定启动告警；勿将明文监听暴露公网；缺省/`tls=true` 仍 HTTPS 且缺证书硬失败」。
- `config.toml.example:7`「# HTTPS bind address (host:port). HTTPS only; there is no plaintext listener.」→ 改为说明默认 HTTPS、`tls=false` 走明文（反代终止 TLS）；并在 `[server]` 段补注释 `tls` 键（缺省=true=HTTPS）。

**Rationale**：宪法规定 `DESIGN.md` 为跨特性权威、项目级风险仅可在 §11 接受；spec 不得与 DESIGN 矛盾，矛盾须同 PR 修订 DESIGN。

# Implementation Plan: server-http-mode

**Branch**: `021-server-http-mode` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/021-server-http-mode/spec.md`

## Summary

让服务端在「TLS 终止反向代理」之后可选监听明文 HTTP：新增 `[server] tls` 开关，**默认/未设置=HTTPS**（绝不静默降级），仅显式 `tls = false` 才走明文。TLS 模式行为完全不变（缺/坏证书仍硬失败）；明文模式忽略证书；明文 + 非回环绑定打一条 WARN 但不拦截。仅服务端改动，客户端 / WireGuard 数据面 / 打包零改动。

**技术取向**：用 `*bool`（三态：nil=未设置→HTTPS，`&false`=明文，`&true`=HTTPS）承载开关，使「键缺省」与「显式 false」可区分——这是满足 FR-002「零值=HTTPS、不降级」的关键。`config.Validate()` 把证书可读性校验包进「仅 TLS 模式」分支；`app.Run` 把监听器按模式分叉（`tls.NewListener` vs 裸 `net.Listener`）。绑定告警决策下沉为 `config` 包纯函数，无头可测。

## Technical Context

**Language/Version**: Go 1.26.2

**Primary Dependencies**: 标准库 `net` / `net/http` / `crypto/tls` / `log/slog`；`github.com/pelletier/go-toml/v2`（已在用，解码 `*bool` 三态）。无新增第三方依赖。

**Storage**: 无新增持久化。SQLite 仍是状态源；本特性只影响**控制面监听传输**（HTTPS vs 明文 HTTP），不触 DB / nftables / WireGuard。

**Testing**: 三层（宪法 II，跨进程边界）。**单元**（`internal/server/config`，无头）：`TLSEnabled()` 三态、`Validate()` 在明文模式跳过证书 / 在 TLS 模式缺证书仍硬失败、绑定告警决策真值表。**集成/验收**（`internal/server/app`，真 WG/nft，`unshare -rUn`）：明文模式打通受保护调用（US1）、缺省配置仍 HTTPS（US2）、明文绑 `0.0.0.0` 仍启动不拦（US3）；TLS 模式缺证书硬失败为纯配置校验失败（无需特权，仿 `TestRunRejectsMissingAdminPassword`）。

**Target Platform**: Linux 服务端（小 VPS，root + CAP_NET_ADMIN）。

**Project Type**: 单 Go module 的 server 子树（`internal/server/...`、`cmd/lanweaved`）。

**Performance Goals**: 不引入每请求新开销；明文模式略省 TLS 握手。冷启动到 `/healthz` 200 仍 ≤ 3s（宪法 IV，集成测试已断言 3s 预算）。

**Constraints**：开关零值必须解析为 HTTPS（升级既有配置不降级，最关键硬约束）；TLS 模式缺 / 坏证书硬失败不退回明文；告警不拦截（兼容反代独立容器绑 `0.0.0.0`）。

**Scale/Scope**: 改动面小——`config.go`（+1 字段、+2 方法、Validate 一处条件化）、`app.go`（监听器分叉 + 告警）、`config.toml.example`（注释）、`DESIGN.md`（措辞 + §11 风险登记）。

## Constitution Check

*GATE: 通过，无违背，无需 Complexity Tracking。*

- **I. Code Quality**：`config` 增 `TLS *bool` 单字段 + `TLSEnabled()`（nil 安全）+ `WarnPlaintextExposure()`（绑定告警纯决策）；`app.Run` 按模式分叉监听器，无新包、无投机抽象。`gofmt`/`go vet`/`staticcheck` 须干净。错误皆为值；无新 `panic`；无散落 `os.Getenv`（开关走既有 TOML 单点）。SQLite 仍唯一状态源，本特性不引入运行时隐藏状态。**通过**。
- **II. Testing（NON-NEGOTIABLE）**：跨进程边界（监听器），三层齐。可测核心（三态解析 / 证书条件校验 / 告警决策）下沉到无头 `config` 单元测试；每个用户故事 ≥1 真启动集成验收（真 WG/nft，禁 mock）。既有 TLS 集成测试保持绿（回归 SC-005）。**通过**。
- **III. UX Consistency**：仅服务端，无客户端 UI 面（FR-009 客户端零改动）。不适用于 GUI，但「明文降级须显式、绝不静默」即面向运维的一致与可预期。**通过**。
- **IV. Performance**：无新每请求路径；冷启动预算不变。**通过**。
- **Security & Operational Discipline**：FR-002「零值=HTTPS」本身即安全门——`*bool` 三态确保升级不降级；TLS 缺证书硬失败保留。明文监听属项目级风险，**必须**在 `DESIGN.md §11` 风险登记新增接受项（宪法规定项目级风险仅可在 §11 接受，FR-010 同 PR 落地）。无密钥入日志（证书路径非密钥；明文模式 bearer token 仍只在 Authorization 头，不记日志）。**通过**。
- **Workflow**：specify→plan→tasks→implement；DESIGN.md 同 PR 修订（FR-010）；ROADMAP 021 于合并提交勾选。**通过**。

## Project Structure

### Documentation (this feature)

```text
specs/021-server-http-mode/
├── plan.md                    # 本文件
├── research.md                # Phase 0：三态开关 / 证书条件校验 / 监听器分叉 / 告警决策 / 测试与 DESIGN 修订
├── data-model.md              # Phase 1：传输模式开关实体 + 解析表 + Validate 行为矩阵
├── quickstart.md              # Phase 1：无头测试命令 + 集成场景 + 反代手工冒烟
├── contracts/
│   └── server-transport.md    # Phase 1：[server] tls 配置键契约 + 启动行为/日志/退出码
└── tasks.md                   # Phase 2（/speckit-tasks 生成，非本命令）
```

### Source Code (repository root)

```text
internal/server/config/config.go        # 既有：ServerConfig 增 TLS *bool；TLSEnabled() / WarnPlaintextExposure() / 内部 loopback 判定；Validate() 证书校验条件化
internal/server/config/config_test.go   # 既有：增三态、证书条件校验、告警决策表的单元测试
internal/server/app/app.go              # 既有：按 TLSEnabled() 分叉监听器（TLS vs 明文），明文+非回环打 WARN
internal/server/app/app_test.go         # 既有：增 US1/US2/US3 集成/验收测试（复用 writeConfig / Ready 骨架）
config.toml.example                     # 既有：第 7 行注释改为「默认 HTTPS；tls=false 走明文（反代终止 TLS）」+ 注释 tls 键
DESIGN.md                               # 既有：控制面措辞放宽 + §11 风险登记新增一行（FR-010）
```

**Structure Decision**：可测核心（三态解析、证书条件、告警判定）置于 `config` 包纯逻辑，`go test ./internal/server/config/...` 无头即可断言；`app.Run` 仅做「按模式选监听器 + 记日志」。此分层服务宪法 II（可测核心）并把真启动验收留给已有 `app` 集成骨架。

## Complexity Tracking

> 无违背。本特性为既有配置/监听路径的最小条件化扩展，未引入新包、新依赖或新抽象，故不填写。

# Implementation Plan: tunnel-auto-reconnect

**Branch**: `028-tunnel-auto-reconnect` | **Date**: 2026-06-08 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/028-tunnel-auto-reconnect/spec.md`

## Summary

客户端纯前端自愈特性。两部分：

1. **源端口随机**（FR-017）——已满足。每次 `Connect()` 都新建 wireguard-go `device.NewDevice` 并用 `conn.NewDefaultBind()`（不在 UAPI 写 `listen_port`），OS 自动分配临时端口。本切片不改代码，仅在 DESIGN.md 记录该不变量并加回归测试守护。

2. **握手陈旧自愈重连**（FR-001~FR-016）——新增一个独立的健康巡检 goroutine（15s ticker，与现有 `pollLoop` 节点刷新解耦），在 `desiredConnected==true` 且隧道存活时读取最后握手时间，age 超过 240s 则自动重连；重连失败每 15s 重试直到恢复或用户手动断开。引入仅内存态的 `desiredConnected` 用户意图标志，配合单飞守卫串行化所有 connect/disconnect 转换，手动断开必胜，顺带修掉「在途 Connect 复活已拆除隧道」的既有竞态。UI 把状态从 `(state, desiredConnected)` 派生：重连窗口显示黄色「连接中…」、按钮维持「断开」、全程静默。防火墙严格镜像手动路径（重连窗口内关闭）。

技术手段：在 `tunnel` 包暴露纯函数 `parseHandshakeAge`（解析 UAPI `last_handshake_time_sec`）与可注入 `now`/`threshold` 的 `Stale()` 判定，达成零 wall-clock sleep 的单元测试；netns `unshare -rUn` + 真实 wireguard-go 做集成/验收。

## Technical Context

**Language/Version**: Go 1.23

**Primary Dependencies**: golang.zx2c4.com/wireguard（wireguard-go，进程内数据面）、fyne.io/fyne/v2（桌面 GUI）。**不新增任何依赖。**

**Storage**: 无新增持久化。`desiredConnected` 刻意仅存内存（不落配置/SQLite），保证应用重启不自动连接（FR-006/FR-007/FR-008）。

**Testing**: `go test`；单元层用包内 `fakeEngine` 缝（已存在于 `tunnel_test.go`）+ 注入 `now`/`threshold`；集成/验收层 `unshare -rUn` 跑真实 wireguard-go（禁止 mock，Constitution II）。

**Target Platform**: Windows 客户端（开发/CI 的隧道集成测试在 Linux netns 跑）。

**Project Type**: 桌面应用（单仓 Go，`internal/client/*`）。

**Performance Goals**: 链路恢复后首个新握手 ≤3s（Constitution IV）；陈旧检出到发起重连的巡检延迟 ≤15s（SC-001 端到端 ≤一个轮询周期 + 握手）。

**Constraints**: 巡检不得阻塞节点列表刷新（FR-005，故独立 goroutine）；自愈全程静默无弹窗（FR-015）；防火墙在重连窗口内必须关闭（FR-016）。

**SC-006 覆盖说明**：「自愈不拖慢节点刷新」由「健康巡检与 `pollLoop` 是两个独立 goroutine、不共享锁/通道」的结构性设计保证，**不另设墙钟计时测试**（计时断言天然易抖动，违宪法 II「禁 flaky」）。该不变量在 T009/T010 的代码评审中核验。

**Scale/Scope**: 单客户端单隧道；改动集中在 `internal/client/tunnel/tunnel.go` 与 `internal/client/ui/panel.go`，外加 DESIGN.md §6 修订。

## Constitution Check

*GATE: Phase 0 前必须通过；Phase 1 设计后复检。*

适用原则（v1.0.1 全部四条均适用）：

- **I. 代码质量与简洁**：复用既有 `engine` 缝与 `fakeEngine`，不引入新抽象层；`desiredConnected` 是单个布尔意图标志而非状态机框架；健康 goroutine 与 `pollLoop` 同构（都是 ticker 循环）。✅ 无过度设计。
- **II. 测试（不可妥协）**：不 mock wireguard-go/nftables/SQLite。三层：①单元——`parseHandshakeAge` 纯解析 + `Stale(now, threshold)` 注入时钟，零 sleep；②集成——`unshare -rUn` 起真实 wireguard-go，构造握手时间戳陈旧，断言触发重连且新 device 端口不同；③验收——每条 User Story ≥1 条（US1 自愈恢复、US2 重连期间 UI 反馈与可中止、US3 不擅自建连）。无 flaky/重试。✅
- **III. 用户体验一致**：持久连接状态指示器在重连窗口显示「连接中…」黄色（派生自 `(state, desiredConnected)`），不出现误导性「失败」红色；长操作有反馈；自愈静默不打断（FR-015 明确要求无确认弹窗，属非破坏性后台动作，符合原则）。✅
- **IV. 性能**：恢复后首个握手 ≤3s（沿用现有 `connectTimeout`=8s 上限内，正常 ≤3s）；在线状态滞后 ≤30s（巡检 15s 周期满足）。✅

**结论：Constitution Check 通过，无违例，Complexity Tracking 留空。**

## Project Structure

### Documentation (this feature)

```text
specs/028-tunnel-auto-reconnect/
├── plan.md              # 本文件
├── research.md          # Phase 0 输出
├── data-model.md        # Phase 1 输出
├── quickstart.md        # Phase 1 输出
├── contracts/           # Phase 1 输出（内部 API + UI 契约）
│   └── tunnel-health.md
├── checklists/
│   └── requirements.md  # /speckit-specify 输出
└── tasks.md             # /speckit-tasks 输出（本命令不创建）
```

### Source Code (repository root)

```text
internal/client/tunnel/
├── tunnel.go                  # 改：暴露握手时间/Stale()，Connect/Disconnect 单飞 + desiredConnected reconcile，新增健康巡检入口
├── profile.go                 # 不改（已确认 BuildUAPIConfig 不写 listen_port → 端口随机）
├── tunnel_test.go             # 改：新增 Stale()/parseHandshakeAge 单元测试，复用 fakeEngine
└── tunnel_integration_test.go # 改：netns 真实 wireguard-go——陈旧触发重连 + 端口随机回归

internal/client/ui/
└── panel.go                   # 改：启停健康 goroutine，status 从 (state, desiredConnected) 派生，按钮/黄灯，防火墙镜像

DESIGN.md                      # 改：§6 记录握手陈旧自愈、desiredConnected 语义、重连窗口防火墙关闭、源端口=OS临时端口不变量
```

**Structure Decision**: 单仓桌面应用既有布局，不新增包/目录。隧道生命周期与自愈判定归 `internal/client/tunnel`（带可测纯函数 + 注入时钟），UI 状态派生与 goroutine 生命周期归 `internal/client/ui/panel.go`，两者经现有 `Tunnel` 公共方法交互。

## Complexity Tracking

> 无 Constitution 违例，留空。

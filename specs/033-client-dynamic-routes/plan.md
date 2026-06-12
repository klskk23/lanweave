# Implementation Plan: 消费端动态路由（宣告子网对全平台成员可达）

**Branch**: `033-client-dynamic-routes` | **Date**: 2026-06-12 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/033-client-dynamic-routes/spec.md`

## Summary

030–033 功能链收官：成员客户端把「所在 zones 的宣告合成段」动态配进本机隧道。**服务端零改动**（030 已备齐 peer AllowedIPs 与查询接口、DTO 自带宣告者名）。Windows 端：`Tunnel.SetExtraRoutes`——wireguard-go `IpcSet`（`replace_allowed_ips=true`，server peer 增量更新，**不断流不重连**）+ 平台路由差分（linux netlink / windows netsh，沿 addr_{linux,windows}.go 既有分文件模式）；panel 控制器连接期 60s 对账 + 面板「可达子网」区块。OpenWrt 端：`engine.SetRoutes`（wgctrl ReplaceAllowedIPs + 内核路由差分），复用 032 对账循环同轮数据双路消费（自己的→NAT，他人的→路由，排除自己防回环）。聚合双视图提为两端共用的纯函数（FR-009 跨端一致的机械保证）。

## Technical Context

**Language/Version**: Go 1.26（仓库现状）

**Primary Dependencies**: 既有全覆盖（wireguard-go device.IpcSet、wgctrl、netlink、netsh 先例、apiclient/protocol 030 DTO）。**零新增依赖、零服务端改动。**

**Storage**: 无（纯派生态）

**Testing**: unit（双视图聚合/路由差分/UAPI 增量文本纯函数）；integration（tunnel_integration 装具演进：wireguard-go netns 真流量 + 热更新断言）；e2e（032 announce_test 装具演进：routerd 消费链路真流量，宣告者以合成地址直挂 peer 模拟——NETMAP 端已由 032 单独验证）；acceptance（SC-005 真机功能链收口，TODO 勾销条件）

**Target Platform**: Windows 客户端 + OpenWrt 路由器（linux 集成测试承载 Windows 路径逻辑，netsh 行为实机矩阵）

**Project Type**: 双客户端增量

**Performance Goals**: 刷新 60s 周期 N+1 次 API（与 032 同量级）；IpcSet 增量微秒级不触握手；路径数 ≤ 宣告配额×成员数（10² 量级路由，内核/wireguard-go 无感）

**Constraints**: 无需重连即生效；断开零残留；冲突逐条跳过不致命；API 失败冻结；单向性不削弱；031/032/Windows 零回归

**Scale/Scope**: tunnel +SetExtraRoutes/平台 addRoute/delRoute；panel +对账驱动与展示区块；engine +SetRoutes；routerd 对账循环扩展 + `announce list --all`；新共享聚合函数（双视图）；测试三层

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 适用性与符合方式 | 结论 |
|---|---|---|
| I. Code Quality | 聚合双视图单一真源共用（消除两端各写一份的漂移）；SetExtraRoutes/SetRoutes 对称小接口；零新依赖；无持久化副本 | PASS |
| II. Testing Standards | wireguard-go/wgctrl/netlink 真实例；真流量集成（两端）；先红后绿；netsh 实机豁免（017/018/031 先例，逻辑层 linux 承载）；每 story ≥1 验收测试 | PASS |
| III. UX Consistency | 跨端可达集合一致（FR-009 共用聚合机械保证）；面板冲突/刷新失败可见；list --all 区分 MINE；零手动操作 | PASS |
| IV. Performance | 不断流热更新；60s 周期与 032 对齐；幂等空操作跳过 | PASS |
| Security & Operational | 单向性不削弱（仅出向路径，反向仍被服务端 ct 收口）；服务端零改动=攻击面零增量；§11 无新增 | PASS |
| Workflow Gates | spec→plan→tasks→implement；DESIGN.md §9 消费端一句 + ROADMAP 同 PR；TODO 勾销留待 SC-005 真机（诚实原则，PR 不勾） | PASS |

**Post-Phase-1 re-check**: 无新增违规。点名取舍：①Windows 热更新走 IpcSet 增量而非重连（D1，spec「无需重连」硬约束）；②routerd e2e 的宣告者侧以合成地址直挂模拟（D6——NETMAP 已由 032 e2e 全真验证，消费链路全真，避免双 routerd 拓扑的装具爆炸）。无 Complexity Tracking 条目。

## Project Structure

### Documentation (this feature)

```text
specs/033-client-dynamic-routes/
├── plan.md / research.md / data-model.md / quickstart.md
├── contracts/consumer-routes.md
└── tasks.md（/speckit-tasks 产出）
```

### Source Code (repository root)

```text
internal/client/routesync/routesync.go(+_test)  # 新：聚合双视图纯函数（两端共用；032 reconcile.Desired 迁移合并或委托）
internal/client/tunnel/tunnel.go                # 改：SetExtraRoutes（IpcSet 增量 + 路由差分 + 集合记忆/幂等/逐条容错）
internal/client/tunnel/addr_linux.go            # 改：addRoute/delRoute（netlink）
internal/client/tunnel/addr_windows.go          # 改：addRoute/delRoute（netsh）
internal/client/tunnel/tunnel_integration_test.go # 改：热更新+真流量+冲突跳过
internal/client/panel/…                         # 改：连接期 60s 对账驱动 + 「可达子网」区块 + i18n
internal/router/engine/engine.go(+_test)        # 改：SetRoutes（ReplaceAllowedIPs + 路由差分）
internal/router/reconcile/reconcile.go(+_test)  # 改：双视图（委托 routesync 或参数化）
cmd/lanweave-routerd/main.go / announce.go      # 改：对账循环双路消费；announce list --all
cmd/lanweave-routerd/announce_test.go           # 改：消费端 e2e（D6 拓扑）
DESIGN.md / docs/ROADMAP.md                     # 改：§9 一句 + 033 行
```

**Structure Decision**: 聚合提为 `internal/client/routesync`（client 树内，routerd 已 import client 系包无层级问题；reconcile 委托之避免双实现）。tunnel/engine 各自平台机制独立演进，行为契约对齐。

## Complexity Tracking

无宪法违规，本表为空。

## 已接受的限制（设计期显式登记）

- **netsh 路由增删的实机维度**：linux 集成测试承载全部逻辑（差分/幂等/容错），netsh 命令本身的行为差异入实机矩阵（既有豁免先例）。
- **离组/撤回的 ≤60s 窗口**：窗口内本地路径残留无害（服务端数据面先收口，030 既定）。
- **TODO.md 勾销时点**：SC-005 真机验收完成时人工勾销，本 PR 不勾（CI 维度不构成功能链收口的证据）。
- **路由对账与 028 自愈重建的间隙**：重建后路径丢失 ≤60s 自动恢复；实现期在 tryUp 成功路径加即时对账触发（若侵入小则做，否则留对账兜底并记录）。

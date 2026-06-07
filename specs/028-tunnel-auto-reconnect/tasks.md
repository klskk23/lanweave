---
description: "Task list for 028-tunnel-auto-reconnect"
---

# Tasks: tunnel-auto-reconnect

**Input**: Design documents from `specs/028-tunnel-auto-reconnect/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/tunnel-health.md, quickstart.md

**Tests**: 按宪法 II（测试不可妥协）——本特性跨 WireGuard 内核边界，MUST 含单元 + 集成测试；每条 User Story MUST ≥1 条端到端验收测试。故下列测试任务均为必需，非可选。

**Organization**: 任务按 User Story 分组以支持独立实现与测试。改动集中在 `internal/client/tunnel/tunnel.go`、`internal/client/ui/panel.go`，外加 DESIGN.md §6。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: US1/US2/US3
- 同一文件的任务必须串行（不标 [P]）

## Path Conventions

单仓 Go 桌面应用，路径相对仓库根。隧道生命周期/自愈判定归 `internal/client/tunnel/`，UI 状态派生/goroutine 生命周期归 `internal/client/ui/panel.go`。

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 确认基线绿、锁定改动面

- [X] T001 确认基线：`go build ./...` 与 `go test ./internal/client/...` 全绿，记录当前 `internal/client/tunnel/tunnel.go`（`handshaked()` 解析 `last_handshake_time_sec=`）与 `internal/client/ui/panel.go`（`pollLoop`、`onConnect`、`onDisconnect`、`statusView`、`primaryActionLabel`）为改动基线

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: tunnel 包共享原语——所有 User Story 都建立其上。MUST 在 US1/US2/US3 之前完成。

⚠️ 以下 T002–T005 均改 `internal/client/tunnel/tunnel.go`，必须串行。

- [X] T002 在 `internal/client/tunnel/tunnel.go` 抽出纯函数 `parseHandshakeAge(uapi string) (lastHandshakeUnix int64, ok bool)`，从 UAPI 文本解析 `last_handshake_time_sec=`（=0 或缺字段→`(0,false)`，畸形不 panic），并令现有 `wgEngine.handshaked()` 复用之（契约 C1）
- [X] T003 在 `internal/client/tunnel/tunnel.go` 的 `Tunnel` 加 `desiredConnected bool` 字段与 `SetDesired(bool)`/`Desired() bool` 访问器（仅内存、互斥保护），并加可注入的 `staleThreshold time.Duration` 字段（`New` 默认 240s）（契约 C3、数据模型「连接意图」）
- [X] T004 在 `internal/client/tunnel/tunnel.go` 加 `Stale(now time.Time) bool` 谓词：读取最后握手 unix，仅当 `unix>0 && now-unix > staleThreshold` 为真；暴露引擎读取最后握手时间戳的途径（扩展 `engine` 接口或复用 `parseHandshakeAge`）（契约 C2、数据模型「握手陈旧度」）
- [X] T005 在 `internal/client/tunnel/tunnel.go` 的 `Connect`/`Disconnect`/`teardown` 加单飞守卫并在每次 `Connect` 尝试结束后对照 `Desired()` reconcile：若 `Desired()==false` 则拆除并 `eng.close()`、`state=Disconnected`，杜绝「在途 Connect 写 `state=Connected` 复活已拆隧道 + 泄漏 device」的既有竞态（契约 C4、FR-011/FR-012）

⚠️ 以下单元测试均改 `internal/client/tunnel/tunnel_test.go`，串行；复用现有 `fakeEngine`，零 wall-clock sleep。

- [X] T006 在 `internal/client/tunnel/tunnel_test.go` 加单元测试：`parseHandshakeAge`（有值/=0/缺字段/畸形）与 `Stale` 边界（注入 `now`+`staleThreshold`，age=239s→false、=241s→true、从未握手→false）（quickstart U1/U2）
- [X] T007 在 `internal/client/tunnel/tunnel_test.go` 加单元测试：新建 Tunnel `Desired()==false`（U3）；单飞 reconcile——手动 `Disconnect` 夹在在途 `Connect` 中间，终态 `Disconnected` 且 `fakeEngine.close` 被调用（无复活、无泄漏，U4）

**Checkpoint**: tunnel 原语就绪且单测通过——可开始 User Story。

---

## Phase 3: User Story 1 - 链路静默断掉后自动恢复连接 (Priority: P1) 🎯 MVP

**Goal**: 客户端自己发现已死连接并自动重连，把「假在线」变「自愈」。

**Independent Test**: 连上后外部令链路失效，不做手动操作，客户端在阈值内自行进入「正在连接」并在链路恢复后回「已连接」、流量恢复。

**依赖**: Phase 2 完成。

⚠️ T008–T010 均改 `internal/client/ui/panel.go`，串行。

- [X] T008 [US1] 在 `internal/client/ui/panel.go`：`onConnect` 成功路径调用 `p.tn.SetDesired(true)`；`onDisconnect` 调用 `p.tn.SetDesired(false)`（FR-006，意图随手动操作落定）
- [X] T009 [US1] 在 `internal/client/ui/panel.go` 新增 `healthLoop` goroutine（自带 15s ticker，**与 `pollLoop` 解耦**，FR-004/FR-005）：每 tick 若 `p.tn.Desired()` 且 (`p.tn.Stale(now)` 或 `State()==Disconnected`) 则走与 `onConnect` 相同的重连路径（含 `ReconcileFirewall`，FR-016）；失败下一 tick 再试、固定周期无退避（FR-010）
- [X] T010 [US1] 在 `internal/client/ui/panel.go` 的 `start()` 启动 `healthLoop`，并随 panel 关闭经 quit channel 退出（参照 `trafficQuit` 模式，契约 C5）

⚠️ T011–T012 均改 `internal/client/tunnel/tunnel_integration_test.go`，串行。

- [X] T011 [P] [US1] 在 `internal/client/tunnel/tunnel_integration_test.go` 加集成测试（`unshare -rUn` 真实 wireguard-go）：(a) 建立隧道→注入极小 `staleThreshold` 使 `Stale` 越界→断言自动重连重新握手成功（quickstart I1）;(b) **SC-002 稳态守护**：健康隧道(握手正常刷新)下用默认 240s 阈值连续观察多个判定点,断言 `Stale` 恒为 false、不触发任何重连(配合 T006 单元边界,共同覆盖 SC-002 无误判)
- [X] T012 [US1] 在 `internal/client/tunnel/tunnel_integration_test.go` 加验收测试 A1：链路失效→无任何手动操作→≤一个巡检周期(15s)+首握手内自动恢复 `Connected`、流量恢复（SC-001）

**Checkpoint**: US1 独立可用 = MVP。链路静默断掉能自愈。

---

## Phase 4: User Story 2 - 重连期间的界面反馈与用户可中止 (Priority: P2)

**Goal**: 自愈期间界面显示「正在恢复」而非「失败」，且用户能随时中止后台重试。

**Independent Test**: 重连进行中界面显示「正在连接」语义且主按钮为「断开」；点断开后停止重连并停在断开态。

**依赖**: US1（需 `Desired()` 与重连窗口存在）。

- [X] T013 [US2] 在 `internal/client/ui/panel.go` 把 UI 状态从**三输入** `(state, Desired(), connFailed)` 派生（**保留**既有 `connFailed` 红色失败态,勿丢弃）：`statusView`/`buildHero`/`applyStatus`/`primaryActionLabel` 遵守优先级——`Desired()==true` 黄灯「连接中…」+按钮「断开」**优先且不看 connFailed**（含重连窗口 `Disconnected && Desired()`,FR-013/FR-014）;红色「失败」**仅** `!Desired() && connFailed`(FR-009);皆否则灰色「未连接」(数据模型 UI 派生表「优先级」段)
- [X] T014 [US2] 在 `internal/client/ui/panel.go` 确保 `healthLoop` 重连路径全程静默——不复用 `onConnect` 的错误弹窗、不弹任何对话框，仅更新状态行与按钮（FR-015）；中止入口经 `onDisconnect`→`SetDesired(false)`+单飞使后台重试立即停止
- [X] T015 [US2] 在 `internal/client/ui/panel_test.go`（新建）加验收测试 A2,表驱动覆盖三输入优先级：`(Disconnected, desired=true, *)`→黄色「连接中」+「断开」(非红,FR-013/FR-014);`(Disconnected, desired=false, connFailed=true)`→红色「失败」+「连接」(FR-009);`(Disconnected, desired=false, connFailed=false)`→灰色「未连接」(数据模型优先级段)

**Checkpoint**: US1 + US2 = 自愈可观察、可中止。

---

## Phase 5: User Story 3 - 自愈只维持「用户已建立的连接」，不擅自建立连接 (Priority: P2)

**Goal**: 钉死自愈边界——手动断开不被连回、手动连接失败不被无限重试、开机不自动连接。

**Independent Test**: 分别验证手动断开后、手动连接失败后、应用重启后，客户端均保持断开、不发起自动重连。

**依赖**: Phase 2（单飞/意图）；与 US1 共享 panel 接线。

- [X] T016 [US3] 在 `internal/client/ui/panel.go` 加边界守卫：`onConnect` **仅成功路径** `SetDesired(true)`（失败保持 false→不触发后台重试，FR-009）；确认启动时 `Desired()` 默认 false、`start()` 不自动 `Connect`（FR-007，US3-3）
- [X] T017 [P] [US3] 在 `internal/client/tunnel/tunnel_integration_test.go` 加集成测试 I3：自愈重连窗口内调用手动 `Disconnect`→重试停止、终态 `Disconnected`、防火墙关、无复活（SC-003/SC-005、Edge「手动断开与自动重连竞争」）
- [X] T018 [US3] 在 `internal/client/tunnel/tunnel_test.go` 加验收测试 A3（fakeEngine）：手动连接失败后 `Desired()==false` 且无自动重试尝试（SC-004）；模拟「重启」=新建 Tunnel→`Desired()==false`、无自动连接（SC-007）

**Checkpoint**: 三条 Story 全部交付，自愈边界完备。

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: 跨切关注点——源端口不变量回归、设计文档固化、全量校验。

- [X] T019 [US1] 在 `internal/client/tunnel/tunnel_integration_test.go` 加回归测试 I2：连续两次 `Connect()` 抓取各自本地 UDP 源端口，断言不同（守护「不写 `listen_port`→OS 随机临时端口」不变量，FR-017/SC-008）
- [X] T020 [P] 修订 `DESIGN.md` §6：记录握手陈旧自愈重连、`desiredConnected` 仅内存态语义、重连窗口内防火墙关闭、源端口=OS 临时端口不变量（plan「DESIGN.md 修订」）
- [X] T021 收尾校验：`go vet ./...` + `go build ./...` + `unshare -rUn go test ./internal/client/...`（含集成/验收）全绿

---

## Dependencies & Execution Order

```
Phase 1 (Setup) → Phase 2 (Foundational) → Phase 3 (US1, MVP)
                                              ├→ Phase 4 (US2)  [依赖 US1]
                                              └→ Phase 5 (US3)  [依赖 Phase 2 + US1 接线]
                                                   └→ Phase 6 (Polish)
```

- **US1 (P1)** 是 MVP，单独交付即构成可用自愈。
- **US2/US3 (P2)** 均建立在 US1 之上（共享 `Desired()`/重连窗口/panel 接线），非完全独立，但各自有独立验收。
- 同文件任务串行：tunnel.go(T002→T005)、tunnel_test.go(T006→T007→T018)、panel.go(T008→T010→T013→T014→T016)、tunnel_integration_test.go(T011→T012→T017→T019)。

## Parallel Opportunities

并行度有限（改动集中在 4 个文件）。明确可并行：

- T011（集成测试，integration_test.go）与 T008–T010（panel.go）属不同文件 → `[P]`
- T017（integration_test.go）与 T016（panel.go）属不同文件 → `[P]`
- T020（DESIGN.md）与任何代码任务属不同文件 → `[P]`

## Coverage Notes（/speckit-analyze 修订）

- **SC-002（健康连接 0 误重连）**：由 T006（单元 `Stale` 边界 239→false）+ T011(b)（集成稳态多判定点 `Stale` 恒 false、0 重连）共同覆盖。
- **SC-006（自愈不拖慢节点刷新）**：记为**设计保证**——健康巡检与 `pollLoop` 为两个独立 goroutine、不共享锁/通道,结构上互不阻塞;**不设墙钟计时测试**(易抖动,违宪法 II)。在 T009/T010 评审中核验。
- **US2 中止行为**：US2 AC#3「点断开即止」的端到端验证由 T017（标 US3 的「手动断开必胜」集成测试）覆盖,T015 仅测可视派生;二者合计覆盖 US2。

## Implementation Strategy

1. **MVP 优先**：Phase 1→2→3 跑通即得可用自愈（US1）。可在此停下交付验证。
2. **增量增强**：再做 Phase 4（可观察/可中止）与 Phase 5（边界安全）。
3. **收尾**：Phase 6 锁定源端口不变量、固化 DESIGN.md、全量绿。

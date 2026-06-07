# Phase 0 Research: tunnel-auto-reconnect

所有关键决策已在 `/grill-me` 阶段敲定，无遗留 NEEDS CLARIFICATION。本文档把分支访谈结论固化为「决策 / 理由 / 备选」格式，供后续阶段引用。

## D1. 源端口随机——现状即满足，仅文档化 + 回归守护

- **Decision**: 不改代码。每次 `Connect()` 新建 `device.NewDevice` 并用 `conn.NewDefaultBind()`，UAPI 配置（`BuildUAPIConfig`）不写 `listen_port`，wireguard-go 绑定端口 0 → OS 分配随机临时端口。加一条集成回归测试断言「两次连接的本地源端口不同」，DESIGN.md §6 记录该不变量。
- **Rationale**: 源端口随机本就是当前实现的天然结果；与其新增配置不如把它锁成显式不变量，防止未来误加固定 `listen_port` 回归。
- **Alternatives considered**: ①显式生成随机端口写进 UAPI——多余且更脆（端口冲突需重试）；②不加测试——无法防回归，违背把它列为 FR-017 的初衷。

## D2. 陈旧阈值 = 240s

- **Decision**: 最后握手 age > 240s 判定陈旧。
- **Rationale**: keepalive=25s、健康链路在 REKEY_AFTER_TIME(120s) 内必刷新握手，正常 age 上界约 120s。240s ≈ 2× 该上界，给 NAT 抖动/临时丢包留足缓冲，几乎不可能误判（SC-002 零误重连）。用户在访谈中明确选「更保守 240s」。
- **Alternatives considered**: 120s——贴近正常上界，抖动易误判；180s——可接受但裕度小于用户偏好。

## D3. 巡检周期 = 15s，独立 goroutine

- **Decision**: 新建专用健康巡检 goroutine，自带 15s ticker，与现有 `pollLoop`（节点列表 15s 刷新）解耦。
- **Rationale**: 用户先选「inline 进 pollLoop」，随即改口「不想阻塞 pollLoop，把改动放到新 goroutine」。独立 goroutine 满足 FR-005（巡检不拖慢节点刷新），且与 SC-001（恢复 ≤ 一个周期 + 握手）一致；15s 周期满足 Constitution IV 在线滞后 ≤30s。
- **Alternatives considered**: inline 进 pollLoop——巡检里的重连（可能耗时到 8s connectTimeout）会阻塞节点刷新，被用户否决。

## D4. desiredConnected——仅内存态用户意图标志

- **Decision**: 新增内存布尔 `desiredConnected`。手动连接**成功**置 true；手动断开置 false；**不持久化**。自愈重连仅当 `desiredConnected==true` 且隧道曾建立时触发。
- **Rationale**: 区分「用户想连」与「当前 state」。①失败的手动连接不置 true → 不会触发后台重试（FR-009）；②>8s 故障使 state 跌回 Disconnected 时，单次重连会放弃，需用意图标志驱动持续重试（FR-008/FR-010）；③不持久化 → 应用重启不自动连接（FR-006/FR-007，US3）。
- **Alternatives considered**: ①只看 `state==Connected`——故障跌到 Disconnected 后无从判断该不该重连，访谈中自我修正否决；②持久化意图——会导致重启自动连接，违背 US3。

## D5. 重试策略——固定 15s 周期，无退避，重试到恢复或手动断开

- **Decision**: 重连失败后随下一个 15s 巡检 tick 再试，固定周期、无指数退避，直到握手恢复或用户手动断开。
- **Rationale**: 局域网/小规模 mesh 场景，链路恢复通常是二元的（通/不通），退避只会拖慢恢复。固定周期实现简单、行为可预测（SC-001）。
- **Alternatives considered**: 指数退避——恢复延迟不可控且超出 SC-001 的「≤一个周期」承诺。

## D6. 单飞守卫 + 手动断开必胜——顺带修既有竞态

- **Decision**: 所有 connect/disconnect 转换经单一串行点；任一 `Connect()` 尝试结束后对照 `desiredConnected` reconcile（若用户中途断开则拆除并 close device）。手动断开始终胜出。
- **Rationale**: 既有 bug——在途 `Connect()` 在隧道被拆除后仍写 `t.state = Connected`，复活隧道并泄漏 device。串行化 + reconcile 同时实现 FR-011/FR-012（手动断开必胜、不被复活，SC-003/SC-005）并修掉该竞态。
- **Alternatives considered**: 无守卫——保留竞态，US2 可中止性无法保证。

## D7. UI——状态派生自 (state, desiredConnected)，复用黄灯，静默

- **Decision**: 重连窗口内 UI 显示黄色「连接中…」、主按钮维持「断开」；状态由 `(state, desiredConnected)` 计算而非裸 `state`。自愈全程无弹窗/通知（FR-015）。防火墙严格镜像手动 `ReconcileFirewall`：重连窗口内关闭。
- **Rationale**: 用户选「复用黄灯」「按钮显示断开」。黄色表「连接中」语义正确，避免误导性红色失败态（FR-013，Constitution III 一致性指示器）。静默因自愈是非破坏性后台动作。
- **Alternatives considered**: 新增「重连中」第四态——UI 复杂度上升，用户明确选复用现有黄色态。

## D8. tunnel.Stale() 谓词 + 可注入时钟

- **Decision**: 在 `tunnel` 包提供纯函数 `parseHandshakeAge`（解析 UAPI `last_handshake_time_sec`）与 `Stale(now, threshold)` 判定，`now`/`threshold` 可注入。
- **Rationale**: 让陈旧判定可零 wall-clock sleep 单测（Constitution II 禁 flaky/sleep）。判定逻辑落在 tunnel 包而非 UI，便于集成测试直接驱动。
- **Alternatives considered**: 在 UI 内联时间比较——不可测、违背测试分层。

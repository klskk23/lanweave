# Quickstart: tunnel-auto-reconnect 验证场景

按 Constitution II 三层执行。集成/验收用 netns + 真实 wireguard-go，禁 mock；单元用注入时钟，禁 wall-clock sleep。

## 前置

```sh
# 隧道集成测试在 Linux netns 跑真实 wireguard-go
unshare -rUn go test ./internal/client/tunnel/ -run Integration -v
# 纯单元（解析 + Stale 注入时钟）
go test ./internal/client/tunnel/ -run 'Stale|ParseHandshake|Desired|SingleFlight' -v
```

## 单元场景（零 sleep）

- **U1 parseHandshakeAge**：喂入含 `last_handshake_time_sec=1717000000` 的 UAPI 文本 → `(1717000000, true)`；`=0` 或缺字段 → `(0, false)`；畸形输入不 panic。
- **U2 Stale 阈值边界**：注入 `now`，构造 age=239s → false；age=241s → true；从未握手(unix=0) → false。
- **U3 Desired 默认态**：新建 Tunnel → `Desired()==false`（应用启动不自动连接）。
- **U4 单飞 reconcile（fakeEngine）**：手动断开夹在在途 Connect 中间 → 终态 Disconnected 且 `eng.close` 被调用（device 不泄漏、不复活）。

## 集成场景（netns + 真实 wireguard-go）

- **I1 陈旧触发重连**：建立隧道，人为令对端停发握手 / 把 `threshold` 注入为很小值使 age 越界 → 健康巡检检出 Stale，自动发起重连，断言隧道重新握手成功。
- **I2 源端口随机回归（FR-017/SC-008）**：连续两次 `Connect()`，抓取各自本地 UDP 源端口 → 断言两端口不同（守护「不写 listen_port」不变量）。
- **I3 手动断开必胜（SC-003/SC-005）**：自愈重试窗口内调用手动 Disconnect → 重连停止，终态 Disconnected，防火墙关，无复活。

## 验收场景（每条 User Story ≥1）

- **A1（US1 链路静默断掉后自动恢复）**：连接成功 → 断链使握手陈旧 → 不做任何手动操作，≤一个巡检周期(15s)+首握手内隧道自动恢复 Connected（SC-001）。
- **A2（US2 重连期间界面反馈与可中止）**：进入重连窗口 → UI 显示黄色「连接中…」、按钮为「断开」、无弹窗（FR-013/14/15）；点「断开」立即终止重连（US2 可中止）。
- **A3（US3 自愈只维持已建立连接）**：从未手动连接（或手动连接失败）→ 后台**不**发起任何重连（FR-008/FR-009）；应用重启后保持未连接（FR-006/FR-007）。

## 防火墙不变量抽查（FR-016）

重连窗口内查 nftables：隧道未实际连通 → 防火墙关；重连成功后 → 防火墙开。任意时刻 防火墙开 ⟺ 隧道实际 Connected。

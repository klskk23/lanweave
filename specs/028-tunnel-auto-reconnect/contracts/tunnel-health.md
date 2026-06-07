# Contract: Tunnel Health & Auto-Reconnect

本特性纯内部（无网络/RPC 端点）。契约面是 `internal/client/tunnel` 包对 `internal/client/ui` 暴露的 Go API，以及 UI 的状态派生约定。措辞为意图契约，非最终签名（实现期可微调）。

## C1. 纯函数：解析握手时间

```go
// parseHandshakeAge 从 wireguard-go UAPI 文本中提取 last_handshake_time_sec，
// 返回 (lastHandshakeUnix, ok)。无该字段或为 0 → ok=false / unix=0。
// 纯函数，无 I/O、无时钟，便于零-sleep 单测。
func parseHandshakeAge(uapi string) (lastHandshakeUnix int64, ok bool)
```

**契约**：
- 输入含 `last_handshake_time_sec=1717000000` → 返回 `(1717000000, true)`。
- 输入含 `last_handshake_time_sec=0` 或缺字段 → 返回 `(0, false)`。
- 解析失败不 panic。

## C2. 陈旧判定（可注入时钟/阈值）

```go
// Stale 报告隧道最后握手是否已超过 threshold。
// now 与 threshold 注入，单测无需 wall-clock sleep。
// 仅当存在有效握手(lastHandshakeUnix>0)且 age>threshold 时为 true。
func (t *Tunnel) Stale(now time.Time) bool
```

**契约**：
- 从未握手（unix=0）→ false。
- `now - lastHandshake <= threshold` → false。
- `now - lastHandshake > threshold` → true。
- 默认 `threshold = 240s`，测试可经字段注入覆盖。

## C3. 用户意图标志

```go
// SetDesired 记录用户连接意图（仅内存）。手动连接成功传 true，手动断开传 false。
func (t *Tunnel) SetDesired(connected bool)

// Desired 返回当前用户意图。健康巡检据此决定是否重连。
func (t *Tunnel) Desired() bool
```

**契约**：
- 初始 `Desired() == false`（应用启动不自动连接）。
- 不持久化，进程退出即丢失。

## C4. 单飞连接转换 + reconcile

```go
// Connect / Disconnect 经单一串行点；任一 Connect 尝试结束后对照 Desired() reconcile：
//   - Desired()==false（用户中途断开）→ 拆除并 close device，state 归 Disconnected
//   - Desired()==true 且握手成功 → state=Connected
// 保证：手动断开必胜，在途 Connect 不复活已拆除隧道（修既有竞态）。
```

**契约**：
- 并发「在途 Connect」与「Disconnect」→ 终态恒为 Disconnected 且 device 已 close（无泄漏）。
- 串行：同一时刻至多一个 connect/disconnect 在执行。

## C5. 健康巡检 goroutine 生命周期（UI 侧）

```go
// panel 启动一个独立健康巡检 goroutine（15s ticker，与 pollLoop 解耦）：
//   每 tick：若 Desired() 为 true：
//     - 隧道存活但 Stale(now) → 触发重连（与手动 Connect 同路径，含 ReconcileFirewall）
//     - 隧道已 Disconnected（重试窗口）→ 再次尝试 Connect
//   重连失败 → 下一 tick 再试（固定周期，无退避），直到恢复或 Desired()==false。
// goroutine 随 panel 关闭而退出（quit channel）。
```

**契约**：
- 巡检不阻塞 `pollLoop` 节点刷新（独立 goroutine，FR-005）。
- 自愈全程不弹窗（FR-015）。
- 重连窗口 UI 状态由 `(state, Desired())` 派生（见 data-model 表）。

## C6. 防火墙镜像

```go
// 自愈路径复用手动路径同一 ReconcileFirewall(connected bool)，
// connected 取隧道实际生效态：重连窗口内为 false（防火墙关），重连成功后 true。
```

**契约**：任意时刻防火墙开 ⟺ 隧道实际 Connected（FR-016）。

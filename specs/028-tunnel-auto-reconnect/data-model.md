# Phase 1 Data Model: tunnel-auto-reconnect

本特性无持久化 schema 变更。下列为内存态运行时实体及其状态转换。

## Entity: 连接意图 (Desired Connection State)

仅内存布尔，表达「用户是否希望保持连接」，独立于隧道当前 `state`。

| 字段 | 类型 | 说明 |
|------|------|------|
| `desiredConnected` | bool | true=用户希望维持连接；false=用户希望断开。**不持久化。** |

**转换规则**：

- 手动连接**成功** → `desiredConnected = true`
- 手动连接**失败** → 保持 false（不触发后台重试，FR-009）
- 手动断开 → `desiredConnected = false`（必胜，FR-011）
- 应用启动 → 初始 false（不自动连接，FR-006/FR-007，US3）
- 自愈重连成功/失败 → **不改变**（意图由用户决定，自愈只服务既有意图）

**不变量**：自愈重连仅当 `desiredConnected == true` 才发起（FR-008）。

## Entity: 握手陈旧度 (Handshake Staleness)

从 wireguard-go UAPI 读取的最后握手时间派生，用于判定链路是否静默断掉。

| 字段 | 类型 | 来源 | 说明 |
|------|------|------|------|
| `lastHandshakeUnix` | int64 | UAPI `last_handshake_time_sec=` | 0=从未握手；>0=最后握手 Unix 秒 |
| `age` | duration | `now - lastHandshakeUnix` | 距今时长 |
| `threshold` | duration | 常量 240s（可注入测试） | 陈旧阈值（D2） |

**判定**：`Stale(now) == (lastHandshakeUnix > 0 && age > threshold)`。

- `lastHandshakeUnix == 0`（从未握手）→ 不视为「陈旧重连」目标；归属正常 `Connect()` 首握手超时路径（既有逻辑）。
- 健康链路（keepalive 25s）`age` 上界约 120s，恒 < 240s → 不触发。

## State: 派生 UI 状态（status = f(state, desiredConnected)）

UI 不再裸用 `tunnel.State`，而由**三输入** `(state, desiredConnected, connFailed)` 派生（D7，FR-013/FR-014）。`connFailed` 是既有的「上次手动连接失败」瞬时标志，本特性保留之，仅在其上叠加 `desiredConnected` 维度：

| `state` | `desiredConnected` | `connFailed` | UI 指示 | 主按钮 |
|---------|--------------------|--------------|--------|--------|
| Connected | true | — | 绿色「已连接」 | 断开 |
| Connecting | true | — | 黄色「连接中…」 | 断开 |
| Disconnected | **true**（自愈重试窗口） | — | 黄色「连接中…」 | 断开 |
| Disconnected | false | true（手动连接失败后） | 红色「失败」 | 连接 |
| Disconnected | false | false | 灰色「未连接」 | 连接 |

**优先级（关键，实现 T013 必须遵守）**：`desiredConnected` 黄灯**优先**——只要 `desiredConnected==true` 即显示黄色「连接中…」、按钮「断开」，**不看 `connFailed`**；红色「失败」**仅当 `!desiredConnected && connFailed`** 时出现（手动连接失败留下的态，FR-009）；二者皆否则灰色「未连接」。这样自愈重试窗口（`Disconnected && desiredConnected`）恒为黄色而非红色（FR-013），且手动连接失败（`Desired()` 被 T016 守卫保持 false）仍正确显示红色失败。

## 防火墙不变量（FR-016）

防火墙开关严格镜像隧道连通意图的当前生效态，而非用户意图：

- 隧道实际 Connected → 防火墙开
- 重连窗口（Disconnected 但 desiredConnected）→ 防火墙**关**（与手动断开期一致，链路本就不通）
- 重连成功重新 Connected → 防火墙重新开

即自愈路径调用与手动路径相同的 `ReconcileFirewall(connected bool)`，`connected` 取隧道实际生效态。

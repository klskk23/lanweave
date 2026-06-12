# Data Model: 消费端动态路由 (033)

**服务端零改动、零持久化新增**。全部为运行期派生态。

## 1. 聚合双视图（每次刷新重算；真源=服务器宣告清单）

来源：`ListZones` → 各 zone `ListAnnouncements`（030 API，DTO 已含 `node_name/owner`），按宣告 id 去重。

| 视图 | 过滤 | 去重键 | 消费者 |
|---|---|---|---|
| **宣告者视图**（032 现状） | `node_id == 本机` | 宣告 id | routerd NAT 期望集 / `announce list` |
| **消费者视图**（033 新增） | `node_id != 本机`（Windows 端无本机宣告，等于全部） | **合成段** | 隧道路径集 / 面板「可达子网」/ routerd 视图 |

消费者视图条目：`Synthetic netip.Prefix`、`Subnet`（仅展示）、`Zones []string`、`AnnouncerName string`。

## 2. 隧道路径派生态（per-platform）

| 端 | 载体 | 应用方式 |
|---|---|---|
| Windows | wireguard-go server peer `allowed_ips` + 系统路由表 | `Tunnel.SetExtraRoutes(extra)`：IpcSet `replace_allowed_ips=true`（VPN 网段 ∪ extra）+ 平台路由差分 |
| OpenWrt | 内核 wg server peer AllowedIPs + 内核路由 | `engine.SetRoutes(extra)`：wgctrl ReplaceAllowedIPs + netlink 路由差分 |

不变量：**应用集合 ⊆ 消费者视图合成段集**（冲突条目被剔除）；集合相等时刷新为幂等空操作；断开/Down 后系统层零残留（接口消失即路由消失 + 显式清除兜底）。

## 3. 状态转移

```
断开 ──connect──▶ 连接（路径=∅） ──立即刷新──▶ 连接（路径=消费者视图）
   ▲                                            │ 每 60s 刷新（新增/撤回/离组收敛；API 失败冻结）
   └────────── disconnect/Down（路径全清） ◀──────┘
单条冲突：剔除+告警 ──冲突消除+下轮刷新──▶ 补上
隧道自愈重建（028）：路径随重建丢失 ──≤60s 对账──▶ 恢复
```

## 4. 回归不变量

- 服务端零改动（FR-007）；peer AllowedIPs 服务器侧机制（030）不动。
- VPN /32 互通、zone 隔离、单向性（FR-006）：消费端只增出向路径，反向仍被服务端 ct 收口。
- 031/032 全部命令行为不变；`announce list` 语义不变（新增视图另列）。

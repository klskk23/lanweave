# Data Model: OpenWrt 宣告端 (032)

**无服务端变更、无本地持久化新增**。本切片的全部"数据"是运行期派生态。

## 1. 期望集（desired set，每次对账重算）

来源：服务器宣告清单（030 API），过滤 `node_id == state.Record.NodeID`，按宣告 id 去重。

| 字段 | 来源 | 用途 |
|---|---|---|
| `AnnouncementID` | `AnnouncementResponse.ID` | 去重键 / remove 解析 |
| `Real netip.Prefix` | `.Subnet` | DNAT 翻译目标 |
| `Synthetic netip.Prefix` | `.Synthetic` | DNAT 匹配 + 翻译源 |
| `Zones []string` | 聚合自各 zone 清单 | list 展示 |

不持久化（真源在服务器；本地规则可全量重建——FR-004 不变量）。

## 2. 本地 NAT 派生态（`inet lanweave_rt` 表）

| 链 | hook/priority | 每条宣告的规则 |
|---|---|---|
| `prerouting` | nat / dstnat | `ip daddr <Synthetic> dnat prefix to <Real 基址>`（NF_NAT_RANGE_PREFIX，主机位 1:1） |
| `postrouting` | nat / srcnat | `ip saddr <VPN池(state.Network)> ip daddr <Real> masquerade` |

生命周期：整表全量重建（变更/对账/启动），logout 删整表。**规则存在 ⟺ 期望集含该宣告（至迟一个对账周期内收敛）**。

## 3. 协议触点（apiclient 增量，复用 030 DTO）

| 方法 | 端点 | typed errors |
|---|---|---|
| `CreateAnnouncement(zone, nodeID, subnet)` | `POST /zones/{name}/announcements` | `ErrPlatformUnsupported` `ErrAnnounceDisabled` `ErrSubnetInvalid` `ErrSubnetOverlap` `ErrAnnounceLimit` `ErrSyntheticPoolExhausted` + 既有 404/会话族 |
| `DeleteAnnouncement(zone, id)` | `DELETE /zones/{name}/announcements/{id}` | 既有 not-found 族 |
| `ListAnnouncements(zone)` | `GET /zones/{name}/announcements` | 既有族 |

## 4. 不变量

1. 本地翻译规则是纯派生态：任何时刻可由「服务器清单 ∩ 本机节点」全量重建；无第二真源。
2. `announce add` 的原子性（FR-005）：要么{远端挂接 ∧ 本地规则}，要么{两者皆无}（本地失败→补偿撤回远端；补偿失败→告警 + 030 服务端收口兜底）。
3. 合成段与本机任何接口网段不重叠（FR-008 宣告时检测；冲突→补偿撤回）。
4. 单向性（FR-009）：本表只含 DNAT(合成→真实) 与 masquerade(成员→LAN 方向流量)；不存在反向 DNAT/路径。
5. 服务端与 Windows 客户端零行为变化；031 命令零回归（apiclient 三方法为纯加法）。

## 5. 状态转移

```
（无宣告）
   │ announce add S --zone Z
   │   远端 Create(攻略 030 全校验) → 本地冲突检测(FR-008) → natctl.Rebuild(期望集+S)
   │   └ 本地失败 → Delete(Z, id) 补偿 → 报错（原子性恢复）
   ▼
宣告{S:Z} ── add S --zone Z2 ──▶ 宣告{S:Z,Z2}（同合成段复用，本地规则不变）
   │                                  │ remove S --zone Z2（挂接-1，规则保留）
   │ remove S --zone Z（最后挂接）      ▼
   ▼                              宣告{S:Z}
（远端回收 + 本地 Rebuild 去除规则）

旁路收敛：服务端被第三方移除 ──(≤60s 对账)──▶ 本地 Rebuild 清除
logout ──▶ 删除整表（远端级联归 030）
daemon 启动 ──▶ 对账重建（重启恢复，SC-002）
```

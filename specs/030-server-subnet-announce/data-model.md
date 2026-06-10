# Data Model: 服务端子网宣告控制面 (030)

## 1. Schema 迁移 `0007_announcements.sql`

### 1.1 `nodes` 表新列

| 列 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `platform` | TEXT | NOT NULL DEFAULT 'unknown' | 客户端注册时自报（`^[a-z0-9-]{1,32}$`）；旧节点由 DEFAULT 回填 `unknown` |

### 1.2 `announcements`（宣告本体）

| 列 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | INTEGER | PK AUTOINCREMENT | |
| `node_id` | INTEGER | NOT NULL REFERENCES nodes(id) ON DELETE CASCADE | 宣告节点 |
| `real_base` | INTEGER | NOT NULL | 真实子网基址（uint32，nodes.ip 同存法） |
| `prefix_len` | INTEGER | NOT NULL | 前缀长度，真实段与合成段共用（等长分配） |
| `synthetic_base` | INTEGER | NOT NULL | 合成段基址（uint32） |
| `created_at` | TEXT | NOT NULL | RFC3339 |

约束：`UNIQUE(node_id, real_base, prefix_len)`（FR-005 按（节点,真实子网）唯一）；`UNIQUE(synthetic_base, prefix_len)`（合成段全服唯一的 DB 兜底，真正的不重叠由分配器事务保证）。

### 1.3 `announcement_zones`（挂接）

| 列 | 类型 | 约束 |
|---|---|---|
| `announcement_id` | INTEGER | NOT NULL REFERENCES announcements(id) ON DELETE CASCADE |
| `zone_id` | INTEGER | NOT NULL REFERENCES zones(id) ON DELETE CASCADE |

约束：`UNIQUE(announcement_id, zone_id)`。

## 2. 配置字段

| 键 | 类型 | 语义 |
|---|---|---|
| `[announce] pool` | string (CIDR) | 缺省/空 = 宣告功能停用（写 API 返回 `announce_disabled`）；非空必须合法 IPv4 CIDR 且不与 `wireguard.network` 重叠（Validate 硬失败） |
| `[limits] max_announced_subnets_per_user` | *int | nil→10；0=无限；负数 Validate 失败；admin 豁免（023 三态逐字复刻） |

## 3. 协议 DTO（`pkg/protocol`）

- `RegisterNodeRequest` 增 `Platform string \`json:"platform,omitempty"\``（缺省→unknown）。
- `NodeResponse` 增 `Platform string \`json:"platform"\``。
- 新增：`CreateAnnouncementRequest{NodeID, Subnet}`、`AnnouncementResponse{ID, NodeID, NodeName, Owner, Subnet, Synthetic}`、`AnnouncementListResponse{Announcements []…}`。CIDR 在协议层一律文本（"192.168.1.0/24"），整数仅落库。

## 4. 不变量

1. 任意时刻全部 `(synthetic_base, prefix_len)` 互不重叠，且整体落在 `announce.pool` 内、不与 VPN 池重叠。
2. 同一 `node_id` 名下全部 `(real_base, prefix_len)` 互不重叠；**跨节点不约束**（核心价值）。
3. `announcements` 行存在 ⟺ 至少一行 `announcement_zones` 引用它（应用层事务维护：Detach 删除最后挂接时同事务删除宣告本体；创建时同事务先建本体后建首个挂接）。
4. 宣告挂接存在 ⟺ 该节点是该 zone 成员（创建时校验；退 zone/被踢的同事务清挂接）。
5. 数据面派生态：peer AllowedIPs = 节点 /32 ∪ 该节点全部合成段；`zone_<id>_routes` set = 该 zone 全部挂接的合成段——两者任何时刻可从 DB 全量重建（FR-011）。
6. 配额计数 = 用户名下 `announcements` 行数（挂接数不计；删除释放名额；grandfather：下调只挡新增）。

## 5. 状态转移

```
（无宣告）
   │ POST zone X announcements {node, subnet}
   │   校验: platform=openwrt ∧ 成员(node,X) ∧ 自有 node ∧ CIDR 合法 ∧ 配额
   │   事务: 分配合成段 → INSERT announcement + attachment(X)
   ▼
宣告{S} 挂接{X} ──POST zone Y──▶ 宣告{S} 挂接{X,Y}   （复用 S，不再分配）
   │                                │
   │ DELETE X 挂接                   │ DELETE Y 挂接
   ▼                                ▼
（最后挂接消失 → 同事务删宣告本体，S 回收可复用）
```

级联入口（全部收敛到同一回收路径）：显式 DELETE / 节点 leave zone / owner 踢出 / 删 zone / 删节点 / admin 删用户。每条入口 = DB 变更（事务）+ 紧随的数据面同步（peer AllowedIPs 重算 + routes set 元素删除），008 模式。

## 6. nftables 派生态形状

```
table inet lanweave {
  chain forward (policy drop) {
    ct state established,related accept          # 全链一条（回程，单向性）
    ip saddr @zone_1 ip daddr @zone_1 accept      # 既有成员互通规则
    ip saddr @zone_1 ip daddr @zone_1_routes accept  # 新增：成员→合成段
    ...每 zone 两条...
  }
  set zone_1        { type ipv4_addr; }                    # 既有，/32 成员
  set zone_1_routes { type ipv4_addr; flags interval; }    # 新增，合成 CIDR
}
```

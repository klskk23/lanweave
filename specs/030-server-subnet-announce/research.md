# Research: 服务端子网宣告控制面 (030)

> Phase 0 产出。输入：spec.md（FR-001~014）、grill 共识 2026-06-11、现有代码普查
> （ipam / netfw / wg / store / app dataplane / api / protocol，2026-06-11）。

---

## D1 — 合成段分配器：事务内首次适配（first-fit）对齐块分配

**Decision**: 在 `ipam` 包新增纯数学的「块分配」：给定池 CIDR、所需前缀长度、已占用块列表（按基址排序），返回第一个**自然对齐**且不与任何已占块重叠的空闲块。store 层在同一 SQLite 事务内 `SELECT` 现存 `announcements` 的占用块 → 计算 → `INSERT`，写锁串行天然并发安全（IPAM /32 分配同款思路）。块以 `(base uint32, prefix_len int)` 持久化（沿用 nodes.ip 的 INTEGER 存法）。

**Rationale**: 与现有 `ipam.PoolRange`/`AddrToUint32` 风格一致（纯函数 + store 事务组合）；块数量级小（每用户配额 10），O(n) 扫描远低于性能预算；自然对齐是 CIDR 语义要求（/24 必须 256 对齐），也使回收后复用（FR-010/SC-003）自动成立。

**Alternatives considered**: 预切固定 /24 网格（浪费池空间、不支持 /16–/30 变长）；buddy allocator（过度设计，宪法 I 反对）。均拒绝。

## D2 — Schema：迁移 0007，两张新表 + nodes.platform 列

**Decision**: `0007_announcements.sql`：

- `ALTER TABLE nodes ADD COLUMN platform TEXT NOT NULL DEFAULT 'unknown'`（FR-001 旧节点回填语义由 DEFAULT 直接达成）。
- `announcements(id, node_id REFERENCES nodes ON DELETE CASCADE, real_base INTEGER, prefix_len INTEGER, synthetic_base INTEGER, created_at, UNIQUE(node_id, real_base, prefix_len), UNIQUE(synthetic_base, prefix_len))`。
- `announcement_zones(announcement_id REFERENCES announcements ON DELETE CASCADE, zone_id REFERENCES zones ON DELETE CASCADE, UNIQUE(announcement_id, zone_id))`。

**Rationale**: ON DELETE CASCADE 让「删节点/删 zone → 挂接消失」由 SQLite 兜底（008 先例：DB 级联 + 紧随的数据面同步仍由应用层做）；「最后一个挂接消失 → 整条宣告回收」是应用层事务逻辑（SQL 级联表达不了），由 store 的 Detach 在同事务内检查并删除。real/synthetic 用 `(base, prefix_len)` 整数对而非文本 CIDR，重叠判定纯整数区间比较，无解析歧义。

**Alternatives considered**: 宣告直接挂 zone（每 (node,subnet,zone) 一行独立分配合成段）——违反 FR-005 复用语义、浪费池。拒绝。

## D3 — nftables：每 zone 第二个 interval set + 路由放行规则 + 全链一次性 conntrack 规则

**Decision**:
- 现有成员 set（`zone_<id>`，纯 /32）**不动**。
- 每 zone 新增 `zone_<id>_routes` set：`KeyType: TypeIPAddr, Interval: true`，元素为该 zone 已挂接宣告的合成 CIDR。
- 每 zone 新增一条规则：`ip saddr @zone_<id> ip daddr @zone_<id>_routes accept`（成员 → 本 zone 合成段，新建方向）。
- forward 链**头部**新增一条全局规则（Rebuild 时第一条建）：`ct state established,related accept`——承载合成段 → 成员的回程（FR-009/FR-013 单向语义：回包必有 conntrack 表项，新建方向反着来则无表项、落 policy drop）。
- `Rebuild` 全量重建扩展：zones 携带 `RouteCIDRs []netip.Prefix`；增量 API 新增 `AddZoneRoute(zoneID, prefix)` / `RemoveZoneRoute(zoneID, prefix)`，`AddZone`/`DeleteZone` 同步建/删第二 set 与规则。

**Rationale**: interval set 是 nftables 表达 CIDR 集的标准机制（google/nftables 以 `Interval: true` + 区间端点元素支持）；规则形态与现有 `sameZoneAcceptExprs` 同构（saddr 查成员 set + daddr 查路由 set），代码增量小。conntrack 规则全链一条而非每 zone 一条：established 表项只可能由某条 accept 规则创建，安全等价且规则数 O(1)。**注意**：该规则同时使现有「成员↔成员」流量的回程提前命中——行为不变（原本也放行），仅匹配路径变化，集成测试回归兜底。

**Alternatives considered**: 把合成 CIDR 塞进成员 set（类型不支持 interval，改造成 interval set 会动 005 以来的全部增量路径，风险大）；为回程做对称无状态规则 `saddr @routes daddr @zone accept`（会放行 LAN 侧主动发起，违反 FR-013 单向性）。均拒绝。

## D4 — WireGuard：PeerConfig 扩展为多前缀，统一 ReplaceAllowedIPs

**Decision**: `wg.PeerConfig` 增 `Routes []netip.Prefix`；`peerConfig()` 组装 `AllowedIPs = [node/32] + Routes`（保持 `ReplaceAllowedIPs: true`）。`AddPeer` 签名扩为接收 routes；新增 `SetPeerRoutes(publicKey, ip, routes)`（即 AddPeer 的全量替换语义，宣告增删时调用）；`ReplacePeers`（启动重建）携带每节点的合成段。

**Rationale**: wgctrl 的 `ReplaceAllowedIPs` 本就是幂等全量替换，增删宣告时按 DB 重算该 peer 全部前缀再替换，永不漂移（SQLite 单一真源，宪法 I）。

**Alternatives considered**: 增量 append AllowedIPs（wgtypes 不带 per-prefix 删除，删除时仍要全量替换，徒增两条路径）。拒绝。

## D5 — 配置：`[announce] pool` + `[limits] max_announced_subnets_per_user`

**Decision**:
- 新 TOML 段 `[announce]`，字段 `pool`（IPv4 CIDR 字符串，**无默认**：缺省/空 = 宣告功能整体停用，宣告写 API 返回 `announce_disabled`；查询 API 正常返回空清单）。Validate：非空时必须合法 IPv4 CIDR、前缀 ≤ /16 警戒（不强制）、**不得与 `wireguard.network` 重叠**（硬失败）。
- `LimitsConfig` 增 `MaxAnnouncedSubnetsPerUser *int`：nil→10、0=无限、负数报错、admin 豁免——023 三态模式逐字复刻。
- `config.toml.example` 给注释示例 `# pool = "100.100.0.0/16"`（CGNAT 段，避开默认 VPN 池 100.127/16）。

**Rationale**: 「缺省=停用」让现存部署升级后零行为变化（FR-012/021 的零值安全先例）；池与 VPN 池重叠是配置错误，启动期硬失败好过运行期怪象。

**Alternatives considered**: 给 pool 默认值（替运维做网络规划决策，撞已有网络的风险不可接受）。拒绝。

## D6 — API 面与错误码

**Decision**: 三个新端点（均 AuthRequired）：

- `POST /api/v1/zones/{name}/announcements` body `{node_id, subnet}` → 201 `{id, node_id, subnet, synthetic}`（创建宣告或复用既有合成段 + 挂接本 zone）
- `DELETE /api/v1/zones/{name}/announcements/{id}` → 204（撤销本 zone 挂接；最后挂接则整条回收）
- `GET /api/v1/zones/{name}/announcements` → 200 `{announcements: [{id, node_id, node_name, owner, subnet, synthetic}]}`（成员可见，FR-008 映射展示）

节点注册 `POST /api/v1/nodes` body 增可选 `platform`（缺省→"unknown"；校验 `^[a-z0-9-]{1,32}$`）；`GET /api/v1/nodes` 响应增 `platform`。

新错误码（并入 029 的 `knownErrorCodes` 与 openapi.yaml——**029 的一致性哨兵会强制 openapi.yaml 同步，这正是它的设计目的**）：

| code | HTTP | 场景 |
|---|---|---|
| `platform_unsupported` | 409 | 非 openwrt 节点宣告（FR-002） |
| `announce_disabled` | 503 | 池未配置（FR-003） |
| `subnet_invalid` | 400 | 非 RFC1918 / 前缀越界 / 与池重叠（FR-007，validation_error 之外单列便于客户端区分） |
| `subnet_overlap` | 409 | 与同节点现存宣告重叠（FR-007） |
| `announce_limit_reached` | 409 | 配额（FR-004，023 命名风格） |
| `synthetic_pool_exhausted` | 503 | 池耗尽（FR-006，pool_exhausted 同款语义） |

鉴权语义沿用现有：非成员操作 zone → 404 `not_found`（与 zoneMembers 一致）；他人 node → 404。

**Alternatives considered**: 顶层 `/api/v1/announcements` 资源（脱离 zone 上下文，鉴权与「成员可见」语义都要另起炉灶；挂接本质是 zone 子资源）。拒绝。

## D7 — 测试策略（宪法 II 三层）

**Decision**:
- **Unit**: ipam 块分配器表驱动（对齐/重叠/耗尽/回收复用/边界 /16 与 /30）；RFC1918 与重叠校验纯函数。
- **Integration（`unshare -rUn`，真 SQLite/nftables/WireGuard）**: store 层（迁移 0007、配额事务、复用、级联六路径）；netfw 层（interval set 增删、conntrack 规则、Rebuild 含 routes）；api 层（端点全错误矩阵、admin 豁免）；app 层（`zones_dataplane_test.go` 先例扩展：重启重建含宣告，SC-004）。
- **E2E（app 层，netns 拓扑）**: 复用 `internal/server/app` 现有真实拓扑测试模式——成员 netns 经真 wg 隧道 ping 宣告节点的合成地址，宣告节点 netns 内手工配 NETMAP+MASQUERADE 等价翻译（032 之前的模拟件，spec Assumption 已声明），断言连通 + 非成员被丢弃（SC-001）。
- **Acceptance**: quickstart.md 手工矩阵（curl 全流程 + 真机两 netns 演练）。

**Rationale**: `zones_dataplane_test.go` / `app_test.go` 已证明 netns 内真 wg+nft 端到端可测，本切片照搬模式；六条级联路径逐一入测是 SC-003 的硬要求。

## D8 — DESIGN.md / 文档同步（宪法强制，同 PR）

**Decision**: §1 非目标「不做 site-to-site」改为「支持单向子网宣告（合成段映射，宣告端限具备地址翻译能力的路由器型客户端）；LAN 侧主动连成员永久不做」；§11 增三条接受风险（platform 自报可谎报——后果限于宣告不通不越权；宣告端 MASQUERADE 抹真实源——目标 LAN 只见路由器 IP；宣告内容社工风险——zone 准入密码即信任边界）；网络模型章补合成段池说明；`config.toml.example` 增 `[announce]` 段与 limits 键。

## 解决的 NEEDS CLARIFICATION

无遗留——spec 的 Clarifications 节（grill 2026-06-11）已锁定全部产品决策；本文件 D1–D8 锁定全部技术选型。

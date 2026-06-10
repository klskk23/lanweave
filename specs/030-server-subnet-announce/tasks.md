# Tasks: 服务端子网宣告控制面（合成网段映射）

**Input**: Design documents from `/specs/030-server-subnet-announce/`

**Prerequisites**: plan.md, spec.md, research.md（D1~D8）, data-model.md, contracts/announce-endpoints.md, quickstart.md

**Tests**: 宪法 Principle II（NON-NEGOTIABLE）：本切片跨三个内核边界（SQLite/nftables/WireGuard），三层测试全配、真实例零 mock、先写先红。全量回归命令（CI 同款）：`unshare -rUn bash -c 'ip link set lo up && go test ./...'`。

**Organization**: 按 user story 分组；US1（宣告全链路）为 MVP。

## Format: `[ID] [P?] [Story] Description`

## Phase 1: Setup（共享地基：schema / 配置 / 协议）

- [X] T001 [P] 迁移 0007：新建 `internal/server/store/migrations/0007_announcements.sql`（`nodes` 加 `platform TEXT NOT NULL DEFAULT 'unknown'`；`announcements` 与 `announcement_zones` 两表，约束按 data-model.md §1）；在 `internal/server/store/store_test.go` 增迁移升级用例（0006 库升 0007，旧 node 读出 platform=unknown），先红后绿
- [X] T002 [P] 配置：`internal/server/config/config.go` 增 `AnnounceConfig{Pool string}`（`[announce]`，缺省空=停用）与 `LimitsConfig.MaxAnnouncedSubnetsPerUser *int`（nil→10/0=无限/负数报错，023 三态复刻）；Validate 增「pool 非空必须合法 IPv4 CIDR 且不与 wireguard.network 重叠（硬失败）」；`internal/server/config/config_test.go` 增三态与重叠校验用例，先红
- [X] T003 [P] 协议 DTO：`pkg/protocol/node.go` 的 `RegisterNodeRequest` 增 `Platform`（omitempty）、`NodeResponse` 增 `Platform`；新建 `pkg/protocol/announce.go`（`CreateAnnouncementRequest{NodeID,Subnet}`、`AnnouncementResponse{ID,NodeID,NodeName,Owner,Subnet,Synthetic}`、`AnnouncementListResponse`），CIDR 协议层一律文本（data-model.md §3）

---

## Phase 2: Foundational（三大子系统能力，阻塞全部 story）

**⚠️ CRITICAL**: 本阶段完成前不开任何 user story

- [X] T004 [P] ipam 块分配器：新建 `internal/server/ipam/blocks.go`——`AllocateBlock(pool netip.Prefix, prefixLen int, used []Block) (Block, error)` first-fit 自然对齐（research D1）、`Overlaps(a, b Block) bool`、`IsRFC1918(p netip.Prefix) bool`；`internal/server/ipam/blocks_test.go` 表驱动（对齐/重叠/耗尽/回收复用/边界 /16 与 /30/与池越界），先红后绿
- [X] T005 [P] netfw 路由面：`internal/server/netfw/nftables.go` 增 `zone_<id>_routes` interval set（`Interval: true`）——`AddZoneRoute(zoneID, prefix)` / `RemoveZoneRoute(zoneID, prefix)`；`AddZone`/`DeleteZone` 同步建/删第二 set 与 `saddr @zone daddr @routes accept` 规则；`Rebuild` 的 `ZoneState` 增 `RouteCIDRs []netip.Prefix` 且 forward 链头建一条 `ct state established,related accept`（research D3）；新建 `internal/server/netfw/routes_test.go` 集成测试（unshare 真 nft：interval 元素增删、规则存在性、Rebuild 含 routes、ct 规则在链头），先红后绿
- [X] T006 [P] wg 多前缀 peer：`internal/server/wg/iface.go` 的 `PeerConfig` 增 `Routes []netip.Prefix`，`peerConfig()` 组装 `[/32]+Routes`，新增 `SetPeerRoutes(publicKey string, ip netip.Addr, routes []netip.Prefix) error`（ReplaceAllowedIPs 全量替换，research D4），`ReplacePeers` 携带 routes；`internal/server/wg/peers_test.go` 增集成用例（真 wg 接口断言 allowed-ips 集合），先红后绿
- [X] T007 store platform：`internal/server/store/nodes.go` 的 Create/Get/List 读写 `platform` 列；`internal/server/api/node_handlers.go` 注册入参校验 `^[a-z0-9-]{1,32}$`（缺省→unknown，非法 400 validation_error）并在 listNodes 响应回填；`internal/server/store/nodes_test.go` 与 `internal/server/api/node_handlers_test.go` 增用例（含旧式无 platform 注册不变，FR-012），先红后绿
- [X] T008 store 宣告核心：新建 `internal/server/store/announcements.go`——单事务 `Create(userID, nodeID, real Block, zoneID, limit int, pool netip.Prefix)`（校验节点归属/zone 成员/配额（0=跳过）/同节点不叠 → 分配合成段 → INSERT 本体+首挂接；复用路径：同 (node,real) 已存在则仅添挂接）、`Detach(zoneID, annID)`（删挂接，最后一个则同事务删本体）、`ListByZone`、`ListByNode`、哨兵错误 `ErrAnnounceLimit/ErrSubnetOverlap/ErrPoolExhausted/ErrNotMember` 等；新建 `internal/server/store/announcements_test.go`（真 SQLite：配额/复用/同节点叠拒/跨节点同段允许/Detach 回收复用/并发不越界），先红后绿

**Checkpoint**: 三大子系统能力就绪且各自绿，US1/US2/US3 可开工

---

## Phase 3: User Story 1 - 成员宣告子网，同 zone 成员经合成地址访问 (Priority: P1) 🎯 MVP

**Goal**: 宣告 API 全链路：校验→分配→peer AllowedIPs→nft 放行→映射可查；netns e2e 连通

**Independent Test**: quickstart §2（curl 宣告→`wg show`/`nft list` 状态就绪→另一成员查到映射→非成员 404）+ §7 e2e

### Tests for User Story 1 (REQUIRED) ⚠️ 先写先红

- [X] T009 [P] [US1] API 集成测试：新建 `internal/server/api/announce_handlers_test.go`（api 包现有真 SQLite 装具 + fake wg/netfw seam 不可用——本切片 handler 直驱真 wg/netfw，故该测试归入 unshare 域，参照 zone_handlers_test.go 现状）：宣告 201 与 synthetic 形状、`wg show` 等价断言（经 wg.Server 查询）、nft routes set 含元素、清单含三元组、非成员/他人节点 404、platform_unsupported 409、announce_disabled 503（Options 池为空）且同状态下 **GET 清单返回空数组**（FR-003）、subnet_invalid 矩阵（非 RFC1918 / /15 / /31 / 与池叠）、`announce_limit_reached` 409（配额命中）+ **admin 同场景豁免**、`synthetic_pool_exhausted` 503（小池打满）、zone owner 可摘他人挂接而普通成员不可（FR-008）、rate-limit 适用（contracts/announce-endpoints.md 全错误矩阵，SC-006 全覆盖）
- [X] T010 [P] [US1] e2e 连通测试：在 `internal/server/app/` 新建 `announce_dataplane_test.go`，复用 zones_dataplane_test.go 的 netns 拓扑——成员 netns 经真 wg 隧道 ping 合成地址，宣告节点 netns 内手工配 NETMAP+MASQUERADE（模拟 032）断言回程通；非本 zone 成员同目标被丢弃（SC-001）；**反向新建被丢弃**：宣告 netns 以合成段内源地址主动向成员 VPN IP 发起新连接 → 无 conntrack 表项 → 落 policy drop（FR-013 单向性负断言）

### Implementation for User Story 1

- [X] T011 [US1] handler：新建 `internal/server/api/announce_handlers.go`——`createAnnouncement`（decode→platform 门禁→store.Create→wg.SetPeerRoutes→netfw.AddZoneRoute，nft 失败走补偿回滚，015 createZone 同款模式）、`deleteAnnouncement`（Detach→数据面收缩）、`listAnnouncements`；错误码映射按 contracts（6 个新码）；重复挂接幂等语义对齐 joinZone 现状
- [X] T012 [US1] 装配：`internal/server/api/routes.go` 路由表 +3 行；`api.Options` 增 `AnnouncePool netip.Prefix`（零值=停用）与 `MaxAnnouncedSubnetsPerUser int`；`internal/server/app/app.go` 解析 `cfg.Announce.Pool` 传入
- [X] T013 [P] [US1] 文档同步：`internal/server/api/docs/openapi.yaml` 增 3 端点（schema/鉴权/错误码全按 contracts）与 nodes 的 platform 字段；`internal/server/api/openapi_consistency_test.go` 的 `knownErrorCodes` += 6 个新码（029 哨兵转绿的唯一途径）
- [X] T014 [US1] 转绿闸门：T009/T010 全绿；`unshare -rUn bash -c 'ip link set lo up && go test ./...'` 全量回归绿

**Checkpoint**: MVP——curl 全流程 + e2e 连通可演示

---

## Phase 4: User Story 2 - 相同真实网段多客户并存 (Priority: P2)

**Goal**: 跨节点同段并存互不抢占；同节点叠拒；多 zone 复用——核心价值钉死

**Independent Test**: quickstart §3 四项

### Tests for User Story 2 (REQUIRED) ⚠️ 先写先红

- [X] T015 [US2] 并存矩阵集成测试：在 `internal/server/api/announce_handlers_test.go` 增——节点 A(zone X) 与 B(zone Y) 同段各得不同合成段且 A 的 peer/nft 状态前后逐字节不变（SC-002）；同 zone 两节点同段并存双映射；同节点重叠 409 `subnet_overlap`；同节点同段挂第二 zone 复用 synthetic（响应一致、announcements 行数不增）

### Implementation for User Story 2

- [X] T016 [US2] 修复 T015 暴露的缺陷直至全绿（预期 T008/T011 已覆盖语义，本任务为验证闸门；发现实现偏差时修在对应文件并补归因注释）

**Checkpoint**: 核心价值（任意重叠）有自动化保障

---

## Phase 5: User Story 3 - 生命周期级联与重启重建 (Priority: P3)

**Goal**: 六条删除路径三处零残留、合成段可复用；重启从 DB 全量重建

**Independent Test**: quickstart §5/§6

### Tests for User Story 3 (REQUIRED) ⚠️ 先写先红

- [X] T017 [P] [US3] 级联六路径集成测试：在 `internal/server/store/announcements_test.go` 与 `internal/server/api/announce_handlers_test.go` 增——①显式 DELETE 最后挂接 ②leave ③kick ④DELETE node ⑤DELETE zone ⑥admin 删用户，每条断言 DB 三表/peer AllowedIPs/nft routes set 零残留 + 合成段立即可复用（SC-003）；多 zone 挂接只删一个 → 另一 zone 完好
- [X] T018 [P] [US3] 重启重建测试：在 `internal/server/app/announce_dataplane_test.go`（或 dataplane_test.go）增——留 ≥2 条宣告（含多 zone 挂接）重启 App，断言 peer AllowedIPs 与 nft 状态与重启前一致（SC-004，FR-011）

### Implementation for User Story 3

- [X] T019 [US3] 级联实现：`internal/server/store/zones.go`（Leave/Kick/DeleteZone 同事务清挂接并回收孤儿宣告）、`internal/server/store/users.go`（删用户级联，008 扩展）、`internal/server/api/node_handlers.go` deleteNode 与 `internal/server/api/zone_handlers.go` 相应 handler 的数据面收缩（重算 peer routes + 删 nft 元素）
- [X] T020 [US3] 重建实现：`internal/server/app/dataplane.go` 启动重建——`ZoneState.RouteCIDRs` 从 announcement_zones 装载、`ReplacePeers` 携带各节点合成段；T017/T018 全绿

**Checkpoint**: 三个 story 全部独立可验

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T021 [P] DESIGN.md 修订（FR-014，宪法 D8）：§1 非目标改「支持单向子网宣告（合成段映射）；LAN 侧主动连成员永久不做」；§3 网络模型补合成段池与 ct 回程规则说明；§11 增三条接受风险（platform 自报 / MASQUERADE 抹源 / 宣告社工面）
- [X] T022 [P] `config.toml.example` 增 `[announce]` 段（注释示例 `# pool = "100.100.0.0/16"`，说明缺省=停用、CGNAT 选段建议）与 `max_announced_subnets_per_user` 注释键
- [X] T023 lint 门禁：`gofmt -l .`、`go vet ./...`、`staticcheck ./...` 全清；`unshare -rUn bash -c 'ip link set lo up && go test ./...'` 全绿（FR-012/SC-005 回归）
- [ ] T024 quickstart.md 人工矩阵逐条执行（§1–§6、§8；§7 由 T010 自动化承载），结果记录回 quickstart.md
  > 部分完成（见 quickstart「执行记录」）：§1–§7 全部由自动化测试承载并全绿；剩余真机双客户机复刻与 systemd 重启冒烟两项需真实环境

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (P1)**: T001 ∥ T002 ∥ T003 互不依赖
- **Foundational (P2)**: T004 ∥ T005 ∥ T006 互不依赖；T007 依赖 T001+T003；T008 依赖 T001+T004
- **US1 (P3)**: T009 依赖 T003（DTO 编译）；T010 依赖 T005/T006；T011 依赖 T007+T008+T009 红；T012 依赖 T002+T011；T013 可与 T011 并行起（contracts 即规约）；T014 收尾
- **US2 (P4)**: T015 依赖 T014（在已绿基线上加压）；T016 收尾
- **US3 (P5)**: T017 ∥ T018 可并行写（依赖 T014）；T019 依赖 T017 红；T020 依赖 T018 红
- **Polish (P6)**: T021 ∥ T022 随时；T023/T024 必须最后

### Parallel Opportunities

```text
Setup:        T001 ∥ T002 ∥ T003
Foundational: T004 ∥ T005 ∥ T006（三大子系统三个包，零交集）
US1:          T009 ∥ T010 ∥ T013
US3 测试:     T017 ∥ T018
Polish:       T021 ∥ T022
```

### Within Each Story

- 测试任务（T009/T010/T015/T017/T018）先写、确认红，再做实现
- T014/T016/T020 是各 story 的转绿闸门；全量回归命令必须带 `ip link set lo up`

---

## Implementation Strategy

**MVP = Phase 1→3（T001–T014）**：宣告全链路 + e2e 连通，curl 即可演示核心能力。
**增量 2 = Phase 4（T015–T016）**：把「任意重叠」这一核心价值钉进 CI。
**增量 3 = Phase 5（T017–T020）**：级联与重建——安全性收口（幽灵路由零容忍）。
**收尾 = Phase 6**：宪法强制的 DESIGN.md 同步 + 门禁 + 人工矩阵。

单人串行按 T001→T024；每完成一个逻辑组提交一次。

---

## Notes

- 客户端目录（`internal/client`、`cmd/lanweave-client`）零触碰；031/032/033 另行切片。
- T008（store 事务核心）与 T011（handler 编排+补偿）是体量与风险中心：补偿回滚必须复用 015 createZone 的「建后任一步失败→级联清理」模式，禁止引入新事务形态。
- T013 不做则 029 的 openapi 一致性哨兵必红——文档同步是闸门内任务，不是 Polish。
- e2e（T010）的 NETMAP/MASQUERADE 是测试装置（模拟 032 的 OpenWrt 端），放测试文件内并注释指明 032 接管后的对应关系。

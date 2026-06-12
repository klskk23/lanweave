# Tasks: 消费端动态路由（宣告子网对全平台成员可达）

**Input**: Design documents from `/specs/033-client-dynamic-routes/`

**Prerequisites**: plan.md, spec.md, research.md（D1~D7）, data-model.md, contracts/consumer-routes.md, quickstart.md

**Tests**: 宪法 Principle II：wireguard-go/wgctrl/netlink 真实例；两端真流量集成；先红后绿；netsh 实机豁免（逻辑层 linux 承载）。全量回归（CI 同款）：`unshare -rUn bash -c 'ip link set lo up && go test ./...'`。

**Organization**: 按 user story 分组；US1（Windows 消费端）为 MVP。

## Format: `[ID] [P?] [Story] Description`

## Phase 1: Setup（无独立 setup——零新依赖零服务端改动，直接进 Foundational）

*(本切片无 Phase 1 任务；编号从 Foundational 起)*

---

## Phase 2: Foundational（聚合双视图，阻塞两个 story）

**⚠️ CRITICAL**: 两端共用的数据底座，先行

- [X] T001 聚合双视图：新建 `internal/client/routesync/routesync.go`——`Entry{ID int64; Subnet, Synthetic netip.Prefix; Zones []string; Announcer string}`；`Fetch(zones []protocol.ZoneResponse, list func(string) (protocol.AnnouncementListResponse, error)) ([]Entry, error)`（聚合所在 zones 全部宣告、按宣告 id 去重、zones 排序——032 reconcile.Desired 的超集）；`Mine(entries, nodeID) []Entry` 与 `Consumed(entries, nodeID) []Entry`（消费者视图：node_id≠自己，按合成段去重）；`Prefixes(entries) []netip.Prefix`。新建 `internal/client/routesync/routesync_test.go` 表驱动（多 zone 去重/双视图划分/合成段去重/空集/坏 CIDR 报错），先红后绿
- [X] T002 reconcile 委托：改 `internal/router/reconcile/reconcile.go`——`Desired` 内部委托 `routesync.Fetch`+`Mine`（行为不变，删除重复聚合逻辑）；`internal/router/reconcile/reconcile_test.go` 原测试零改动全绿（委托等价性证明）；032 全部测试零回归

**Checkpoint**: 单一聚合真源就位，两端可共用

---

## Phase 3: User Story 1 - Windows 成员拨通伙伴的局域网 (Priority: P1) 🎯 MVP

**Goal**: tunnel 热更新（不断流）+ panel 60s 对账 + 「可达子网」区块；新增/撤回 ≤60s 生效、断开零残留

**Independent Test**: quickstart §4 tunnel 集成维度（§1 实机留人工）

### Tests for User Story 1 (REQUIRED) ⚠️ 先写先红

- [X] T003 [US1] tunnel 热更新集成测试：改 `internal/client/tunnel/tunnel_integration_test.go`（wireguard-go netns 既有装具）——连接后 `SetExtraRoutes([合成段])`：①UAPI（`dev.IpcGet`）server peer allowed_ips = VPN 网段∪合成段；②系统路由就位（netlink 断言）；③**热更新不致重握手**（IpcSet 前后 last_handshake 不回零/连接不中断——以更新前后 Transfer 连续与 handshake age 单调为判据）；④**真流量**：netns 对端 peer 挂合成地址 echo，经隧道往返通（SC-001 CI 维度）；⑤撤除（SetExtraRoutes 子集→消失前缀的路由删除、allowed_ips 收缩）；⑥幂等（同集合再调→零系统调用副作用，以路由表无变化判定）；⑦单条冲突跳过（预配重叠地址→该条跳过+其余生效+返回的 applied 集合不含它，FR-005）；⑧断开→路由零残留（接口消失判定）
- [X] T004 [P] [US1] UAPI 增量文本与差分纯函数单测：改 `internal/client/tunnel/profile_test.go`——`BuildPeerUpdate(serverPub, network, extras)` 文本形状（public_key/replace_allowed_ips=true/allowed_ip 序）；路由差分纯函数（add/del 集合计算）表驱动，先红后绿

### Implementation for User Story 1

- [X] T005 [US1] tunnel.SetExtraRoutes：改 `internal/client/tunnel/tunnel.go`——`SetExtraRoutes(extra []netip.Prefix) ([]netip.Prefix, error)`（返回实际应用集）：集合与当前记忆相等→幂等返回；`BuildPeerUpdate` 增量 UAPI → `dev.IpcSet`；路由差分逐条 add/del（单条失败跳过记内部状态，FR-005）；断开/teardown 清记忆。`internal/client/tunnel/profile.go` 增 `BuildPeerUpdate`；`addr_linux.go`/`addr_windows.go` 增 `addRoute/delRoute`（netlink RouteReplace+RouteDel / netsh add+delete route，既有模式）
- [X] T006 [US1] panel 对账驱动 + 展示：改 `internal/client/panel/`——控制器连接成功即一次 + 连接期每 60s：`ListZones`→`routesync.Fetch`→`Consumed`→`tunnel.SetExtraRoutes(Prefixes(...))`（API 失败跳过本轮记状态，FR-003）；断开即停；面板「可达子网」区块（替身/真实/zone(s)/宣告者，冲突条目标注，刷新失败提示）+ zh/en i18n；既有 panel 单测扩展（假 API/假 tunnel 驱动：刷新触发/冻结/区块数据/冲突标注/**第二轮返回缩集→tunnel 收到缩集**（SC-001 撤回收敛的 panel 维度）），先红后绿
- [X] T007 [US1] 转绿闸门：T003/T004 + panel 测试全绿；`unshare -rUn bash -c 'ip link set lo up && go test ./...'` 全量零回归（Windows 既有连接/断开/自愈行为、服务端、031/032）

**Checkpoint**: MVP——CI 内 wireguard-go 全链路热更新 + 真流量可证；实机 §1 留人工

---

## Phase 4: User Story 2 - OpenWrt 路由器作为消费者 (Priority: P2)

**Goal**: engine.SetRoutes + 032 对账循环双路消费（排除自己）+ `announce list --all`

**Independent Test**: quickstart §4 routerd e2e 维度（§2 实机留人工）

### Tests for User Story 2 (REQUIRED) ⚠️ 先写先红

- [X] T008 [US2] 消费端 e2e：改 `cmd/lanweave-routerd/announce_test.go`（032 装具演进，D6 拓扑）——「宣告者」=memberNS 手工 peer：经 API 注册节点（**必须 `RegisterNodePlatform(..., "openwrt")`——030 平台门禁，普通 RegisterNode 会被 platform_unsupported 拒**）+入 zone+`CreateAnnouncement`（直接 API），其接口直挂合成段地址 + echo（NETMAP 端由 032 已证，此处全真验证消费链路）。断言：daemon 运行 ≤1 对账周期（短周期参数）后 root 路由器访问合成地址 echo 往返通（**SC-002**）；`ip route`/wgctrl 断言合成段在路由与 server peer AllowedIPs 中；**排除自己**：root 自己也宣告一条（032 流程）→ 其合成段不出现在隧道路由/AllowedIPs；宣告者撤回 → ≤1 周期路径移除+访问超时；`announce list --all` 输出 MINE yes/no 两行（ANNOUNCER 列）；logout/Down 后路由零残留
- [X] T009 [P] [US2] engine.SetRoutes 集成测试：改 `internal/router/engine/engine_test.go`——Up 后 `SetRoutes([s1,s2])`：AllowedIPs=Network∪s1∪s2 + 内核路由就位；`SetRoutes([s1])`→s2 路由与 AllowedIPs 收缩；同集合幂等；单条冲突跳过返回 applied；Down 后零残留，先红后绿

### Implementation for User Story 2

- [X] T010 [US2] engine.SetRoutes：改 `internal/router/engine/engine.go`——`SetRoutes(extra []netip.Prefix) ([]netip.Prefix, error)`：wgctrl ConfigureDevice（server peer，ReplaceAllowedIPs，Network∪extra）+ 路由差分（RouteReplace/RouteDel，单条容错）+ 集合记忆/幂等；Down 清记忆
- [X] T011 [US2] routerd 对账双路 + list --all：改 `cmd/lanweave-routerd/main.go`——对账循环改用 `routesync.Fetch` 一次取数：`Mine`→NAT 重建（032 现状）、`Consumed`→`engine.SetRoutes`（daemon 持 engine 引用——cmdRunCtx 已有 eng，传入对账闭包）；tryUp 成功后触发即时对账（plan 登记项，侵入小则做）。改 `cmd/lanweave-routerd/announce.go`——`announce list --all`（MINE/SUBNET/SYNTHETIC/ZONES/ANNOUNCER 列；默认仍仅自己的，行为不回归）；T008/T009 全绿

**Checkpoint**: 两个 story 独立可验；跨端可达集合一致由共用 routesync 机械保证

---

## Phase 5: Polish & Cross-Cutting Concerns

- [X] T012 [P] 文档：DESIGN.md §9 OpenWrt/Windows 客户端段各补消费端动态路由一句（IpcSet 热更新/SetRoutes、60s 对账、排除自己）；docs/ROADMAP.md 增 033 行
- [X] T013 lint 门禁：`gofmt -l .`、`go vet ./...`、`staticcheck ./...` 全清；全量套件 ×2（资源隔离纪律复验——031/032 教训）
- [X] T014 quickstart 记录：§4 执行勾选 + 「执行记录」；§1–§3 实机部分登记人工遗留并注明 **SC-005 通过后人工勾销 TODO.md「设备路由宣告到区域」**
  > 部分完成（见 quickstart「执行记录」）：§4 已执行，§1–§3 由 CI 自动化等价承载；实机部分（SC-005 功能链收口 + TODO 勾销）待真设备人工执行

---

## Dependencies & Execution Order

### Phase Dependencies

- **Foundational**: T001 → T002（委托依赖 routesync 存在）
- **US1**: T003 依赖 T001；T004 ∥ T003（不同文件）；T005 依赖 T003/T004 红；T006 依赖 T005；T007 收尾
- **US2**: T008 依赖 T007（同装具文件串行更稳）；T009 ∥ T008 可（不同文件）；T010 依赖 T009 红；T011 依赖 T008 红 + T010；US2 整体可与 US1 并行开发（不同文件域），但按优先级串行执行更稳
- **Polish**: T012 随时 [P]；T013/T014 最后

### Parallel Opportunities

```text
US1 内:  T003 ∥ T004（tunnel_integration_test vs profile_test）
US2 内:  T008 ∥ T009（announce_test vs engine_test）
Polish:  T012 与 T013 并行起步
```

### Within Each Story

- T003/T004/T008/T009 先写先红；T007/T011 转绿闸门
- 全量回归命令必须带 `ip link set lo up`
- **共享 ns 纪律（031/032 教训）**：新测试资源（合成段地址、echo 端口、路由前缀）一律私有命名/私有网段；tunnel 集成测试在其既有 netns 装具内自隔离

---

## Implementation Strategy

**MVP = Phase 2→3（T001–T007）**：Windows 路径 CI 全链路（热更新真流量）。
**增量 2 = Phase 4（T008–T011）**：OpenWrt 消费端 + list --all。
**收尾 = Phase 5**：文档/门禁/quickstart；TODO 勾销留 SC-005 真机。

---

## Notes

- 服务端零触碰（FR-007 是硬约束，出现任何服务端 diff 即违规）。
- routesync 落 `internal/client/` 树（routerd 已依赖 client 系包，无层级问题）；reconcile 委托后删除重复逻辑（非复制）。
- T003 的「不重握手」判据用可观测量（handshake age 单调 + Transfer 连续），不依赖实现内部。
- 第 4 份服务端测试装具的 testutil 提取已登记（032 评审）——本切片 e2e 是 032 文件演进而非新装具，不再加重；若实现期自然顺手则提取，不强制。

# Tasks: OpenWrt 宣告端（宣告 CLI + 地址翻译下发）

**Input**: Design documents from `/specs/032-openwrt-announcer/`

**Prerequisites**: plan.md, spec.md, research.md（D1~D8）, data-model.md, contracts/announce-cli.md, quickstart.md

**Tests**: 宪法 Principle II：nftables 内核边界→真实例集成（D1 spike 为闸门任务）；三命名空间 e2e 真流量；补偿路径经 env 注入 seam（自有边界）。先写先红。全量回归（CI 同款）：`unshare -rUn bash -c 'ip link set lo up && go test ./...'`。

**Organization**: 按 user story 分组；US1（宣告全链路）为 MVP。

## Format: `[ID] [P?] [Story] Description`

## Phase 1: Setup（apiclient 触点）

- [X] T001 [P] apiclient 宣告三方法：`internal/client/apiclient/client.go` 增 `CreateAnnouncement(zone string, nodeID int64, subnet string) (protocol.AnnouncementResponse, error)`、`DeleteAnnouncement(zone string, id int64) error`、`ListAnnouncements(zone string) (protocol.AnnouncementListResponse, error)`（030 端点）；6 个新 typed errors（`ErrPlatformUnsupported/ErrAnnounceDisabled/ErrSubnetInvalid/ErrSubnetOverlap/ErrAnnounceLimit/ErrSyntheticPoolExhausted`）按错误码映射；`internal/client/apiclient/client_test.go` 增映射用例（httptest 假端点逐码断言），先红后绿

---

## Phase 2: Foundational（natctl 与期望集，阻塞全部 story）

**⚠️ CRITICAL**: 本阶段完成前不开任何 user story；T002 的 spike 是 D1 技术闸门

- [X] T002 [P] natctl（含 D1 spike）：新建 `internal/router/natctl/natctl.go`——`Rule{Synthetic, Real netip.Prefix}`；`Rebuild(table string, vpnPool netip.Prefix, rules []Rule) error`（删旧表→建 `inet <table>`→prerouting(dstnat) 每条 `ip daddr <Synthetic> dnat prefix to <Real 基址>`（`expr.NAT{Prefix: true}`，research D1）+ postrouting(srcnat) 每条 `ip saddr <vpnPool> ip daddr <Real> masquerade`→原子 Flush）；`Teardown(table) error`（幂等删表）、`Current(table) ([]Rule, error)`（读回现行规则集，供 list 的 RULES=ok/pending 判定与对账比对）。新建 `internal/router/natctl/natctl_test.go` 集成测试（unshare 真内核，唯一表名）：**spike——dnat prefix 规则可下发且 `GetRules` 读回 NAT 表达式含 PREFIX flag**；Rebuild 幂等、规则数=期望、清多余补缺失、Teardown 幂等，先红后绿
- [X] T003 [P] 期望集计算：新建 `internal/router/reconcile/reconcile.go`——`Desired(zones []protocol.ZoneResponse, listFn func(zone string) (protocol.AnnouncementListResponse, error), nodeID int64) ([]natctl.Rule, []Entry, error)`（聚合各 zone 清单→过滤 node_id==本机→按宣告 id 去重→解析 CIDR；Entry 含 ID/Subnet/Synthetic/Zones 供 list 展示）；`internal/router/reconcile/reconcile_test.go` 纯函数表驱动（多 zone 去重/他人节点过滤/坏 CIDR 容错/空集），先红后绿

**Checkpoint**: spike 通过 = D1 路线确认；期望集与 NAT 重建各自绿

---

## Phase 3: User Story 1 - 把家里的局域网宣告进 zone (Priority: P1) 🎯 MVP

**Goal**: `announce add/list` 全链路：远端创建→本地冲突检测→规则下发→成员经替身地址往返 LAN；重启/对账重建

**Independent Test**: quickstart §1 + §3（CI：三命名空间 e2e）

### Tests for User Story 1 (REQUIRED) ⚠️ 先写先红

- [X] T004 [US1] 三 ns e2e（031 装具演进）：新建 `cmd/lanweave-routerd/announce_test.go`——serverNS（newTestServer 整体经 inNS 构建于子 ns，veth 通 routerNS 与 memberNS；wg 端口/HTTP 经 veth 地址暴露）、routerNS（**每次 cli()/daemon goroutine 以 LockOSThread+setns 钉扎执行**，research D7；lan0 dummy 挂 192.168.50.1/24 与 192.168.50.50/32 模拟零配置 LAN 主机）、memberNS（030 newPeerNS 同款，AllowedIPs 含合成池+路由）。断言：`announce add 192.168.50.0/24 --zone homelab` 输出映射且服务端清单含该条；成员 UDP echo 替身 .50 往返通（**SC-001**）；`nft list`（routerNS）两条规则就位；`announce list` 表格含 SUBNET/SYNTHETIC/ZONES/RULES=ok；同子网 add 第二 zone 复用合成段；daemon 重启（cancel→重起 runDaemon+对账）后可达性恢复（SC-002 前半）；**单向性反向负断言（FR-009）**：routerNS 内以 LAN 地址（192.168.50.50）为源主动向成员 VPN IP 发起新连接 → 超时（本机无反向 DNAT 路径 + 服务端 ct 单向双保险）
- [X] T005 [P] [US1] 失败矩阵 + FR-008：在 `announce_test.go` 增——030 六类拒绝逐一经 CLI 触发（windows 节点宣告→platform 文案、自身重叠、配额（服务端 limit=1 Options）、池耗尽（小池）、未入 zone→not found、池停用→disabled 文案），全部非零退出且 routerNS `nft` 零残留（SC-003）；FR-008：routerNS 预配合成池内地址→add→失败指明冲突且服务端清单无挂接（补偿生效）

### Implementation for User Story 1

- [X] T006 [US1] announce 命令族（add/list）：`cmd/lanweave-routerd/main.go`——`cmdAnnounce` 分发；`add <subnet> --zone Z`：state.NodeID 前置校验→`CreateAnnouncement`→合成段本地冲突检测（netlink AddrList 全接口重叠判定，FR-008）→冲突或后续失败则 `DeleteAnnouncement` 补偿（FR-005）→对账式本地重建（reconcile.Desired→natctl.Rebuild）→输出映射 + 非直连子网 stderr 提示 + 隧道接口不存在时 stderr 提示「成员暂不可达（daemon 未运行）」（spec 边界用例）；`list`：Desired 的 Entry 渲染表格（RULES 列：本地表与期望一致=ok 否则 pending，FR-011）；六 typed errors→人类可读（contracts 全表）；`env` 增 natctl 接口字段（默认真实现，测试可换失败件——SC-004 seam）
- [X] T007 [US1] 对账循环：`cmd/lanweave-routerd/main.go` 的 `cmdRunCtx`——daemon 启动后并行 goroutine：立即对账一次 + 每 60s 一次（`reconcile.Desired`→`natctl.Rebuild`；API 失败记日志跳过本轮，规则冻结——D4）；ctx 取消即停（表保留，logout 才删）
- [X] T008 [US1] 转绿闸门：T004/T005 全绿；`unshare -rUn bash -c 'ip link set lo up && go test ./...'` 全量回归绿（031/030/Windows 零回归）

**Checkpoint**: MVP——三 ns 内成员经替身地址往返 LAN 全自动化可证

---

## Phase 4: User Story 2 - 撤回宣告与生命周期收口 (Priority: P2)

**Goal**: remove/部分撤回/第三方移除收敛/logout 清场

**Independent Test**: quickstart §2/§3 后半/§5

### Tests for User Story 2 (REQUIRED) ⚠️ 先写先红

- [X] T009 [US2] 生命周期 e2e：在 `announce_test.go` 增——`announce remove`（最后挂接）后成员访问超时 + 本地规则消失；双 zone 挂接仅撤一个→另一 zone 成员照常 + 规则保留；**第三方移除收敛**：直接调服务端 API（zone owner 摘除）→ 触发一轮对账（测试用短周期或手动触发函数）→ 本地规则消失（SC-002 后半）；**补偿路径（SC-004）**：env 换失败 natctl→add→命令失败且服务端清单无该挂接；logout 后 `inet lanweave_rt` 表不存在

### Implementation for User Story 2

- [X] T010 [US2] remove + logout 扩展：`cmd/lanweave-routerd/main.go`——`announce remove <subnet> --zone Z`（ListAnnouncements 解析 id→DeleteAnnouncement→本地重建；未宣告→not found 非零退出）；`cmdLogout`（含 --force）在拆隧道同场调 `natctl.Teardown`（FR-006/契约扩展）；对账周期参数化（测试可缩短）；T009 全绿

**Checkpoint**: 两个 story 独立可验，生命周期闭合

---

## Phase 5: Polish & Cross-Cutting Concerns

- [X] T011 [P] 文档：`packaging/openwrt/README.md` 增 announce add/remove/list 示例段（对应 quickstart §1/§2）；DESIGN.md §9 OpenWrt 客户端段补宣告端职责一句（翻译/伪装为派生态、服务器清单为真源——research D8）
- [X] T012 lint 门禁：`gofmt -l .`、`go vet ./...`、`staticcheck ./...` 全清；`unshare -rUn bash -c 'ip link set lo up && go test ./...'` 全绿 ×2（含与 tunnel/030 包并行的资源隔离复验——031 教训固化）
- [X] T013 quickstart 矩阵：§7 本地执行勾选并写「执行记录」；§1–§6 实机部分（真 OpenWrt + fw4 + 真实第二客户端访问 NAS）登记为人工遗留（SC-006）
  > 部分完成（见 quickstart「执行记录」）：§7 已执行，§1–§5 由 CI 自动化等价承载；§1–§6 实机部分（真 OpenWrt/fw4/真实客户端）待人工执行

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup**: T001 独立
- **Foundational**: T002 ∥ T003（不同包；T003 编译依赖 natctl.Rule 类型——先定义类型再并行，或 T003 自带类型后对齐，实现时按 T002 先行半步处理）
- **US1**: T004 依赖 T001–T003（编译面）；T005 可与 T004 同文件接续；T006 依赖 T004/T005 红；T007 依赖 T006；T008 收尾
- **US2**: T009 依赖 T008；T010 依赖 T009 红
- **Polish**: T011 随时 [P]；T012/T013 最后

### Parallel Opportunities

```text
Foundational: T002 ∥ T003（类型先行半步）
US1 测试:     T004 与 T005 同文件，串行写更稳
Polish:       T011 与 T012 可并行起步
```

### Within Each Story

- T004/T005/T009 先写先红；T008/T010 为转绿闸门；T002 的 spike 是 D1 技术闸门（失败即按 research 回退 exec-nft 并修订 plan）
- 全量回归命令必须带 `ip link set lo up`

---

## Implementation Strategy

**MVP = Phase 1→3（T001–T008）**：三 ns 内「宣告→成员经替身地址访问 LAN→重建恢复」全自动化。
**增量 2 = Phase 4（T009–T010）**：撤回/收敛/logout 清场。
**收尾 = Phase 5**：文档/门禁/实机登记。

---

## Notes

- 服务端与 Windows 客户端零触碰；apiclient 三方法纯加法（033 复用）。
- **共享 ns 纪律（031 教训）**：本切片所有新测试资源（表名、接口、网段、端口）一律私有化或 ns 隔离；T012 显式包含与 tunnel/030 包的并行复验。
- 三 ns 钉扎装具是 033 的直接复用对象——若实现中自然成形为可提取形态，在 PR 中登记（不强制本期提取）。
- T006 的 RULES=ok/pending 判定：读回本地表规则集与期望集比对（natctl 暴露 `Current(table)` 读取函数，归入 T002 范围）。

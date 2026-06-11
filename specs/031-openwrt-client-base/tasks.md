# Tasks: OpenWrt 客户端基础（无头 daemon + CLI）

**Input**: Design documents from `/specs/031-openwrt-client-base/`

**Prerequisites**: plan.md, spec.md, research.md（D1~D8）, data-model.md, contracts/cli-commands.md, quickstart.md

**Tests**: 宪法 Principle II：跨内核边界（wg/netlink）→ 真实例集成测试（netns，030 装具模式）；daemon 健康循环用假引擎单测（028 先例，自有 seam 非系统 mock）；测试先写先红。全量回归（CI 同款）：`unshare -rUn bash -c 'ip link set lo up && go test ./...'`。

**Organization**: 按 user story 分组；US1（接入+隧道）为 MVP。

## Format: `[ID] [P?] [Story] Description`

## Phase 1: Setup（既有包三处向后兼容加法）

- [X] T001 [P] state 扩展：`internal/client/state/state.go` 的 `Record` 增 `NodeID int64`，`SchemaVersion` 升 3（loader 接受 ≤3，v2 记录 NodeID 取零值）；`internal/client/state/state_test.go` 增 v2→v3 兼容用例（载入 v2 JSON → NodeID==0、其余字段完好）与「并发 Save 不损坏状态」用例（FR-014；Save 现状已是 temp+rename 原子写，state.go:78——补并发断言或确认既有覆盖后注记），先红后绿
- [X] T002 [P] keyring 注入构造器：`internal/client/keyring/store_other.go` 增 `OpenAt(dir string) (Store, error)`（目录 0700、文件 0600，复用 fileStore）；`internal/client/keyring/` 测试增 OpenAt 用例（权限断言）
- [X] T003 [P] 协议触点：`internal/client/apiclient/client.go` 增 `RegisterNodePlatform(name, pubKey, platform string)`（现有 `RegisterNode` 委托传空——Windows 行为零变化）；`internal/client/onboard/onboard.go` 的 `Provisioner` 增 `Platform string` 字段（空=不传），Provision 成功时把返回的 node ID 写入 `state.Record.NodeID`；`internal/client/apiclient/client_test.go` 与 `internal/client/onboard/onboard_test.go` 增用例（platform 透传、NodeID 落盘、旧路径零回归），先红后绿

---

## Phase 2: Foundational（两个新包，阻塞全部 story）

**⚠️ CRITICAL**: 本阶段完成前不开任何 user story

- [X] T004 [P] 隧道引擎：新建 `internal/router/engine/engine.go`——`Config{Iface, PrivateKey, Address(netip.Addr), Network(netip.Prefix), ServerPubKey, Endpoint, Keepalive}`；`Up()`（建内核 wg 接口+地址+VPN 网段路由+server peer，接口已存在则报错不覆盖）、`Down()`（幂等拆除）、`LastHandshake() (time.Time, error)`、`Connected(threshold) bool`；参考实现=030 e2e `newPeerNS`（注释指明）。新建 `internal/router/engine/engine_test.go` 集成测试（unshare 真内核：Up 后接口/地址/路由/peer/keepalive 断言、重复 Up 报错、Down 幂等），先红后绿
- [X] T005 [P] daemon 健康循环：新建 `internal/router/daemon/daemon.go`——engine 经接口注入；`Run(ctx)`：起隧道 → 每 15s 查最后握手 → 已连接但陈旧 >240s 自动 Down/Up 重建 → 失败每 15s 重试到底不退出 → ctx 取消优雅 Down（028 语义，常量注释互指 `internal/client/tunnel`）；refresh token 续期失败时停止重试并记录「需重新登录」日志（FR-006 后半，按日志承载）。新建 `internal/router/daemon/daemon_test.go` 假引擎单测（陈旧触发重建、重试不退出、取消即拆、单飞不并发重建），先红后绿

**Checkpoint**: 引擎与守护循环各自绿，US1/US2/US3 可开工

---

## Phase 3: User Story 1 - 路由器接入：onboard 并建立隧道 (Priority: P1) 🎯 MVP

**Goal**: CLI 三步 onboard（凭据三件套一致落盘、platform=openwrt）→ `run` 起隧道 → ping 通服务器；重启自愈；TOFU/续期语义对齐

**Independent Test**: quickstart §1 + §3（CI：netns 双 ns 全链路）

### Tests for User Story 1 (REQUIRED) ⚠️ 先写先红

- [X] T006 [US1] 全链路集成测试：新建 `internal/router/router_integration_test.go`（或 cmd 内同包测试）——netns 拓扑（服务端栈进专属 ns，030 e2e 装具模式；路由器侧第二 ns）：调用 CLI 入口函数依次 setup/login/register → 断言三件套落盘且权限 0600/0700、服务端节点 platform=openwrt、state.NodeID 非零 → daemon Run → 路由器 ns 内 ping 服务器 VPN 地址通（SC-001）→ 杀循环重起 → 隧道恢复（SC-002）→ 重复 register → 报错提示先 logout；TOFU：自签服务端首连返回指纹错误 → trust 后通、证书更换 → 拒绝（018 语义）；**零机密断言**：捕获全流程 stdout/stderr 与日志，断言不含测试密码、设备私钥、access/refresh token 字符串（FR-003 + 宪法 Security「Tests assert this」）

### Implementation for User Story 1

- [X] T007 [P] [US1] CLI 骨架：新建 `cmd/lanweave-routerd/main.go`——标准库 flag 子命令分发（contracts/cli-commands.md 全清单）、全局 `--data-dir`（默认 `/etc/lanweave`）、退出码约定、stderr 单行错误格式；入口函数可被测试直接调用（`run(args []string, stdin io.Reader, stdout, stderr io.Writer) int`）；新建分发表驱动单测（未知命令/缺 flag/帮助输出）
- [X] T008 [US1] onboard 族命令：`setup`（写 ServerURL，`--pin` 预置指纹）、`login`/`register-account`（密码 stdin，token 落盘）、`register`（wgkey 生成 + onboard.Provision 复用，Platform="openwrt"，写 NodeID；已 onboard 再 register → 报错）、`trust <fp>`（持久化指纹）、`--insecure` 全局逃生 flag（仅当次进程不持久化，018 语义）；错误映射 apiclient typed errors → 人类可读 + 非零退出（SC-006）
- [X] T009 [US1] 隧道族命令：`run`（装配 state+keyring → engine → daemon.Run，前台，SIGTERM 优雅）、`down`（幂等拆接口）、`status` 的 daemon/tunnel/ip/last_handshake 四字段（zones 字段 US2 补）；接口名固定 `lanweave0`
- [X] T010 [US1] 转绿闸门：T006 全绿；`unshare -rUn bash -c 'ip link set lo up && go test ./...'` 全量回归绿（含 Windows 路径零回归，FR-013）

**Checkpoint**: MVP——netns 内从零到 ping 通全自动化可证

---

## Phase 4: User Story 2 - zone 管理与状态查看 (Priority: P2)

**Goal**: zone 全生命周期 CLI + status 五字段齐全；同 zone 互通

**Independent Test**: quickstart §2（CI：netns 第二节点互 ping）

### Tests for User Story 2 (REQUIRED) ⚠️ 先写先红

- [X] T011 [US2] zone 集成测试：在 `internal/router/router_integration_test.go` 增——`zone create`（自动入组，015 语义）、第二节点（直接 API 模拟）join 后 `zone members` 见双方名称/IP/归属、`zone join` 错误密码 → 「zone 或密码无效」、`zone leave` 后列表消失、`status` 五字段（含 zones）可 grep（SC-003 前半）；netns 拓扑下同 zone 两节点互 ping 通（SC-003 后半）

### Implementation for User Story 2

- [X] T012 [US2] zone 族命令：`cmd/lanweave-routerd/main.go`（或拆 `zones.go`）——create（密码 stdin，传 state.NodeID 自动入组）/join/leave/list/members，复用 apiclient 现有五方法；输出表格化人类可读；`status` 补 zones 字段（ListZones 拉取，API 不可达时该字段显示 unavailable 不阻塞其余字段）
- [X] T013 [US2] 转绿闸门：T011 全绿

**Checkpoint**: zone 操作 100% CLI 化（无需 curl）

---

## Phase 5: User Story 3 - 退出登录与清盘 (Priority: P3)

**Goal**: 025 语义三态登出：远端注销+本地清盘 / 不可达阻止 / --force 逃生

**Independent Test**: quickstart §5

### Tests for User Story 3 (REQUIRED) ⚠️ 先写先红

- [X] T014 [US3] 登出集成测试：在 `internal/router/router_integration_test.go` 增——可达：logout 后服务端 node 消失（按 state.NodeID 删）、RT 吊销（旧 RT refresh 失败）、本地三件套清空、接口拆除（SC-004）；不可达（关掉测试服务端）：非零退出 + 本地完好；`--force`：仅清本地 + stderr 警告

### Implementation for User Story 3

- [X] T015 [US3] logout 命令：`cmd/lanweave-routerd/main.go`——按 state.NodeID 调 DeleteNode + apiclient.Logout（吊销 RT），有限重试（025 同款 3×1s）；成功 → 清 state+keys+拆接口；失败 → 阻止并提示 `--force`；`--force` 跳过远端、清本地、警告；T014 全绿

**Checkpoint**: 三个 story 全部独立可验

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T016 [P] procd 与文档：新建 `packaging/openwrt/lanweave-routerd.init`（USE_PROCD=1、respawn、stop→SIGTERM+兜底 down、START=95）与 `packaging/openwrt/README.md`（安装三步：拷二进制→onboard→enable，对应 quickstart §1/§4）
- [X] T017 [P] 交叉编译：`Makefile` 增 `routerd`（本机）与 `routerd-cross`（`CGO_ENABLED=0 GOOS=linux GOARCH=amd64|arm64|mipsle GOMIPS=softfloat` → `dist/lanweave-routerd-<arch>`）；本地跑通三目标（FR-012/SC-005）
- [X] T018 [P] DESIGN.md 修订（宪法 D8）：§2/客户端清单补「OpenWrt 无头客户端（daemon+CLI，内核 WireGuard）」；§11 增「路由器凭据明文 0600 落盘（无 keyring；root 失守即全失，设备单用户场景接受）」
- [X] T019 lint 门禁：`gofmt -l .`、`go vet ./...`、`staticcheck ./...` 全清；`unshare -rUn bash -c 'ip link set lo up && go test ./...'` 全绿（SC-005）
- [ ] T020 quickstart 矩阵：§0/§6 本地执行勾选；§1–§5 实机部分（真 OpenWrt arm64 或 mipsle）人工执行并记录回 quickstart.md（017/018 豁免维度）
  > 部分完成（见 quickstart「执行记录」）：§0/§6 已执行；§1–§5 由 CI 自动化等价承载，实机部分（scp/procd/reboot/真 ping）待真 OpenWrt 设备人工执行

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup**: T001 ∥ T002 ∥ T003（三个不同包）
- **Foundational**: T004 ∥ T005（engine 与 daemon 不同包；daemon 用接口不依赖 T004 完成）
- **US1**: T006 依赖 T001–T005（编译面）；T007 可与 T006 并行起；T008 依赖 T003+T007；T009 依赖 T004+T005+T007；T010 收尾
- **US2**: T011 依赖 T010；T012 依赖 T011 红；T013 收尾
- **US3**: T014 依赖 T010（可与 US2 并行）；T015 依赖 T014 红
- **Polish**: T016 ∥ T017 ∥ T018 随时；T019/T020 必须最后

### Parallel Opportunities

```text
Setup:        T001 ∥ T002 ∥ T003
Foundational: T004 ∥ T005
US1:          T006 ∥ T007
US2 ∥ US3:    T011/T012 与 T014/T015 无文件级冲突可交错（同一测试文件需协调，串行更稳）
Polish:       T016 ∥ T017 ∥ T018
```

### Within Each Story

- 测试任务（T006/T011/T014）先写、确认红，再实现；T010/T013/T015 为转绿闸门
- 全量回归命令必须带 `ip link set lo up`

---

## Implementation Strategy

**MVP = Phase 1→3（T001–T010）**：netns 内从零 onboard 到 ping 通服务器全自动化，即可演示。
**增量 2 = Phase 4（T011–T013）**：zone 全生命周期 CLI。
**增量 3 = Phase 5（T014–T015）**：登出收尾生命周期。
**收尾 = Phase 6**：procd/交叉编译/DESIGN.md/门禁/实机矩阵。

---

## Notes

- 服务端零触碰；`internal/client` 仅 T001–T003 的三处加法，Windows 路径回归由全量套件钉死（FR-013）。
- T006 的 netns 装具复用 030 `announce_dataplane_test.go` 的 inNS/newChildNS/addVeth 模式——可考虑把这三个测试辅助抽到 `internal/testutil`（三个消费者：030 e2e、031 集成、未来 032/033——三次法则达标，实现时裁决）。
- T009 的 `status` 在 daemon 未跑时必须照常工作（接口不存在 → daemon=stopped、tunnel=disconnected，其余字段照常；spec 边界用例）。
- 实机矩阵（T020 的 §1–§5 部分）不阻塞合并，与 029/030 的人工遗留项同列管理。

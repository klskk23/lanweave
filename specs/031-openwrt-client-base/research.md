# Research: OpenWrt 客户端基础 (031)

> Phase 0 产出。输入：spec.md、grill 共识、`internal/client/*` 复用度普查（2026-06-11）。

---

## D1 — 复用矩阵：现有客户端栈四件套直接复用，零抽取重构

**Decision**: 逐包核验后确认零 GUI 耦合，按下表复用：

| 包 | 复用方式 | 所需改动 |
|---|---|---|
| `internal/client/apiclient` | 原样（纯 HTTP+protocol，TOFU/typed errors/refresh 全内置） | `RegisterNode` 增 platform 入参（新增 `RegisterNodePlatform(name, pubKey, platform)`，旧方法委托传空，Windows 调用零改动） |
| `internal/client/keyring` | 原样——`store_other.go` 已有 `!windows` 文件后端 | 增 `OpenAt(dir)` 构造器（现 `Open()` 用 UserConfigDir；路由器要 `/etc/lanweave/keys` 注入） |
| `internal/client/state` | 原样（Load/Save/Clear 路径注入、原子写） | `Record` 增 `NodeID int64`（见 D5），SchemaVersion 2→3（旧记录零值兼容，018 先例） |
| `internal/client/wgkey` | 原样（纯函数） | 无 |
| `internal/client/onboard` | **原样复用 `Provision`/`Cleanup` 编排**（接口驱动，无 UI） | `Provisioner` 增 `Platform string` 字段（空=旧行为） |
| `internal/client/tunnel` | **不复用**（wireguard-go+WinTun 引擎，Windows 专属） | 自愈阈值常量语义对齐 028（独立定义并注释互指） |

**Rationale**: 宪法 I（可逆、不过早抽象）：四件套本就以「路径/接口注入」写成，复用即拿来；唯一的跨端共享点是 protocol 与 apiclient，早已是单一真源。

**Alternatives considered**: 抽取共享 `pkg/clientcore`（过早抽象，两个消费者就重构，拒绝）；路由器另写一套 API 客户端（重复 TOFU/refresh/错误映射三块硬逻辑，拒绝）。

## D2 — 新组件形状：`cmd/lanweave-routerd` + `internal/router/{engine,daemon}`

**Decision**:
- `internal/router/engine`: 内核 WireGuard 隧道引擎——创建/拆除 wg 接口、配地址与 VPN 网段路由、配置 server peer（endpoint/AllowedIPs/keepalive=25s）、读取最后握手。netlink+wgctrl 直驱，形态即 030 e2e `newPeerNS` 的产品化（该测试代码就是验证过的参考实现）。
- `internal/router/daemon`: 生命周期——从 state 装配引擎、起隧道、028 同款健康循环（15s 查握手、>240s 陈旧即重建、重试不退避不退出）、SIGTERM 优雅拆除。
- `cmd/lanweave-routerd`: 单二进制，子命令分发 + 各命令编排（onboard 三步、zone 族、status、logout、trust、run）。

**Rationale**: engine/daemon 分层让健康循环可用假引擎做无头单测（028 在 tunnel 包的同款手法），真引擎走 netns 集成测试；服务端 `internal/server/wg` 是服务器视角（地址=池首个、无 endpoint/keepalive），不混用。

**Alternatives considered**: 复用 server wg 包加客户端模式开关（双职责污染，拒绝）；daemon 与 CLI 拆两个二进制（路由器上分发与文档负担翻倍，拒绝）。

## D3 — CLI：标准库 `flag` + 手工子命令分发，零新依赖

**Decision**: `lanweave-routerd <subcommand> [flags]`；密码/邀请码经 `--password-stdin` 类 flag 从标准输入读；输出人类可读、字段名稳定；非零退出码区分失败。子命令清单见 contracts/cli-commands.md。

**Rationale**: 子命令 ~10 个、flag 简单，cobra/urfave 带来的补全/帮助生成不值一个新依赖树（宪法 I + 依赖卫生）；Go 团队自身工具链同款做法。

**Alternatives considered**: spf13/cobra（依赖树大）、urfave/cli（同理）。均拒绝。

## D4 — 文件布局与权限：`/etc/lanweave/`（可被 flag/env 覆盖）

**Decision**:
- 状态：`/etc/lanweave/state.json`（state.Record，0600）
- 凭据/私钥：`/etc/lanweave/keys/`（keyring fileStore via `OpenAt`，目录 0700、文件 0600）
- 覆盖：`--data-dir` flag（测试与非标准布局），无散落 env（宪法 I）
- pidfile：procd 管理生命周期，不自管 pidfile；「daemon 是否在跑」由 wg 接口存在性 + procd status 判定

**Rationale**: OpenWrt 上 `/etc` 持久（overlayfs），路由器单用户 root 场景无 per-user 目录需求；与 UCI 集成留给 LuCI 切片。

**Alternatives considered**: UCI (`/etc/config/lanweave`)（绑定 OpenWrt 专有格式，挡住普通 Linux 验收与 CI，拒绝——留 LuCI 切片）；`/var/lib`（OpenWrt 上是 tmpfs，重启即失，拒绝）。

## D5 — 状态扩展：`Record.NodeID`（SchemaVersion 2→3）

**Decision**: `state.Record` 增 `NodeID int64`，onboard 成功时写入；登出（FR-010）与未来 032 宣告都需要稳定的本机 node id。SchemaVersion 升 3：v2 记录加载时 NodeID 取零值（Windows 端不依赖该字段，行为不变；loader 接受 ≤3）。

**Rationale**: Windows 登出现状靠运行期解析，路由器 CLI 每次进程都是冷启动，按 ID 直查最稳；018 的 schema 迁移先例（零值语义无缝）。

## D6 — daemon 与 CLI 不做 IPC：状态经内核与文件共享

**Decision**: v1 无 socket/IPC。CLI 的 status 直接读 wgctrl（握手/接口）+ state 文件 + API（zones）；zone 命令直连 API；daemon 唯一职责是隧道生命周期。两者对 state 的写入用「临时文件+rename」原子写（state 包现状）+ 写前读校验。

**Rationale**: 本切片 CLI 与 daemon 没有任何必须实时协商的状态（AllowedIPs 固定 VPN 大段）；033 引入动态路由时再评估是否需要通知通道（彼时记入 033 计划）。少一个 IPC 面 = 少一类故障与测试矩阵。

**Alternatives considered**: unix socket 控制接口（v1 无消费者，纯负担）；D-Bus/ubus（绑定 OpenWrt，CI 难）。均拒绝。

## D7 — 测试策略（宪法 II 三层）

**Decision**:
- **Unit**: CLI 子命令分发与 flag 解析（表驱动）；daemon 健康循环用假引擎（028 同款：陈旧判定/重试/单飞）；state schema v2→v3 兼容。
- **Integration（`unshare -rUn`，真内核 wg + 真服务端栈）**: engine 起真接口断言地址/路由/peer/keepalive；onboard→daemon→ping 服务器 VPN 地址（netns 拓扑复用 030 e2e 装具模式：服务端栈进专属 ns，路由器侧再一个 ns）；zone 命令全生命周期（真 store/nft）；登出三态（可达清盘/不可达阻止/强制）。CLI 层以 `go test` 内调用命令入口函数（不 exec 二进制）覆盖编排。
- **Acceptance**: quickstart 实机矩阵（arm64 或 mipsle 任一台 OpenWrt：安装→onboard→ping→重启自愈→procd 拉起）；三目标交叉编译 `make routerd-cross` 通过即 FR-012 验收。

**Rationale**: 030 已证明 netns 双命名空间真流量测试可行且稳定；实机硬件维度沿用 017/018 人工豁免先例（spec 已登记）。

## D8 — procd 与交叉编译

**Decision**:
- `packaging/openwrt/lanweave-routerd.init`: procd 脚本——`START=95`、`USE_PROCD=1`、`procd_set_param respawn`、stop 时调 `lanweave-routerd down`（拆接口）；脚本静态检查（shellcheck 风格人工核）+ 实机矩阵验收。
- Makefile 增 `routerd` 与 `routerd-cross` 目标：`CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64,mipsle} GOMIPS=softfloat`，产物 `dist/lanweave-routerd-<arch>`。
- DESIGN.md 同 PR：客户端清单补「OpenWrt 无头客户端」；§11 登记「路由器凭据明文 0600 落盘（无 keyring，root 失守即全失）」。

**Alternatives considered**: 本期就做 .ipk（spec 已排除，留打包切片）；upx 压缩（硬件决策不需要，且有兼容性风险）。均拒绝。

## 解决的 NEEDS CLARIFICATION

无遗留：形态/硬件/凭据/语义对齐来自 spec Clarifications；D1–D8 锁定全部技术选型（复用边界、新包形状、CLI 框架、路径、schema、IPC 取舍、测试、procd）。

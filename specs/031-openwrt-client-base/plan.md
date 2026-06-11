# Implementation Plan: OpenWrt 客户端基础（无头 daemon + CLI）

**Branch**: `031-openwrt-client-base` | **Date**: 2026-06-11 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/031-openwrt-client-base/spec.md`

## Summary

为 openwrt 平台交付客户端本体：单二进制 `lanweave-routerd`（daemon + CLI 子命令），非交互、可脚本化。最大化复用现有客户端栈——apiclient（TOFU/refresh/typed errors 原样）、keyring 文件后端（`OpenAt` 注入 `/etc/lanweave/keys`）、state（`Record` 增 NodeID，schema 2→3）、wgkey、**onboard.Provision 编排原样复用**（增 Platform 字段）；新写两个小包：`internal/router/engine`（内核 WireGuard 隧道，netlink+wgctrl，即 030 e2e 验证过的形态产品化）与 `internal/router/daemon`（028 同款健康自愈循环）。procd init 脚本 + Makefile 三架构交叉编译（amd64/arm64/mipsle softfloat）。无 IPC：CLI 读 wgctrl+state+API，daemon 唯一拥有隧道生命周期。

## Technical Context

**Language/Version**: Go 1.26（仓库现状）

**Primary Dependencies**: 既有依赖全覆盖——`wgctrl`、`vishvananda/netlink`（均已 direct）、`pkg/protocol`、`internal/client/{apiclient,keyring,state,wgkey,onboard}`。**零新增依赖**（CLI 用标准库 flag，cobra 拒绝）。

**Storage**: 仅客户端本地文件 `/etc/lanweave/{state.json, keys/}`（0600/0700，`--data-dir` 可覆盖）；服务端零 schema 变化

**Testing**: unit（CLI 分发、daemon 假引擎健康循环、state v3 兼容）；integration `unshare -rUn`（engine 真内核接口、netns 双 ns 全链路 onboard→ping、zone 生命周期、登出三态——复用 030 e2e 装具模式）；acceptance（实机 OpenWrt 人工矩阵 + 三目标交叉编译）

**Target Platform**: Linux/OpenWrt 路由器（≥64MB flash / 128MB RAM）；首发 arm64 + amd64 + mipsle(softfloat)；CGO_ENABLED=0 静态二进制

**Project Type**: headless CLI/daemon（新客户端形态）

**Performance Goals**: 首次握手 ≤3s（宪法 IV）；CLI 命令本地部分即时、网络部分受 API 预算约束；二进制 ~10MB（64MB flash 无压力）

**Constraints**: 凭据 0600 明文（无 keyring 硬件）；onboard 三件套原子一致（019）；TOFU=018、续期=024、登出=025、自愈=028 跨端语义一致（宪法 III）；服务端与 Windows 客户端零行为变化

**Scale/Scope**: 新增 2 个包 + 1 个 cmd + procd 脚本 + Makefile 目标；既有包 3 处加法（apiclient 方法、onboard 字段、state 字段）；~12 个 CLI 子命令

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 适用性与符合方式 | 结论 |
|---|---|---|
| I. Code Quality | 复用优先零抽取（D1，四件套注入式复用）；engine/daemon 单一职责分层；零新依赖；配置单点（--data-dir，无散落 env）；无 panic 路径 | PASS |
| II. Testing Standards | 跨内核边界（wg/netlink）→ 真实例集成测试（netns 双 ns 全链路，030 装具先例）；健康循环假引擎单测（028 先例——这是自有 seam 非系统边界 mock）；每 story ≥1 验收测试；实机硬件维度人工豁免（017/018 先例，spec 登记） | PASS |
| III. UX Consistency | CLI 错误信息人类可读、退出码语义一致、status 字段稳定；TOFU/续期/登出/自愈四语义与 Windows 端逐条对齐（spec Clarifications） | PASS |
| IV. Performance | 首次握手 ≤3s 直接入验收；daemon 常驻内存为单 goroutine 循环，路由器 128MB 无感 | PASS |
| Security & Operational | 凭据 0600 明文落盘为接受风险（同 PR 登记 §11）；输出/日志零机密（测试断言）；TOFU 默认安全、--insecure 不持久化；无新 crypto | PASS（§11 修订随 PR） |
| Workflow Gates | spec→plan→tasks→implement 全走；DESIGN.md 客户端清单与 §11 同 PR 修订；ROADMAP 合并勾选 | PASS |

**Post-Phase-1 re-check**: 设计未引入违规。点名两个取舍：①无 IPC（D6，033 时重评）；②健康循环阈值常量与 Windows tunnel 包各自定义、注释互指（提取共享包属过早抽象）。均不构成宪法偏离。无 Complexity Tracking 条目。

## Project Structure

### Documentation (this feature)

```text
specs/031-openwrt-client-base/
├── plan.md / research.md / data-model.md / quickstart.md
├── contracts/cli-commands.md      # CLI 子命令契约 + procd 契约 + 回归契约
└── tasks.md                       # Phase 2（/speckit-tasks）
```

### Source Code (repository root)

```text
cmd/lanweave-routerd/main.go            # 新：子命令分发 + 各命令编排
internal/router/engine/engine.go(+_test)  # 新：内核 wg 隧道（建/拆/握手/地址/路由）
internal/router/daemon/daemon.go(+_test)  # 新：生命周期 + 028 健康循环（engine 接口注入）
internal/router/cli_test.go 或 cmd 内测试  # CLI 编排测试（调入口函数，不 exec）
internal/client/apiclient/client.go     # 改：+RegisterNodePlatform（旧方法委托）
internal/client/onboard/onboard.go      # 改：Provisioner +Platform 字段
internal/client/state/state.go(+_test)  # 改：Record +NodeID，SchemaVersion 3，v2 兼容
internal/client/keyring/store_other.go  # 改：+OpenAt(dir) 构造器
packaging/openwrt/lanweave-routerd.init # 新：procd 脚本
Makefile                                # 改：routerd / routerd-cross 目标
DESIGN.md                               # 改：客户端清单 + §11 凭据明文风险
docs/（最小安装文档，入 packaging/openwrt/README.md）
```

**Structure Decision**: 新代码进 `internal/router/`（与 `internal/client/`——Windows GUI 栈——平级，名字即边界）；复用包只做向后兼容加法。`cmd/lanweave-routerd` 与 `cmd/lanweave-client`、`cmd/lanweaved` 并列。

## Complexity Tracking

无宪法违规，本表为空。

## 已接受的限制（设计期显式登记）

- **无 IPC**（D6）：CLI 与 daemon 经内核（wgctrl）与状态文件间接共享；033 动态路由引入时重新评估通知通道。
- **凭据明文 0600**：路由器无 keyring；root 失守即全失（§11 登记）。
- **实机硬件人工验收**：CI 覆盖 netns 维度，真 OpenWrt（arm64/mipsle）走 quickstart 矩阵（017/018 豁免先例延伸）。
- **028 阈值常量重复定义**（daemon 与 Windows tunnel 各一份，注释互指）：两个消费者不抽包（宪法 I 第三条）。
- **CLI 英文单语**：i18n 是 GUI 范畴（020），路由器 CLI 不做。

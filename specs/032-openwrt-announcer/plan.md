# Implementation Plan: OpenWrt 宣告端（宣告 CLI + 地址翻译下发）

**Branch**: `032-openwrt-announcer` | **Date**: 2026-06-11 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/032-openwrt-announcer/spec.md`

## Summary

为 031 的 `lanweave-routerd` 加上宣告端能力，打通 030–033 功能链的"最后一公里"：`announce add/remove/list` 命令族驱动 030 服务端 API，宣告成功即在路由器自有 nftables 表（`inet lanweave_rt`）下发**前缀 1:1 翻译**（prerouting dstnat，`expr.NAT`+`NF_NAT_RANGE_PREFIX`——已核验 google/nftables v0.3.0 原生支持，零新依赖）与**源伪装**（postrouting masquerade，内核自动选出口地址）。本地规则是纯派生态：服务器宣告清单为唯一真源，daemon 启动对账重建 + 运行期 60s 周期对账（独立 goroutine，不改 031 daemon 包）+ 命令后即时重建；`announce add` 具备补偿原子性（本地失败→自动撤回远端挂接）。E2E 用三命名空间拓扑（serverNS/routerNS/memberNS），CLI 以 **ns 钉扎 goroutine 同步执行**获得隔离——产品代码零测试钩子，031 评审发现的共享 ns 干扰从设计上规避。

## Technical Context

**Language/Version**: Go 1.26（仓库现状）

**Primary Dependencies**: 既有依赖全覆盖——google/nftables（`NF_NAT_RANGE_PREFIX` 已核验）、netlink、wgctrl、apiclient/protocol（030 DTO 现成）。**零新增依赖。**

**Storage**: 无任何持久化新增（本地规则为派生态，真源在服务器）

**Testing**: unit（期望集计算纯函数、CLI 分发增量）；integration `unshare -rUn`（natctl 真内核：prefix-DNAT spike、Rebuild 幂等/清多余/补缺失、masquerade 形状）；e2e（三 ns：宣告→成员↔LAN 往返→撤回→重启重建→第三方移除收敛→FR-005 补偿 seam）；acceptance（实机 fw4 共存矩阵）

**Target Platform**: OpenWrt 路由器（031 同款三架构）；内核 ≥5.8（NF_NAT_RANGE_PREFIX）——OpenWrt 22.03+ 满足

**Project Type**: headless CLI/daemon 增量（031 延续）

**Performance Goals**: 全表重建 O(宣告数≤10) 微秒级；对账 60s 周期 2 次 API 调用每 zone，路由器负载可忽略；翻译为内核态 NAT，转发性能无用户态开销

**Constraints**: 自有表不碰 fw4；规则派生态可全量重建（宪法 I 客户端镜像）；FR-005 补偿原子性；单向性（仅 DNAT 合成→真实 + masquerade，无反向路径）；服务端/Windows/031 命令零回归

**Scale/Scope**: 1 个新包（natctl）+ 1 个新文件（reconcile 期望集）+ CLI 三子命令 + apiclient 三方法/6 typed errors + logout 扩展 + e2e 装具

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 适用性与符合方式 | 结论 |
|---|---|---|
| I. Code Quality | natctl 单一职责（本机 NAT 派生态）；全量重建模式镜像服务端 netfw（无增量簿记）；无第二真源（不持久化副本）；零新依赖、无散落 env | PASS |
| II. Testing Standards | nftables 内核边界→真实例集成（含 D1 spike 前置验证）；三 ns e2e 真流量（030/031 装具合体）；补偿路径经 env 注入 seam 自动化（自有边界非系统 mock）；每 story ≥1 验收测试；实机 fw4 维度人工豁免（先例延续） | PASS |
| III. UX Consistency | CLI 形态/退出码/错误文案延续 031；six 030 错误码映射人类可读；list 的 RULES 列让状态可见（FR-011） | PASS |
| IV. Performance | 内核态 NAT 零额外路径；重建/对账开销可忽略；预算不受影响 | PASS |
| Security & Operational | 单向性机制化（无反向规则）；自有表不削弱 fw4；§11 无新增风险（030 已登记抹源/社工/自报三条）；输出零机密延续 031 断言 | PASS |
| Workflow Gates | spec→plan→tasks→implement；DESIGN.md §9 宣告端职责同 PR 补述；ROADMAP 合并勾选 | PASS |

**Post-Phase-1 re-check**: 无新增违规。点名取舍：①对账独立 goroutine 而非改 daemon 包（职责边界，033 若需通知通道再统一演进）；②`NF_NAT_RANGE_PREFIX` 依赖内核 ≥5.8——以 spike 测试为闸门，失败回退 exec-nft（登记于 research D1，不预实现）。无 Complexity Tracking 条目。

## Project Structure

### Documentation (this feature)

```text
specs/032-openwrt-announcer/
├── plan.md / research.md / data-model.md / quickstart.md
├── contracts/announce-cli.md
└── tasks.md（/speckit-tasks 产出）
```

### Source Code (repository root)

```text
internal/router/natctl/natctl.go(+_test)     # 新：lanweave_rt 表全量重建（dnat-prefix + masquerade）、删表
internal/router/reconcile/reconcile.go(+_test) # 新：期望集计算（ListZones→ListAnnouncements→过滤/去重）+ 对账驱动
internal/client/apiclient/client.go(+_test) # 改：三方法 + 6 typed errors（纯加法）
cmd/lanweave-routerd/main.go                 # 改：announce 子命令族、cmdRun 启动对账循环、logout 删表、env 注入 natctl seam
cmd/lanweave-routerd/announce_test.go        # 新：三 ns e2e + 补偿 seam 测试（031 main_test 装具扩展）
packaging/openwrt/README.md                  # 改：announce 命令示例
DESIGN.md                                    # 改：§9 宣告端职责补述
```

**Structure Decision**: natctl 与 reconcile 各成小包（NAT 派生态 / 真源对账两个职责）；CLI 仍单文件挂子命令；e2e 装具沿 031 main_test 演进（三 ns 钉扎模式为本切片新增、后续 033 可复用——届时三消费者再议 testutil 提取，031 评审已登记）。

## Complexity Tracking

无宪法违规，本表为空。

## 已接受的限制（设计期显式登记）

- **D1 内核下限**：NF_NAT_RANGE_PREFIX 需内核 ≥5.8；spike 测试为闸门，旧内核设备不在 031 既定硬件目标内；万一 CI/实机不支持，回退 exec-nft（仅登记）。
- **对账窗口**：第三方移除后 ≤60s 本地规则残留——服务端数据面已先收口，残留无害（spec 边界用例明文）。
- **API 不可达时规则冻结**：保持现状不删（避免网络抖动清空可达性）；恢复后对账收敛。
- **OpenWrt 自身作为消费者**（访问他人宣告）不在本切片（033 统一处理全平台消费端）。

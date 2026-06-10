# Implementation Plan: 服务端子网宣告控制面（合成网段映射）

**Branch**: `030-server-subnet-announce` | **Date**: 2026-06-11 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/030-server-subnet-announce/spec.md`

## Summary

zone 成员可把（具备地址翻译能力的 openwrt 平台）节点背后的真实 LAN 宣告进 zone：服务端从专用合成段池分配等长、全服唯一的合成段，把它写进该节点 peer 的 AllowedIPs 并在该 zone 的 nftables 状态中放行「成员 → 合成段」新建流 + conntrack 回程；真实网段永不进任何隧道路由，故跨节点任意重叠。模型为（节点,真实子网）→合成段一次分配、可挂接多 zone，最后挂接消失整条回收；六条级联路径与重启全量重建保证 DB/wg/nft 三处永不漂移。本切片纯服务端（031 OpenWrt 客户端、032 宣告端翻译、033 消费端路由为后续切片），端到端用 netns 模拟宣告节点验证。

## Technical Context

**Language/Version**: Go 1.26（仓库现状）

**Primary Dependencies**: 既有依赖全覆盖——`google/nftables`（interval set + ct expr）、`wgctrl`（ReplaceAllowedIPs）、`modernc.org/sqlite` + goose（迁移 0007）。**零新增依赖。**

**Storage**: SQLite 迁移 `0007_announcements.sql`：`nodes.platform` 列 + `announcements` + `announcement_zones` 两表（data-model.md §1）

**Testing**: unit（ipam 块分配器、校验纯函数）；integration `unshare -rUn`（store 事务/配额/级联、netfw interval set、api 错误矩阵、app 重启重建）；e2e（app 层 netns 拓扑，成员 ping 合成地址过真 wg+nft，宣告 netns 手工 NETMAP 模拟 032）；quickstart 手工矩阵

**Target Platform**: Linux 服务端；现有客户端零行为变化（注册可选新字段向后兼容）

**Project Type**: web-service（现有服务端增量切片）

**Performance Goals**: 沿用宪法 IV——nft set 元素增删 ≤50ms、API 写 P50 ≤300ms；块分配器 O(现存宣告数) 整数扫描，配额上限下可忽略

**Constraints**: 合成段全服唯一且自然对齐；池缺省=功能停用（升级零行为变化）；单向连接语义（ct established/related 承载回程）；SQLite 单一真源、派生态可重建

**Scale/Scope**: 3 个新端点 + 1 个端点加字段；6 个新错误码；迁移 1 个；触及 config / ipam / store / netfw / wg / api / app(dataplane) / protocol / openapi.yaml / DESIGN.md

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 适用性与符合方式 | 结论 |
|---|---|---|
| I. Code Quality | 块分配器入 ipam（纯数学包职责不变）；netfw 增量 API 与既有 AddMember 同构；SQLite 单一真源、wg/nft 派生态全量可重建（FR-011 显式测试）；配置一次加载、三态指针复用 023 模式 | PASS |
| II. Testing Standards | 跨内核边界 → 三层全配：真 SQLite/nftables/WireGuard，禁 mock；6 条级联路径逐一集成测试；netns e2e 复用 app 层既有拓扑模式；迁移 0007 有升级测试；每 user story ≥1 验收测试（US1→e2e 连通、US2→同段并存、US3→级联+重建） | PASS |
| III. UX Consistency | 服务端切片；错误码语义与 023/004 同族（limit_reached/pool_exhausted 命名延续），映射展示数据为 033 的 UI 备好 | PASS（弱适用） |
| IV. Performance | nft 单元素操作不变；ct 规则 O(1) 一条；分配器扫描微秒级；预算无新风险 | PASS |
| Security & Operational | 平台自报、MASQUERADE 抹源、社工面三条风险同 PR 登记 §11（FR-014）；输入边界校验完备（FR-007 矩阵）；无新 crypto；单实例假设不变 | PASS（§11 修订随 PR） |
| Workflow Gates | spec→plan→tasks→implement 全走；DESIGN.md 非目标修订同 PR；ROADMAP 合并时勾选；029 的 openapi 一致性哨兵强制文档同步 | PASS |

**Post-Phase-1 re-check**: 设计未引入违规。唯一值得点名的取舍——forward 链头部的全局 `ct state established,related accept` 改变了既有成员↔成员回程包的匹配路径（行为等价，原本也放行），由现有 zone 隔离集成测试回归兜底；不构成宪法偏离。无 Complexity Tracking 条目。

## Project Structure

### Documentation (this feature)

```text
specs/030-server-subnet-announce/
├── plan.md              # 本文件
├── research.md          # Phase 0（D1~D8）
├── data-model.md        # Phase 1（schema/配置/DTO/不变量/状态机/nft 形状）
├── quickstart.md        # Phase 1（手工验收矩阵）
├── contracts/
│   └── announce-endpoints.md   # 新 HTTP 面 + 既有端点变更 + 回归契约
└── tasks.md             # Phase 2（/speckit-tasks 产出）
```

### Source Code (repository root)

```text
internal/server/store/migrations/0007_announcements.sql   # 新
internal/server/store/announcements.go(+_test)            # 新：CRUD/配额/复用/级联（事务）
internal/server/store/nodes.go                            # 改：platform 读写
internal/server/store/zones.go                            # 改：leave/kick/删 zone 级联挂接
internal/server/store/users.go                            # 改：删用户级联（008 扩展）
internal/server/ipam/blocks.go(+_test)                    # 新：等长对齐块 first-fit 分配 + 重叠/RFC1918 判定
internal/server/config/config.go(+_test)                  # 改：[announce] pool + limits 三态
internal/server/netfw/nftables.go(+routes_test)           # 改：zone_<id>_routes interval set、ct 规则、Rebuild 扩展
internal/server/wg/iface.go(+_test)                       # 改：PeerConfig.Routes、SetPeerRoutes、ReplacePeers 扩展
internal/server/api/announce_handlers.go(+_test)          # 新：3 端点 + 错误矩阵
internal/server/api/node_handlers.go                      # 改：platform 入参/出参
internal/server/api/routes.go                             # 改：路由表 +3 行
internal/server/api/docs/openapi.yaml                     # 改：3 端点 + 字段 + 6 错误码（029 哨兵强制）
internal/server/api/openapi_consistency_test.go           # 改：knownErrorCodes += 6
internal/server/app/app.go / dataplane.go(+_test)         # 改：启动重建含宣告；e2e netns 测试
pkg/protocol/node.go / announce.go(新)                     # 改/新：DTO
config.toml.example                                       # 改：[announce] 段 + limits 键
DESIGN.md                                                 # 改：非目标修订 + §11 三条 + 网络模型补节
```

**Structure Decision**: 全部落在既有包边界内：块数学进 `ipam`（包职责「纯 IPv4 池数学」不变）、宣告持久化独立 `store/announcements.go`、防火墙增量进 `netfw`、handler 独立文件进 `api`——无新包，无新依赖。

## Complexity Tracking

无宪法违规，本表为空。

## 已接受的限制（设计期显式登记）

- **本切片交付后端到端用户价值不完整**：真实宣告端（032）与消费端路由（033）未到位前，功能仅可由模拟节点/curl 验证——spec Assumptions 已声明的中间态。
- **全局 ct established/related 规则**：使既有成员互通的回程改走 conntrack 快路径（行为等价），nft 规则形态与 DESIGN §隔离面的字面描述略有出入 → DESIGN.md 网络模型补节随 PR 更新（FR-014 范围内）。
- **平台标识自报可谎报**：后果限于「宣告了兜不住的网段、自己 zone 内不通」，无越权（§11 登记）。
- **重复挂接同一 zone 的幂等语义**（200/201 选择）留给实现时对齐 joinZone 现状，不另发明。

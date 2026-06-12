# Research: 消费端动态路由 (033)

> Phase 0 产出。输入：spec.md、030/031/032 已合并代码现状核读（2026-06-12）。

---

## D1 — Windows 端机制：UAPI 热更新（replace_allowed_ips）+ 平台路由差分，不重连

**Decision**: `tunnel.Tunnel` 保留着 `dev *device.Device`（tunnel.go），增 `SetExtraRoutes(extra []netip.Prefix) error`：
1. `dev.IpcSet` 一段仅含 `public_key=<server> + replace_allowed_ips=true + allowed_ip=<VPN网段> + allowed_ip=<每个合成段>` 的增量 UAPI 文本（wireguard-go 语义：携带 public_key 的既有 peer 即为更新，不触碰握手/keepalive/endpoint——**不断流**）。
2. 平台路由差分：新增前缀加路由、消失前缀删路由（linux: netlink `RouteReplace/RouteDel`（addr_linux.go 既有模式）；windows: `netsh interface ip add/delete route`（addr_windows.go 既有模式），各自新增 `addRoute/delRoute` 辅助）。
3. Tunnel 记忆当前 extra 集合（diff 依据 + 幂等：集合相等直接跳过）。

**Rationale**: 满足 spec「无需重连即生效」；复用既有设备句柄与平台分文件结构；断开时 wireguard-go 关设备 = WinTun/tun 接口整体消失，路由随接口自动清除（FR-004 零残留由现有 teardown 语义免费保证——linux 同理）。

**Alternatives considered**: 集合变化即自动重连（断流 1–2s、握手重协商、与「无需重连」spec 行为冲突）；winipcfg 库（新依赖，netsh 既有先例够用）。均拒绝。

## D2 — Windows 刷新驱动：panel 控制器连接期 60s 路由对账（独立于 15s 健康检查）

**Decision**: panel 控制器（028 自愈循环所在层）增连接期路由对账：连接成功即一次 + 每 60s 一次——`ListZones`→`ListAnnouncements(zone)` 聚合（D4 共享函数）→`tunnel.SetExtraRoutes`。API 失败跳过本轮（路径冻结，FR-003）；断开即停。节奏与 OpenWrt 端（032 的 60s）对齐——FR-009 跨端一致。

**Rationale**: 健康检查（15s）与路由对账（60s）职责不同、频率不同，分开各自简单；面板「可达子网」区块直接复用同一聚合结果。

## D3 — OpenWrt 端机制：engine 增 `SetRoutes` + 复用 032 对账循环（一次 API 喂两个消费者）

**Decision**:
- `internal/router/engine` 增 `SetRoutes(extra []netip.Prefix) error`：wgctrl `ConfigureDevice`（server peer，`ReplaceAllowedIPs: true`，AllowedIPs = VPN 网段 ∪ extra）+ 内核路由差分（RouteReplace 新增 / RouteDel 消失；记忆当前集合幂等跳过）。
- routerd 的 032 对账循环扩展：同一轮 `ListZones`+`ListAnnouncements` 聚合结果，分两路消费——①NAT 期望集（自己的宣告，032 现状）；②消费路由集（**他人**的宣告合成段，按合成段去重，排除 node_id==自己——避免自家流量绕服务器回环）。
- daemon 隧道重建（028 自愈）后路由由下一轮对账自动补上（≤60s）；更优：tryUp 成功后触发即时对账（实现期顺手）。

**Rationale**: 零新循环、零新 API 调用（聚合数据已在手）；排除自己=spec US2-2。

## D4 — 数据聚合：reconcile 包参数化为「双视图」

**Decision**: `reconcile.Desired` 演进为返回完整视图（或新增 `DesiredAll`，实现取舍）：聚合所在 zones 全部宣告（含 `NodeName/Owner`——**protocol DTO 030 已带，服务端零改动 FR-007 成立**），按宣告 id 去重；调用方各取所需：宣告者视图（node_id==自己→NAT/announce list）与消费者视图（node_id≠自己→路由/面板展示，按合成段去重）。Windows 端不 import router 包——聚合逻辑提至双方可达处（`internal/client/routesync` 新小包或 apiclient 旁），routerd 与 panel 共用。

**Rationale**: 一处聚合两端共用（FR-009 一致性的机械保证）；NodeName 现成免服务端改动。

## D5 — 本地冲突（FR-005）：逐条跳过 + 告警

**Decision**: SetExtraRoutes/SetRoutes 内逐前缀容错：单条路由失败（含与本机网段冲突）记日志/状态告警并跳过，其余继续；该条不进「已应用集合」，冲突消除后下轮对账自动补上。Windows 面板该条目标注「冲突」；routerd 日志承载。

## D6 — 测试策略（宪法 II）

**Decision**:
- **Unit**: 聚合双视图纯函数（去重/排除自己/多 zone）；UAPI 增量文本构造；路由差分计算纯函数。
- **Integration（Windows 路径，linux netns 跑 wireguard-go）**: tunnel_integration 既有装具演进——连接后 `SetExtraRoutes`，断言 UAPI allowed_ips 与系统路由就位/撤除/幂等/单条冲突跳过；**真流量**：netns 内访问合成地址 echo 往返（wireguard-go 用户态全链路）。
- **E2E（routerd 消费端，032 announce_test 装具演进）**: root=消费者 routerd；「宣告者」用 memberNS 手工 peer 模拟——**合成段地址直接挂其接口**（宣告者本地 NETMAP 翻译由 032 已单独验证，此处验证消费链路：root 路由/AllowedIPs→隧道→服务器按 peer AllowedIPs 转发→宣告者 peer→echo 往返）。断言：≤1 对账周期可达；撤回后不可达；自己宣告的合成段**不**配进隧道路径；面板/CLI 视图区分。
- **Acceptance（SC-005，真机功能链收口）**: 真 Windows ↔ 真 OpenWrt 路由器 LAN 设备——TODO 勾销条件，人工矩阵。

## D7 — 文档与收口

**Decision**: DESIGN.md §9 客户端补「消费端动态路由」一句；ROADMAP 033 行；**TODO.md「设备路由宣告到区域」在 SC-005 真机验收后勾销**（PR 不勾，留人工矩阵执行时勾——诚实原则）。

## 解决的 NEEDS CLARIFICATION

无遗留。唯一悬置（Windows 热更新方式）由 D1 锁定：IpcSet replace_allowed_ips 增量、不重连。

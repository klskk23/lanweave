# Research: OpenWrt 宣告端 (032)

> Phase 0 产出。输入：spec.md、grill 共识、030/031 已合并代码、google/nftables v0.3.0 能力核验（2026-06-11）。

---

## D1 — 前缀翻译实现：google/nftables `expr.NAT` + `NF_NAT_RANGE_PREFIX`，零新依赖

**Decision**: 用既有库直驱内核 NETMAP：prerouting dstnat 链上 `expr.NAT{Type: DNAT, Prefix: true, RegAddrMin: <真实段基址>}` 配合 daddr 匹配合成段——内核做 1:1 前缀翻译（主机位保留）。已核验 v0.3.0 的 `expr/nat.go:99` 原生携带该 flag；内核要求 ≥5.8（OpenWrt 22.03+ 与 CI 内核 7.x 均满足）。

**Rationale**: 与服务端 netfw 同一工具链与代码风格；可在 CI 真内核上直接集成测试；不引入对 `nft` 二进制的 exec 依赖（跨发行版差异、输出解析、注入面全免）。

**Alternatives considered**: exec `nft` CLI（OpenWrt 必有但 CI 镜像不保证、字符串拼接注入面、不可单测表达式形状）；用户态转发代理（性能与复杂度灾难）。均拒绝。

**风险与早期验证**: T-早期 spike 集成测试先证「prefix-DNAT 在 CI 内核可用」，失败即在 plan 内回退到 exec-nft 方案（登记，不预先实现）。

## D2 — 路由器 NAT 形态：自有表 + 全量重建（服务端 Rebuild 模式镜像）

**Decision**: 自有表 `inet lanweave_rt`（不触碰 fw4 的表，FR-006）：

```
table inet lanweave_rt {
  chain prerouting  { type nat hook prerouting  priority dstnat; }
      # 每条宣告: ip daddr <合成段> dnat prefix to <真实段>
  chain postrouting { type nat hook postrouting priority srcnat; }
      # 每条宣告: ip saddr <VPN池> ip daddr <真实段> masquerade
}
```

变更与对账一律**整表全量重建**（删表→建表→按期望集铺规则，原子 Flush）——不做逐规则增删与簿记。

**Rationale**: 每路由器宣告数 ≤ 配额（10 量级），全量重建微秒级；与服务端 `netfw.Rebuild` 同一模式，杜绝增量漂移类缺陷；fw4 共存性由「独立表 + 标准优先级」保证（NAT 链同优先级并存合法，先匹配先成）。masquerade 由内核自动选出口地址（直连取 LAN 地址、下游路由取出口地址），spec 的源伪装出口推导免实现。

**Alternatives considered**: 逐规则增删 + handle 簿记（030 评审已证全量重建模式更稳）；复用 fw4 include（绑定 OpenWrt、CI 不可测）。均拒绝。

## D3 — apiclient 增量：宣告三方法 + 6 个 typed errors

**Decision**: `internal/client/apiclient` 增 `CreateAnnouncement(zone string, nodeID int64, subnet string)`、`DeleteAnnouncement(zone string, id int64)`、`ListAnnouncements(zone string)`，复用 `pkg/protocol` 的 030 DTO；030 的 6 个错误码映射为 typed errors（`ErrPlatformUnsupported/ErrAnnounceDisabled/ErrSubnetInvalid/ErrSubnetOverlap/ErrAnnounceLimit/ErrSyntheticPoolExhausted`）。Windows 客户端零调用（033 再消费），加法零回归。

## D4 — 真源对账：期望集=服务器清单中 node=本机 的条目；独立 60s 循环

**Decision**:
- **期望集计算**：`ListZones()` → 对每个 zone `ListAnnouncements(zone)` → 过滤 `node_id == state.NodeID` → 按宣告 id 去重（多 zone 复用同一合成段，030 语义）→ 得 `[]{合成段, 真实段}`。
- **对账动作**：`natctl.Rebuild(期望集)`（D2 全量重建，天然幂等：清多余、补缺失）。
- **触发点**：daemon 启动即一次；运行期独立 goroutine 每 60s 一次（与健康循环解耦，cmdRun 内并行启动——不改 031 daemon 包）；`announce add/remove` 命令成功后立即本地重建（不等周期）。
- API 不可达时跳过本轮并记日志（规则保持现状；服务端数据面已先收口，残留无害——spec 边界用例）。

**Rationale**: 「服务器清单唯一真源、本地规则派生可重建」的最小实现；60s 在「第三方移除后收敛」与路由器 API 负载间取中（spec：15s 量级→一个量级内）。

**Alternatives considered**: 嵌进 daemon 健康 tick（把 API 依赖塞进纯隧道包，职责污染）；本地持久化宣告副本（第二真源=漂移源）。均拒绝。

## D5 — CLI 契约：`announce add/remove/list`

见 contracts/announce-cli.md。要点：`remove` 以 `<subnet> --zone Z` 指定（id 由 list 解析，用户不用记 id）；`list` 聚合本机全部宣告（含挂接 zones 与规则就位状态，FR-011）。

## D6 — FR-008 冲突检测：netlink 地址普查

**Decision**: `announce add` 在远端成功、下发本地规则前，对返回的合成段做本地冲突检测：`netlink.AddrList(nil, FAMILY_V4)` 全接口地址 → 任一接口网段与合成段重叠 → 触发 FR-005 补偿（撤回远端挂接）并报错。子网本机可达性仅提示（stderr note）不阻拦。

## D7 — 测试策略（宪法 II 三层）

**Decision**:
- **Unit**: 期望集计算（多 zone 去重/过滤他人节点）纯函数表驱动；CLI 分发增量。
- **Integration（unshare 真内核）**: `natctl` 包——prefix-DNAT spike（D1 验证）、Rebuild 幂等/清多余/补缺失、masquerade 规则形状断言（独立唯一表名）。
- **E2E（三命名空间，031/030 装具合体）**: serverNS（031 testServer 整体迁入，inNS 包裹其构建）⟷veth⟷ routerNS（**CLI 调用整体以 ns 钉扎 goroutine 执行**——`run()` 同步单线程，LockOSThread+setns 后调用即令其全部 netlink/nftables/socket 落在 routerNS；lan0 dummy 上挂 192.168.50.50 当零配置 LAN 主机）⟷serverNS⟷ memberNS（030 newPeerNS 同款手工成员，AllowedIPs 含合成池）。断言：宣告后成员↔LAN 往返通（SC-001）、撤回即断、daemon 重启重建恢复、第三方服务端移除→一个对账周期内本地清除（SC-002）、多 zone 部分撤回保留。
- **补偿路径（SC-004）**: `env` 注入 natctl 工厂 seam，测试换失败实现 → 断言远端挂接被补偿撤回。
- **Acceptance**: 实机矩阵（真 OpenWrt + fw4 共存 + 另一真实客户端访问 LAN 设备，SC-006）。

**Rationale**: 「ns 钉扎 goroutine 跑同步 CLI」让产品代码零测试钩子地获得命名空间隔离——031 评审发现的共享 ns 干扰在 032 测试设计里从根上规避。

**实现期修正（2026-06-11）**：钉扎方案被 `http.Transport` 击穿——其拨号发生在后台 goroutine（任意线程 ≠ 钉扎线程），CLI 的 HTTPS 流量必然落回 root ns。最终拓扑改为**双子 ns + root 即路由器**：server/member 各居子 ns（030 模式），路由器侧（CLI/HTTP/netlink/nftables/wg）天然运行于 root 测试 ns，全部 root 资源私有命名（`lwann0`/`lwlan0`/100.111 池/每测试唯一 veth 名）并显式清理；API handler 在 root 线程的增量 nftables 写以「root 骨架表」吸收、serverNS 真表由 `syncServerNFT` 从 store 重建（与服务端启动重建同机制）。隔离目标全部达成，三 e2e 与全量套件 ×2 验证。

## D8 — 文档同步

**Decision**: DESIGN.md §9 OpenWrt 客户端段补「宣告端职责：本地前缀翻译 + 源伪装，规则为派生态、服务器清单为真源」；§11 无新增（030 已登记抹源/社工/平台自报三条）。packaging/openwrt/README.md 补 announce 命令示例。

## 解决的 NEEDS CLARIFICATION

无遗留：机制承自 grill 方案 B 与 030 数据面；D1 把唯一技术不确定点（库的 NETMAP 能力）当场核验为可行。

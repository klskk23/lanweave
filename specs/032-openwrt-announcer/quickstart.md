# Quickstart: OpenWrt 宣告端 (032)

> 验收/冒烟步骤（宪法 II acceptance 层）。CI 自动化覆盖 netns e2e 维度；
> 实机部分（真 OpenWrt + fw4 + 真实第二客户端）人工执行（017/018/031 豁免先例）。

## 执行记录（2026-06-11，实现期）

- §7 ✅：lint 全清；CI 同款全量套件两轮全绿（含与 tunnel/030/031 包并行的资源隔离复验）。
- §1–§5 的 CI 等价物已由自动化承载：`TestAnnounceE2E`（宣告→映射回显→成员经替身地址 UDP 往返 LAN 监听者（SC-001 真内核 prefix-DNAT+masquerade）→list RULES=ok→双 zone 复用→FR-009 反向负断言→daemon 重启重建恢复）；`TestAnnounceFailuresAndCompensation`（公网段/未知 zone/自身重叠/配额逐一拒绝且零残留、FR-008 合成段冲突→补偿撤回、SC-004 注入失败 natctl→远端挂接补偿清除）；`TestAnnounceLifecycle`（部分撤回保留/未宣告 remove 拒绝/第三方服务端移除 ≤1 对账周期收敛/logout 删表）。
  - 实现期发现并修正：D7 的「ns 钉扎 CLI」被 http.Transport 后台拨号击穿——拓扑改为双子 ns + root 即路由器（research.md D7 修正段）。
- **剩余人工项**：§1–§6 实机部分（真 OpenWrt + fw4 共存 + 真实第二客户端访问 NAS，SC-006）。

## 1. 宣告全链路（US1 / SC-001）

前置：路由器已 onboard（031）、daemon 在跑、节点已在 zone `homelab`；路由器 LAN 上有一台设备（如 192.168.50.50 的 NAS）。

```sh
lanweave-routerd announce add 192.168.50.0/24 --zone homelab
# announced 192.168.50.0/24 -> 100.100.X.0/24 (zone homelab, id N)
lanweave-routerd announce list
nft list table inet lanweave_rt   # 两条规则：dnat prefix + masquerade
```

- [ ] 映射回显与 list 一致；服务端 `GET /zones/homelab/announcements` 出现该条
- [ ] **另一台已入 homelab 的客户端**访问 `100.100.X.50`（NAS 的替身地址）成功；NAS 侧零配置、其日志见访问者为路由器 LAN 地址
- [ ] 同子网 `announce add … --zone office`（节点也在 office）→ 复用同一合成段；list 的 ZONES 列出两个

## 2. 撤回与部分撤回（US2）

```sh
lanweave-routerd announce remove 192.168.50.0/24 --zone office
lanweave-routerd announce remove 192.168.50.0/24 --zone homelab
```

- [ ] 撤回 office 后：homelab 成员照常可达；list 的 ZONES 只剩 homelab
- [ ] 撤回 homelab（最后挂接）后：成员访问替身地址超时；`nft list` 该宣告规则消失

## 3. 派生态收敛（FR-004 / SC-002）

- [ ] 重启路由器（或 `/etc/init.d/lanweave-routerd restart`）→ 宣告可达性自动恢复（规则随对账重建）
- [ ] 在服务器侧由 zone owner 摘除该挂接 → ≤60s 内路由器本地规则消失（日志可见对账动作）

## 4. 失败矩阵（FR-005/007/008 / SC-003/004）

- [ ] 用 Windows 节点的账号在路由器上.. 不适用；改为：未入 zone 的子网宣告 → not found；重复宣告自身重叠子网 → 服务端 subnet_overlap 文案；配额打满 → announce_limit 文案——全部非零退出、`nft list` 零残留
- [ ] 人为制造合成段冲突（给某接口配 100.100.X.0/24 内地址后宣告）→ 命令失败指明冲突接口，服务端清单无该挂接（补偿撤回生效）
- [ ] CI 已自动化：本地下发失败注入 → 远端挂接被补偿撤回（SC-004）

## 5. logout 清场（US2-3）

- [ ] `logout`（或 `--force`）后 `nft list table inet lanweave_rt` 不存在

## 6. fw4 共存（SC-006，实机）

- [ ] OpenWrt 默认防火墙开启状态下完成 §1–§5；`fw4 reload` 后宣告可达性不受影响；`nft list ruleset` 中 lanweave_rt 与 fw4 表并存

## 7. 自动化回归

```bash
gofmt -l . && go vet ./... && staticcheck ./...
unshare -rUn bash -c 'ip link set lo up && go test ./...'
```

- [ ] 全绿：natctl 真内核（prefix-DNAT spike/Rebuild 幂等/清多余补缺失）、三命名空间 e2e（宣告→成员↔LAN 往返→撤回即断→重启重建→第三方移除收敛→补偿路径）、期望集纯函数、031/030/Windows 零回归

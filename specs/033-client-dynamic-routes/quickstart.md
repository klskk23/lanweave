# Quickstart: 消费端动态路由 (033)

> 验收/冒烟步骤（宪法 II acceptance 层）。CI 自动化覆盖 netns 维度（wireguard-go 用户态全链路 + routerd 内核消费链路）；
> 实机部分为**整条 030–033 功能链的最终收口**（SC-005）。

## 执行记录（2026-06-12，实现期）

- §4 ✅：lint 全清；CI 同款全量套件两轮全绿 + Windows 交叉构建过。
- §1–§3 的 CI 等价物已由自动化承载：
  - `TestIntegrationSetExtraRoutes`（tunnel/wireguard-go）：负基线→热更新 allowed_ips/路由就位→**握手时戳不回退、连接不掉**→真流量经隧道访问合成地址→幂等→单条冲突跳过其余生效→撤除即断→断开零残留（八连断言；服务器入子 ns 隔离）。
  - `TestSyncRoutes`（panel 控制器）：聚合喂隧道/冲突标注/API 失败冻结/**第二轮缩集传播**（SC-001 撤回收敛 panel 维度）。
  - `TestSetRoutesLifecycle`（engine 真内核）：AllowedIPs∪路由就位/收缩/幂等/冲突跳过/Down 零残留。
  - `TestConsumerRoutes`（routerd e2e）：≤1 对账周期可达（SC-002 真流量）→**排除自己**（无回环）→`list --all` 区分 MINE/(this)/routed→撤回 ≤1 周期收敛→零残留。
- **剩余人工项（SC-005 功能链最终收口）**：§1–§3 实机——真 Windows 客户端经替身地址访问真 OpenWrt 路由器背后的 LAN 设备；通过后**人工勾销 TODO.md「设备路由宣告到区域」**。

## 1. Windows 成员拨通宣告子网（US1，实机）

前置：真 OpenWrt 路由器已宣告 LAN（032 quickstart §1）；Windows 客户端与路由器同 zone。

- [ ] Windows 连接后（≤60s）面板「可达子网」出现该宣告（替身/真实/zone/宣告者）
- [ ] 直接访问替身地址（如 `100.100.X.50` 的 NAS Web 页）成功——**TODO「设备路由宣告到区域」凭此勾销**
- [ ] 路由器撤回宣告 → ≤60s 面板条目消失、访问超时（无需重连）
- [ ] 再次宣告 → ≤60s 恢复可达（无需重连）
- [ ] 断开 → `route print` 无残留替身路由；重连自动恢复

## 2. OpenWrt 消费端（US2，实机）

- [ ] 两台路由器同 zone 互宣告：各自 ≤60s 能访问对方替身地址；`announce list --all` 区分 MINE yes/no
- [ ] 自己宣告的替身段不出隧道（`ip route` 无对应隧道路由）

## 3. 冲突与失败（FR-003/005，实机抽查）

- [ ] 给 Windows 机器手工配一个与某替身段重叠的网卡地址 → 该条标注冲突跳过、其余照常；删除后 ≤60s 自动补上
- [ ] 断网服务器 → 两端路径冻结不抖动；恢复后收敛

## 4. 自动化回归（CI 维度）

```bash
gofmt -l . && go vet ./... && staticcheck ./...
unshare -rUn bash -c 'ip link set lo up && go test ./...'
```

- [ ] tunnel 集成（wireguard-go netns）：SetExtraRoutes 热更新不致重握手、allowed_ips/系统路由就位与撤除、幂等、单条冲突跳过、**真流量访问合成地址往返**
- [ ] routerd 消费 e2e（032 装具演进）：≤1 周期可达/撤回收敛/排除自己/视图区分
- [ ] 聚合双视图纯函数、路由差分纯函数
- [ ] 031/032/Windows/服务端全量零回归

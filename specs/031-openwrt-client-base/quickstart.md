# Quickstart: OpenWrt 客户端基础 (031)

> 验收/冒烟步骤（宪法 II acceptance 层）。CI 自动化覆盖 netns 维度；本矩阵的实机部分在
> 一台真实 OpenWrt（arm64 或 mipsle 任一）上人工执行（017/018 实机豁免先例）。

## 执行记录（2026-06-11，实现期）

- §0 ✅：`make routerd-cross` 三目标产出（amd64/arm64/mipsle softfloat 静态 ELF，`file` 验证）。
- §6 ✅：lint 全清；CI 同款全量套件三轮全绿 + 重型包 ×3 重复绿（实现期根除一处测试竞态：测试服务器 wg 端口移出内核临时端口范围）。
- §1–§5 的 CI 等价物已由自动化承载：`TestCLIOnboardAndTunnel`（TOFU 指纹流/三件套 0600/0700/platform=openwrt/真内核握手 <3s/守护重启自愈/重复 register 拒绝/**零机密输出断言**）、`TestCLIZones`（创建自动入组/双设备成员表/错误密码不可枚举/status 五字段/leave）、`TestCLILogout` 三态（远端注销+RT 吊销+本地清盘 / 不可达阻止 / --force 警告）。
  - 注：CI 中「ping 通服务器」以**完成真实 WireGuard 握手**为等价判据（双向可达的密码学证明）；字面 ping 与节点↔节点互 ping 留实机矩阵。
- **剩余人工项**：§1–§5 实机部分（真 OpenWrt 设备：scp 安装、procd enable/reboot 自愈、真 ping、双设备互 ping）。

## 0. 交叉编译（FR-012 / SC-005）

```bash
make routerd-cross
file dist/lanweave-routerd-*   # amd64 / arm64 / mipsle 三个 Linux 可执行文件
```

- [ ] 三目标全部构建成功；mipsle 产物为 softfloat

## 1. 实机接入（US1 / SC-001，计时 ≤10 分钟）

```bash
scp dist/lanweave-routerd-<arch> root@router:/usr/bin/lanweave-routerd
ssh root@router
lanweave-routerd setup --server https://vpn.example.com
# 自签证书时：按提示的指纹执行 lanweave-routerd trust <sha256>
echo -n "$PASSWORD" | lanweave-routerd login --username alice
lanweave-routerd register --name home-router
lanweave-routerd run &   # 或直接走 §4 procd
ping -c3 100.127.0.1     # 服务器 VPN 地址
```

- [ ] 每条命令成功输出明确；服务端 `GET /api/v1/nodes` 显示该节点 `platform=openwrt`
- [ ] `ls -l /etc/lanweave/` state.json 0600；`keys/` 0700 且其下文件 0600
- [ ] ping 通；首次握手 ≤3s（`lanweave-routerd status` 的 last_handshake）
- [ ] 重复 `register` → 报错提示先 logout（不产生第二节点）

## 2. zone 全生命周期（US2 / SC-003）

```bash
echo -n "zonepass-1" | lanweave-routerd zone create homelab
lanweave-routerd zone list && lanweave-routerd zone members homelab
# 另一设备（Windows 客户端或第二台路由器）加入后：
lanweave-routerd zone members homelab   # 看到对方名称/IP/归属
ping -c3 <对方 VPN IP>
lanweave-routerd zone leave homelab
```

- [ ] 创建即自动入组；成员清单字段齐全；错误密码加入 → 「zone 或密码无效」
- [ ] 同 zone 互 ping 通；退出后从成员列表消失
- [ ] `status` 输出五字段（daemon/tunnel/ip/last_handshake/zones）且可 grep

## 3. 自愈与续期（US1-4/US2-5 / SC-002）

- [ ] 杀掉 daemon 进程 → procd 拉起（§4 启用后）→ 隧道恢复，无需人工
- [ ] 服务端重启或断网 >4 分钟后恢复 → daemon 自动重连（日志可见重试），ping 恢复
- [ ] 等待 access token 过期（或调短服务端 jwt_ttl）→ 任意 zone 命令仍成功（静默续期）

## 4. procd（FR-011）

```bash
cp packaging/openwrt/lanweave-routerd.init /etc/init.d/lanweave-routerd
chmod +x /etc/init.d/lanweave-routerd
/etc/init.d/lanweave-routerd enable && reboot
```

- [ ] 重启后无人工干预隧道自动恢复（SC-002）
- [ ] `/etc/init.d/lanweave-routerd stop` → 接口消失（优雅拆除）

## 5. 登出三态（US3 / SC-004）

- [ ] 可达：`logout` → 服务端 node 消失（IP 释放/peer 删/zone 清）、RT 失效、本地三件套清空、接口拆除；重新使用需 onboard
- [ ] 不可达（拔上联线）：`logout` → 非零退出 + 原因，本地完好
- [ ] `logout --force` → 仅清本地 + 警告残留

## 6. 自动化回归（CI 维度，已由测试承载）

```bash
gofmt -l . && go vet ./... && staticcheck ./...
unshare -rUn bash -c 'ip link set lo up && go test ./...'
```

- [ ] 全绿：engine 真接口断言、netns onboard→ping 服务器、zone 生命周期、登出三态、健康循环假引擎单测、state v2→v3 兼容、Windows 路径零回归

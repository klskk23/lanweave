# lanweave —— 实施路线（spec-kit /specify 候选清单）

> 状态：v1 设计冻结，按下表 12 个 feature 逐步切。
> 设计文档：`DESIGN.md`
> 用法：每个 feature 单独 `/specify`，独立 spec / plan / tests / implementation。
> 顺序按依赖排，原则上前置 feature 完成后再开下一个。

---

## 切片总览

| #   | Feature 名                              | 类别       | 依赖       | 端到端可验证                        |
|-----|-----------------------------------------|------------|------------|-------------------------------------|
| 001 | server-foundation ✅                     | 服务端     | —          | 服务起来，admin 入库，HTTPS 200      |
| 002 | invites-and-user-auth ✅                 | 服务端     | 001        | 邀请码 → 注册 → 登录 → JWT          |
| 003 | wireguard-server-interface ✅            | 服务端     | 001        | wg 接口 up，nft 空 table 就绪        |
| 004 | node-registration-and-ipam              | 服务端     | 002, 003   | 用户注册 node → 拿到 IP → WG peer 在 |
| 005 | zones-and-nftables-isolation            | 服务端     | 004        | 同 zone 互通，跨 zone drop          |
| 006 | zone-owner-controls                     | 服务端     | 005        | owner 改密 / 踢人 / 删 zone 生效     |
| 007 | node-online-status                      | 服务端     | 004        | 客户端连接 → online，3min 静默 → offline |
| 008 | cascade-deletes                         | 服务端     | 005        | admin 删用户 → 所有 state 清干净     |
| 009 | windows-client-skeleton-and-wizard      | 客户端     | 002, 004   | 首次启动 wizard 走完，本机 node 落地 |
| 010 | windows-client-tunnel                   | 客户端     | 009, 003   | 客户端拉起 WG，能 ping 服务器         |
| 011 | windows-client-main-panel               | 客户端     | 005, 010   | UI 完成所有 node/zone 管理操作        |
| 012 | deployment-packaging                    | 运维       | 008, 011   | .deb 一键装、Windows installer 一键装|

---

## 各切片详情

### 001 — server-foundation
**范围**
- TOML 配置加载（`[server] [wireguard] [nftables] [auth] [admin]`）
- SQLite 打开 + 迁移框架（如 `goose` / `golang-migrate`）
- 结构化日志（`slog` → stdout/journald）
- HTTPS 服务骨架 + `/api/v1/healthz`
- admin bootstrap：TOML 首位 admin 明文密码 → argon2id hash 入库
- 全局 `rate.Limiter` 中间件框架（默认很宽松，后续 feature 不再碰）

**验收**
- `lanweaved` 二进制干净启动；DB 文件出现；admin 用户在 users 表存在且密码已 hash；`curl -k https://host/api/v1/healthz` → 200。

**不做**
- 业务 API、WG、nftables、客户端、打包。

---

### 002 — invites-and-user-auth
**范围**
- `invites` 表 + admin 端点 `POST /admin/invites`、`GET /admin/invites`
- `POST /api/v1/register`（需 invite_code）
- `POST /api/v1/login` → JWT（1–2h，HS256）
- JWT 验证中间件 + `GET /api/v1/me`
- 失败计数：**仅全局 rate.Limiter**（不做账户级）

**验收**
- admin token 调 `POST /admin/invites` 拿到 code；新用户用 code + username/password 注册成功；登录拿 JWT；调 `/me` 返回当前用户。

---

### 003 — wireguard-server-interface
**范围**
- 启动时创建或恢复 `wg-lanweave` 接口（`wgctrl-go` + netlink）
- 服务端私钥：首次生成 → `/var/lib/lanweave/wg_private`（chmod 600）；后续读现存
- 服务端 IP = 池第一个地址
- 启用 `net.ipv4.ip_forward`
- nft 专用 table `inet lanweave` 初始化（启动时全量重建 + 空 forward 链 policy drop）

**验收**
- `ip a` 看到 `wg-lanweave` 网卡，地址 = 100.127.0.1（按配置）
- `wg show` 看到接口配置，无 peer
- `nft list table inet lanweave` 出现，forward 链 policy drop

---

### 004 — node-registration-and-ipam
**范围**
- `nodes` 表 + IPAM（事务内 `SELECT MIN(unused)`，回收，并发安全）
- `POST /api/v1/nodes` body: `{name, wg_pubkey}` → `{id, ip}`
- `DELETE /api/v1/nodes/{id}`
- `GET /api/v1/nodes`（名下所有 node）
- `GET /api/v1/server`（返回 WG 服务端 pubkey、endpoint、network、MTU）
- 创建/删除 node 时联动 `wgctrl` 增/删 peer

**验收**
- 用户 POST `/nodes` → 拿到 IP 与 `/server` 信息
- `wg show` 出现新 peer，公钥与上传一致
- 第二个 node 取下一个 IP
- 删除一个 node 后再创建，复用最低空闲 IP
- `wg show` 中对应 peer 同步消失

---

### 005 — zones-and-nftables-isolation
**范围**
- `zones`、`zone_members` 表
- `POST /api/v1/zones`（创建，调用者成 owner）
- `POST /api/v1/zones/{name}/join`（body: node_id + password）
- `POST /api/v1/zones/{name}/leave`（body: node_id）
- `GET /api/v1/zones`（我所在的 zone）
- `GET /api/v1/zones/{name}/members`
- nft `set zone_<id>` + `rule ip saddr @z and ip daddr @z accept` 增量管理
- 启动时从 SQLite 全量重建 set / rule

**验收**
- 两个 node 加入同 zone → 互 ping 通
- 不在同 zone 的两个 node → ping 不通
- 服务重启，nft 自动恢复到一致状态
- 同一 node 加入多个 zone，IP 出现在多个 set

---

### 006 — zone-owner-controls
**范围**
- `PATCH /api/v1/zones/{name}` body: `{password}` 改密（**不踢老人**）
- `DELETE /api/v1/zones/{name}/members/{node_id}` 踢
- `DELETE /api/v1/zones/{name}` 删整个 zone（释放 name）
- 鉴权：非 owner 调用一律 403

**验收**
- owner 改密码后老 member ping 仍通；新用户用旧密码 join 失败、用新密码成功
- owner 踢某 member → 该 node 立即从 set 移除，互通断
- owner 删 zone → set/rule 销毁，所有 member 关系清除，name 释放可被新建

---

### 007 — node-online-status
**范围**
- 后台 goroutine 每 30s 拉 `wgctrl` 所有 peer 的 `last_handshake_time`
- 写入 `nodes.last_handshake_at`（可选缓存）
- `GET /api/v1/nodes` 返回 `online: bool`（`now - last_handshake < 3min`）

**验收**
- 客户端连上（带 PersistentKeepalive=25）后 API 返回 `online: true`
- 客户端断网 3min 后 API 翻 `false`
- 重连后 30s 内恢复 `true`

---

### 008 — cascade-deletes
**范围**
- `DELETE /api/v1/admin/users/{id}`
- 完整级联（单一事务 + 紧随的 nft/wg 同步）：
  - 用户 → 名下 nodes → IP 释放 → wg peer 删除 → nodes 从 zone_members 移除 → 对应 set 元素删除
  - 用户 owner 的 zones → 一并删 → set/rule 销毁 → 所有 member 关系清除

**验收**
- 删 admin/普通用户后：DB 中无残留行
- `wg show` 无该用户任何 peer
- `nft list table inet lanweave` 中所有相关 set/rule 干净
- 被释放的 IP 能被新 node 复用
- owner 的 zone 即使有别人的 node 也被一并删除

---

### 009 — windows-client-skeleton-and-wizard
**范围**
- Fyne 桌面项目骨架（`cmd/lanweave-client`）
- REST client（HTTPS、JWT 头、错误处理）
- Windows credential manager 封装（DPAPI）
- 首次启动 wizard：
  1. 输入 server URL
  2. 已有账号登录 / 新账号注册（输 invite_code）
  3. 输入 node 名 → 本地生成 WG 密钥对 → POST `/nodes`
  4. 私钥写 keyring；公钥/IP/server pubkey/endpoint 写本地状态文件
- 本地状态文件路径（如 `%LOCALAPPDATA%\lanweave\state.json`）
- 高级 CLI flag `--insecure`（跳过证书验证，不在 UI 暴露）

**验收**
- 干净 Windows 上首次启动 → 走完 wizard → 状态文件落地、私钥进 keyring
- 重启客户端不再要求注册，直接进入主面板（UI 暂占位）

---

### 010 — windows-client-tunnel
**范围**
- 嵌入 `wireguard-go` + WinTun 驱动（打包 .dll/.sys）
- 用 keyring 私钥 + 状态文件 peer 信息组装 WG 配置
- 启动/停止隧道；UAC 提权（创建虚拟网卡需管理员）
- `PersistentKeepalive = 25`
- UI 上一个"连接 / 断开"按钮

**验收**
- 点连接 → `ipconfig` 出现 100.127.x.y 网卡
- 能 ping 服务器 100.127.0.1
- （需 005 + 加入同 zone）能 ping 其他 node
- 点断开 → 网卡消失

---

### 011 — windows-client-main-panel
**范围**
- 主面板 Fyne 视图：
  - 顶部：当前 node 状态（IP、连接开关、上次握手）
  - "我的 nodes" 标签：列名下所有 node，标注哪个是本机
  - "我的 zones" 标签：列本 node 所在 zone，展开见 member 列表
  - "加入 zone" 按钮：输入 name + password
  - "创建 zone" 按钮：输入 name + password
  - 仅本人 owner 的 zone 上显示：改密 / 踢人 / 删 zone 按钮
- 在线状态从 `GET /nodes` 取并轮询刷新

**验收**
- 端到端：UI 完成创建 zone、加入他人 zone、离开、（owner 视角）改密 / 踢人 / 删 zone，无需任何 curl
- 验证视野：member 能看 zone 内全员名字 + IP

---

### 012 — deployment-packaging
**范围**
- 服务端 `.deb` 包：`nfpm` 或 `dpkg-deb`，含 `/usr/bin/lanweaved`、`/etc/lanweave/config.toml.example`、`/lib/systemd/system/lanweaved.service`
- systemd unit：`User=root`、`CapabilityBoundingSet=CAP_NET_ADMIN`、`Restart=on-failure`、`StandardOutput=journal`
- Windows 客户端：NSIS 或 WiX 打包为 `.msi`/`.exe` installer，含 WinTun 驱动
- 安装后约定路径：`/etc/lanweave/config.toml`、`/var/lib/lanweave/db.sqlite`、`/var/lib/lanweave/wg_private`、Windows 侧 `%LOCALAPPDATA%\lanweave\`

**验收**
- 干净 Debian 12 上 `dpkg -i lanweave_*.deb` → `systemctl status lanweaved` 显示 active
- 干净 Windows 10/11 上跑 installer → 桌面图标，启动客户端走 wizard 成功
- 卸载干净，残留可控（运维数据/keyring 保留 vs 删除有明确策略）

---

## 已知非范围（v1 不切，留 v1.1+）

- ownership 转让（zone 换 owner）
- 账户级失败计数 / 锁定
- Let's Encrypt 自动续签集成
- macOS / Linux 桌面客户端
- WebSocket 推送在线/事件
- 软删与审计日志保留
- 客户端跨设备同步 node（"一个账号一份 node 配置"）

---

## 工作建议

1. 每个 feature 跑 `/specify <feature-name>` 生成完整 spec / plan / tasks。
2. 严格按本表顺序，前置未完不开下一个，避免半成品阻塞集成测试。
3. 005 / 008 是高副作用密度（数据库 + nftables + wireguard 三向同步），建议各预留充分测试时间。
4. 009–011 三个 Windows feature 串行做：先骨架（009）→ 隧道（010）→ 主面板（011），不要并行。

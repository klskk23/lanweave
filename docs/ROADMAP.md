# lanweave —— 实施路线（spec-kit /specify 候选清单）

> 状态：v1 设计冻结，按下表 12 个 feature 逐步切；013 为部署联调发现的加固修复，014 为 CI/CD 自动化，015 为创建 zone 自动入组的体验完善，016 为 Windows 客户端图标补齐，017 为客户端退出登录 + insecure-TLS 可交互，018 为客户端防火墙控制 + TOFU 证书钉扎（取代 017 会话级 insecure），019 为修复 onboarding 会话 token 未落盘导致进面板二次要求登录，020 为客户端 GUI 中文/英文双语（汉化），021 为服务端可选明文 HTTP 监听（供反向代理终止 TLS），022 为客户端主页面与 Wizard 视觉改版（按 `UI-DESIGN.md`/`UI-example.png`，Material 3 启发的深色 flat 风格）；023 为每用户设备 / 拥有-zone 配额上限（配置文件全局值，默认各 10，0=无限制，admin 豁免）；024 为会话 refresh token（登录发 access+refresh，access 2h 过期客户端用 refresh token 静默换新，不再每 2h 重输密码；RT 30天滑动、服务端存哈希可吊销）；025 为退出登录加固（服务器网络不可达时阻止退出以避免残留孤儿 node，含「强制退出」逃生口，登出吊销本设备 refresh token）；026 为邀请码有效期（admin 邀请码默认 24h 过期，由配置 `invite_ttl` 控制，`0`/空=永不过期；旧码祖父化永久有效；过期码注册被拒并归入通用「邀请码无效」）。
> 设计文档：`../DESIGN.md`
> 用法：每个 feature 单独 `/specify`，独立 spec / plan / tests / implementation。
> 顺序按依赖排，原则上前置 feature 完成后再开下一个。

---

## 切片总览

| #   | Feature 名                              | 类别       | 依赖       | 端到端可验证                        |
|-----|-----------------------------------------|------------|------------|-------------------------------------|
| 001 | server-foundation ✅                     | 服务端     | —          | 服务起来，admin 入库，HTTPS 200      |
| 002 | invites-and-user-auth ✅                 | 服务端     | 001        | 邀请码 → 注册 → 登录 → JWT          |
| 003 | wireguard-server-interface ✅            | 服务端     | 001        | wg 接口 up，nft 空 table 就绪        |
| 004 | node-registration-and-ipam ✅           | 服务端     | 002, 003   | 用户注册 node → 拿到 IP → WG peer 在 |
| 005 | zones-and-nftables-isolation ✅         | 服务端     | 004        | 同 zone 互通，跨 zone drop          |
| 006 | zone-owner-controls ✅                   | 服务端     | 005        | owner 改密 / 踢人 / 删 zone 生效     |
| 007 | node-online-status ✅                    | 服务端     | 004        | 客户端连接 → online，3min 静默 → offline |
| 008 | cascade-deletes ✅                       | 服务端     | 005        | admin 删用户 → 所有 state 清干净     |
| 009 | windows-client-skeleton-and-wizard ✅   | 客户端     | 002, 004   | 首次启动 wizard 走完，本机 node 落地 |
| 010 | windows-client-tunnel ✅                | 客户端     | 009, 003   | 客户端拉起 WG，能 ping 服务器         |
| 011 | windows-client-main-panel ✅            | 客户端     | 005, 010   | UI 完成所有 node/zone 管理操作        |
| 012 | deployment-packaging ✅                 | 运维       | 008, 011   | .deb 一键装、Windows installer 一键装|
| 013 | windows-client-elevation ✅            | 客户端     | 010, 012   | 双击快捷方式启动即弹 UAC，建网卡成功  |
| 014 | ci-cd-build-and-release ✅              | 运维       | 012, 013   | 打 v* tag → 测试门禁 → 出 .deb+安装器 → 自动建 draft Release |
| 015 | zone-create-auto-join ✅                | 客户端/服务端 | 005, 011   | 创建 zone 后本机 node 自动入组,无需二次 join         |
| 016 | windows-app-icon ✅                      | 客户端/运维 | 014        | EXE / Fyne 窗口 / 安装器 / 卸载器 / ARP 5 处均显示 lanweave 图标 |
| 017 | client-logout-and-tls-optin ✅          | 客户端     | 004, 011   | 退出登录(断连 + 注销本机 node + 回填服务器 URL);证书错误时弹窗可选「继续(不安全)」,会话级不持久 |
| 018 | client-firewall-and-tofu-pin ✅          | 客户端     | 010, 017   | 客户端可开关入站防火墙规则放行 VPN 网段(默认关);证书首次信任后钉扎(TOFU),取代 017 会话级 insecure |
| 019 | client-session-persist-fix ✅           | 客户端     | 009, 011   | 向导登录/注册完成后直接进面板,不再二次要求登录;冷启动复用已缓存会话 |
| 020 | client-i18n ✅                           | 客户端     | 009, 011   | 客户端 UI 中/英双语,启动按系统语言,可手动切换(重启生效) |
| 021 | server-http-mode ✅                      | 服务端     | 001        | 服务端可配 tls=false 监听明文 HTTP(供反代终止 TLS);默认仍 HTTPS,缺 cert 仍硬失败,现存配置不降级 |
| 022 | client-ui-redesign                      | 客户端     | 010, 011, 020 | 主页面+Wizard 按 UI-DESIGN.md/UI-example.png 重做(Material 3 深色 flat):自定义主题/App Bar+overflow/Hero 卡片/列表 avatar+状态点/Switch/区域详情页/流量计数;Wizard 仅套主题,客户端行为零回归 |
| 023 | per-user-limits ✅                       | 服务端/客户端 | 002, 004, 005, 020 | 每用户设备数 + 拥有-zone 数配额(配置全局值,默认各 10,0=无限,admin 豁免);超限 409,删除释放名额,下调上限只挡新增 |
| 024 | session-refresh-tokens                   | 服务端/客户端 | 002, 009, 019 | 登录返回 access + refresh token;access 2h 过期时客户端用 refresh token 静默换新,不再每 2h 弹密码;RT 30天滑动、服务端存哈希可逐条吊销 |
| 025 | client-logout-hardening                  | 客户端     | 017, 024   | 退出登录时服务器 API 网络不可达(3 次 1s 重试仍失败)则阻止退出 + 弹窗(取消 / 强制退出逃生口),避免残留孤儿 node;登出额外吊销本设备 RT |
| 026 | invite-expiry                            | 服务端     | 002        | 邀请码有有效期:配置 `invite_ttl` 全局默认 24h,建码时写 `expires_at=created_at+ttl`;`0`/空 与旧码=NULL=永不过期;过期码注册被拒并归入通用「邀请码无效」 |

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
- 安装后约定路径：`/etc/lanweave/config.toml`、`/var/lib/lanweave/db.sqlite`、`/var/lib/lanweave/wg_private`、Windows 侧 `C:\Program Files\lanweave\`

**验收**
- 干净 Debian 12 上 `dpkg -i lanweave_*.deb` → `systemctl status lanweaved` 显示 active
- 干净 Windows 10/11 上跑 installer → 桌面图标，启动客户端走 wizard 成功
- 卸载干净，残留可控（运维数据/keyring 保留 vs 删除有明确策略）

---

### 013 — windows-client-elevation
**背景**
- 部署联调时发现：登录成功后建隧道报 “could not set up the network adapter”（`tunnel.ErrAdapter`），根因是客户端 exe 从快捷方式启动时是**普通权限**，而创建 WinTun 网卡需要管理员。手动“以管理员身份运行”可绕过。
- 012 在 `cmd/lanweave-client/main.go` 的注释声称安装器附带 `requireAdministrator` manifest 自动提权，但仓库实际**未嵌入任何 manifest**，承诺与实现不符。

**范围**
- 让客户端 exe 启动即以管理员权限运行，二选一实现：
  - 嵌入 `requireAdministrator` manifest（`.manifest` + 资源工具生成 `.syso`，`go build` 自动链接）；或
  - 启动时自检权限，未提权则以 `runas` 重新拉起自身（触发 UAC）后退出。
- 修正 `main.go` 关于 manifest 的失实注释；同步更新 `docs/GUIDE.zh.md` 与 NSIS 构建说明里的提权描述。
- 拒绝 UAC 时行为明确，不静默失败误导用户。

**验收**
- 普通用户从开始菜单/桌面快捷方式**双击**启动 → 自动弹 UAC → 同意后程序以管理员运行；连接隧道，`ipconfig` 出现 100.127.x.y 网卡，能 ping 服务器。
- 无需手动“以管理员身份运行”。
- 用户拒绝 UAC 时有可理解的结果（不报错成功、不假装已连）。

**不做**
- 非 Windows 平台（无此问题）。
- 建网卡固有的管理员需求本身（无法降权规避）。

---

### 014 — ci-cd-build-and-release
**背景**
- 仓库已有 GitHub 远端（`klskk23/lanweave`，public）。手工编译服务端 `.deb` 与 Windows 客户端、再手动发布，既慢又易漏步骤、版本易对不上。把编译/测试/打包/发布做成 GitHub Actions，产物自动挂到 Release。

**范围**
- **触发与版本**：`release.yml` 仅 `v*` tag 触发；版本 = tag 去前导 `v`（`vX.Y.Z`→`X.Y.Z`），作为唯一来源喂给 nfpm `VERSION`、Windows `-ldflags -X main.version`、各产物文件名；tag 含 pre-release 后缀（如 `-rc1`）→ Release 标 `prerelease`。
- **测试门禁（Linux job，先过才构建）**：lint（gofmt / go vet / staticcheck）+ `unshare -rUn go test ./...`（真 SQLite/nftables/WireGuard，禁 mock，符合 Principle II）+ 打包测试（nfpm / dpkg-deb / fakeroot）。
- **构建 .deb（ubuntu job）**：`make deb` → 上传 artifact。
- **构建 Windows 客户端（`windows-latest` 原生 job）**：`setup-go` + cgo gcc（mingw）+ `choco install nsis`；下载 `wintun-0.14.1.zip` 并校验 SHA256 → 取 `bin/amd64/wintun.dll`；`go build -tags gui` → `makensis`（OutFile 带版本）→ 另打便携 zip → 上传 artifacts。
- **发布（ubuntu job，`permissions: contents: write`，用自动 `GITHUB_TOKEN`，无需 PAT）**：汇总所有 artifact → 生成 `SHA256SUMS` → 建 **draft** Release（自动 release notes，按 tag 判 prerelease）。
- **资产**：`lanweave_<ver>_amd64.deb`、`lanweave-client-<ver>-setup.exe`、便携 zip（`lanweave-client.exe`+`wintun.dll`）、`SHA256SUMS`。
- **普通 CI（`ci.yml`）**：push / PR 触发跑 lint + `unshare -rUn go test ./...`，保持 main 常绿。

**验收**
- push 一个 `v0.1.0` tag → Actions 跑完测试门禁 → 在 Release 看到 draft，挂着 `.deb` + 安装器 + 便携 zip + `SHA256SUMS`，文件名均带 `0.1.0`。
- 测试失败时不产出 Release（门禁有效）。
- `ci.yml` 在 PR 上跑测试并能拦红。

**不做 / 暂缓**
- 代码签名（Windows Authenticode、`.deb` GPG）——v1 不签，发布说明写明 SmartScreen 提示。
- arm64（deb / Windows 均仅 amd64）。
- Windows GUI/UAC/建网卡的自动化测试（已登记手动例外，CI 只 build 不测）。

**发布前 TODO（不阻塞搭流水线）**
- 确认 Wintun 预编译二进制再分发许可（WireGuard LLC）允许打进 installer。
- 把 `wintun-0.14.1` 官方 SHA256 填入 workflow。

---

### 015 — zone-create-auto-join
**背景**
- 现状：`POST /api/v1/zones`（创建）与 `POST /api/v1/zones/{name}/join`（入组）是两步。用户在主面板「创建 zone」后,自己并不在该 zone 里,还得再点「加入 zone」、重输密码才成为成员,反直觉。
- 期望：创建者的当前设备节点在创建的同一操作内直接成为成员。

**范围**
- **协议**：`CreateZoneRequest` 增加可选字段 `node_id`（0/省略 = 旧的「只建不入」行为,向后兼容）。
- **服务端**（`createZone`,原子语义）：若 `node_id != 0` —— **先**校验节点归属（复用 `Nodes().GetOwned(userID, node_id)`,不属于则 404 且不建 zone）→ 建 zone → `AddZone` → `Zones().Join` + nft 把该节点 IP 加进 zone set;建 zone 之后任一步失败,复用现有「删 zone + 级联 + nft 清理」补偿路径(不引入新 SQL 事务)。不变量:带 `node_id` 时结果要么{zone 建好 + 创建者已入组 + nft 一致},要么{什么都没建 + 报错}。
- **客户端**：`apiclient.CreateZone` 增 `nodeID` 形参写进请求体;`panel.Controller.CreateZone` 内部解析当前设备 node id 并传入(复刻现有 JoinZone 解析);GUI 创建永远自动入组,无 opt-out 勾选;「加入 zone」按钮保留(用于加入他人 zone);建完刷新成员列表到能看到自己即达标,不额外弹提示。

**验收**
- 主面板创建 zone 后,无需任何额外操作,成员列表立即包含本机 node。
- 服务端集成测试(真 nft,`unshare -rUn`)：带合法自有 `node_id` 建 zone → 节点是成员且 nft zone set 含其 IP;带他人 `node_id` → 拒绝且 DB/nft 无残留;不带 `node_id` → 旧行为(建好、无成员)。
- 现有不带 `node_id` 的 `CreateZone` 调用与测试保持绿(向后兼容)。

**不做**
- 改动「加入他人 zone」流程(仍走 join,需密码)。
- nft 中途失败→补偿删 zone 的确定性注入测试(不好可靠触发,与现有同类补偿路径未单测保持一致)。

---

### 016 — windows-app-icon
**背景**
- 014 把 Windows 客户端 + NSIS 安装器自动化打包出来后,实战发现五个用户感知的图标位置(EXE 文件资源、Fyne 运行时窗口、安装器 EXE、卸载器 EXE、控制面板「添加删除程序」)全是 Windows 默认空白,品牌识别为零。
- `packaging/icon.svg`(256×256 矢量,深蓝底 + 青色花纹)已就位,缺一条「SVG → 多分辨率 ICO/PNG → 嵌入 EXE/Fyne/NSIS」的稳定路径。

**范围**
- **SVG → 栅格**:新增 `packaging/scripts/gen-icons.sh`,用 `rsvg-convert`(librsvg)+ `icotool`(icoutils)产 `packaging/icon.ico`(含 16/32/48/256 四张)+ `internal/client/ui/icon.png`(256×256,给 Fyne)。两份产物入仓。
- **EXE 资源图标(W1 windres)**:复用 014 已装的 mingw 工具链,新增 `windres` 一步从 `packaging/windows/icon.rc`(3 行)+ `icon.ico` 生成 `cmd/lanweave-client/resources_windows.syso`,`go build` 自动链接。SYSO 不入仓(`.gitignore`),`make icons` 即时生成,`make client` 通过 Makefile 依赖防遗漏。
- **Fyne 窗口图标**:`internal/client/ui/icon.go` 用 `//go:embed icon.png` 嵌字节,导出 `AppIcon() fyne.Resource`;`cmd/lanweave-client/main.go` 在 `app.NewWithID` 后、`a.NewWindow` 前调一行 `a.SetIcon(ui.AppIcon())`(跨平台生效,Linux/Mac GUI 也带,接受)。
- **NSIS 安装器/卸载器 + ARP 元数据**:`lanweave-client.nsi` 加 `Icon "icon.ico"`、`UninstallIcon "icon.ico"`;Uninstall 注册表段补 `DisplayIcon "$INSTDIR\${EXE}"`、`DisplayVersion`、`Publisher`。`.nsi` 接收 `/D VERSION=...`,`release.yml` `makensis` 调用传入。
- **修订 013 research.md**:本期为图标引入 `.syso` 工具链,推翻 013 Decision 1 「无新构建工具」原则;运行时 `runas` 抬升路径不动,research.md 追加一节修订记录。
- **CI**:`release.yml` Windows job 增装 librsvg / icoutils 的 chocolatey 等价物;Build client step 改为 `make icons && go build`。

**验收**
- CI `release.yml` 打 `vX.Y.Z` tag 跑完出包,人工 verify 矩阵(写在 016 的 `quickstart.md`)逐一对 5 个位置肉眼检查:安装器 EXE 图标 / 装好后 `lanweave-client.exe` 文件图标 / Start menu 与桌面快捷方式 / 运行客户端时 Fyne 窗口标题栏 + Windows 任务栏 / 控制面板「添加删除程序」里 lanweave 那行图标,全部显示 lanweave 图标。
- `make icons` 在 Linux / Windows 上重生成产物,产出可被 `file` 识别为合法 PNG/ICO/COFF,可读 SVG diff 与新 PNG 渲染肉眼一致。
- `internal/client/ui/icon_test.go` 单测(无头,无 GUI tag)断言 `AppIcon().Content()` 非空 + 前 8 字节为 PNG magic `89 50 4E 47 0D 0A 1A 0A`。
- `unshare -rUn go test ./...` 仍全绿(本期不动 SQLite/nftables/WireGuard,宪法 II 不受影响)。

**不做**
- NSIS Modern UI 2 升级(welcome / finish 页 + welcome 宣传图)——另一项目。
- 代码签名 / Windows Authenticode(沿用 014 暂缓策略)。
- 自动化 PE 资源段解析校验(5 个位置人工肉眼覆盖,ROI 低)。
- SVG 改动后强制 CI 重生成 ICO/PNG 的防漂移钩子(信任 review)。
- `gen-icons.sh` 产物哈希校验(`rsvg-convert` 跨版本字节不稳,反而引入「换版本就 fail」)。

---

### 017 — client-logout-and-tls-optin
**背景**
- 011 主面板交付后，客户端缺两块体验：(1) 没有「退出登录」——onboard 一旦完成就绑死在某台服务器/账号，想换服务器只能手动清 state.json/keyring；(2) 证书校验失败（自签 / 内网 CA）时，`--insecure` 只能命令行传入（009 / DESIGN §275「不在 UI 暴露」），普通用户在 GUI 里撞上 `ErrUntrustedCert` 就卡死、无路可走。
- 原 017 还含「删除 node」，grill 阶段判定其与「退出登录」对本机的处理大面积重叠、独立价值有限，已剔除；代价：够不着的旧 / 丢失设备 node 无法远程撤销，作为**接受的限制**记录。

**范围**
- **退出登录**：
  - Home 面板账户 / 设置区（或右上溢出菜单）新增「退出登录」按钮，远离主操作防误点。
  - 点击 → **普通确认弹窗**，文案点明「断开连接 + 移除本设备在该服务器的节点 + 需重填服务器地址」。
  - 确认后原子执行：拆 WG 隧道 → 调 `DELETE /api/v1/nodes/{id}` 注销本机 node → 清 keyring（session token + 设备私钥）→ `state.Clear()` → 回到 wizard `stepServer`（可重填服务端 URL）。
  - `apiclient` 新增 `DeleteNode`（仅 logout 内部调用）；`panel` 的 `api` interface 同步加。
  - 注：删 node 既已剔除，logout 的注销即**唯一 node 清理路径**，故必须「注销」而非「留 node」（留则每次退登造一个无人可清的孤儿）。
- **insecure-TLS 可交互（被动 opt-in，非常驻勾选）**：
  - wizard 与 Home 两处：连接命中 `ErrUntrustedCert` 时弹窗「证书无法验证，是否仍继续?（不安全）」；用户确认才以 `WithInsecure()` 重建该会话 client 重试。
  - **不持久化**：insecure 仅当前进程内存生效，不写 state.json；重启回安全默认。
  - 保留 `--insecure` CLI flag（命令行传入则一开始即跳过校验，行为不变）。
  - 处于 insecure 会话时，Home 常驻「⚠ 证书未验证」提示条。
- **修订 DESIGN.md（同 PR）**：§275、§360（§11 风险登记）解除「忽略证书验证不在 UI 暴露」字面禁令，改记新口径——UI 仅被动触发、需显式确认、会话级不落盘、常驻警告，保留「防无脑勾选」精神。

**验收**
- 退出登录：Home 点退出 → 确认 → 隧道断（`ipconfig` 网卡消失）、服务端 `wg show` / DB 中本机 node 消失、keyring 清空、回到填服务器 URL 的 wizard 首步；重走 wizard 可注册到同一或不同服务器。
- 服务端集成测试（真 nft/wg/SQLite，`unshare -rUn`）：logout 调 DeleteNode 后该 node 的 IP 释放、wg peer 删除、zone set 元素清除（复用 008 级联路径）。
- insecure 交互：对自签服务器，wizard/Home 撞证书错误 → 弹窗 → 选继续 → 连接成功且出现「⚠ 证书未验证」；不选则连接失败、不静默放行；重启后再连默认仍校验（未持久）。
- `panel.Controller` + fake `api`（HTTP 边界）headless 测覆盖 logout 编排与证书弹窗判定；GUI 弹窗 / 提示条 `//go:build gui`，Windows 手工核对。

**待 plan 阶段确认**
- 服务端 `DELETE /api/v1/nodes/{id}` 是否已实现 + owner 校验（只能删自己的 node）。
- 客户端 WG 隧道 teardown 入口（对称于 013 提权建网卡路径）。

---

### 018 — client-firewall-and-tofu-pin
**背景**
- 017 交付后留两处可改进：(1) Windows 客户端起隧道后，本机对同 zone 对端是否暴露由 Windows Defender 默认入站策略决定，缺一个用户可控的「放行 VPN 网段入站」开关；(2) 017 的 insecure-TLS 是会话级、每次启动撞证书错误都要重新弹窗确认，对自签 / 内网 CA 的长期用户反复操作烦，体验差。
- 本切片做两件强相关的事（**共用一次 state schema 迁移**），并显式 supersede 017 的 insecure 相关需求。

**范围**
- **客户端防火墙控制（默认关）**：
  - 规则形态：仅入站的 Windows Defender 防火墙规则，`remoteip=100.127.0.0/16`、全端口 + ICMP、`profile=any`，经 `netsh advfirewall firewall add rule` 下发；**仅 Windows**（Linux / 其它平台 no-op）；默认 OFF。
  - 生命周期：规则存在 ⟺ (开关 ON ∧ 隧道 Connected)。连接成功且开关 ON 时加；已连接时拨 ON 立即加；断开 / 拨 OFF / 登出 / 退出程序时删。
  - 防残留：**具名规则**（如 `lanweave-vpn-inbound`）+ **幂等加**（加前先按名删）+ **启动清扫**（启动时按名删一次，清掉上次崩溃的孤儿）。规则下发逻辑挂在 connect/disconnect 与开关回调上，`netsh` 执行放接口 seam 后面以便无头测决策逻辑。
  - UI：主面板 footer 一个 `widget.Check`「Allow inbound from VPN peers (100.127.0.0/16)」；拨 ON 即落库 + 应用，拨 OFF 即落库 + 删，均静默；旁边一行**内联警告标签**说明「开启会让同 VPN 网段(zone)内对端访问本机所有本地服务 / 端口」（不弹确认框）。
- **TOFU 证书钉扎（取代 017 会话级 insecure）**：
  - 首次连某 server，证书过不了系统 CA 时弹 TOFU 提示「在本设备信任此证书?」；接受 → 存**叶证书 SHA-256 指纹**进 state.json，后续静默通过。
  - 验证规则：证书通过 ⟺ (指纹 == 已钉指纹) **或** (过系统 CA)。按 server 钉；切换 server URL 重新走 TOFU。
  - 证书变更：新增 typed error `ErrCertChanged`，弹更重的「证书已变更」警告；接受则覆盖旧钉。
  - 指示器两分：TOFU 已钉（自签但已信任）→「self-signed (trusted on this device)」中性提示；`--insecure` → 沿用「⚠ certificate not verified」警告条。
  - 保留 `--insecure` CLI flag（完全不验证的逃生口）。
  - **取代** 017 的 FR-009 / 010 / 011 / 013 / 014（反应式会话级 opt-in）。
- **state schema 迁移（把两件事绑成原子单元）**：一次 `SchemaVersion 1→2`，同时加 `PinnedCertSHA256 string`（空 = 未钉）+ `FirewallAllowVPN bool`（false = 关）；旧 v1 记录加载时两者取零值（未钉 + 防火墙关），无缝。
- **修订 DESIGN.md（同 PR）**：§275 / §360 从「反应式会话 opt-in」改为「TOFU 持久信任」；补「客户端可添加入站规则放行 VPN 网段」一条；§11 风险登记：开启防火墙开关暴露本机服务给同 zone 对端（已接受、默认关、用户可控）。

**验收**
- 防火墙：Windows 上连接后拨 ON →（管理员）`netsh advfirewall firewall show rule name=lanweave-vpn-inbound` 出现该规则（remoteip=100.127.0.0/16、入站）；拨 OFF / 断开 / 登出 / 退出 → 规则消失；崩溃后重启 → 启动清扫删掉残留再按状态重建；开关状态重启后保持（持久化）。
- TOFU：对自签 server，首连弹 TOFU 提示 → 接受 → 连接成功且 state.json 出现指纹；重启再连**不再弹窗**（已钉，静默过）；服务器换证书 → 弹「证书已变更」重警告，接受覆盖。
- headless 测（`panel.Controller` + fake `api` / 接口 seam）：logout 仍清理、防火墙「何时加 / 删」决策逻辑、TOFU 指纹钉扎 / 比对、`ErrCertChanged` 路径（httptest 自签走真实 TLS 往返）；旧 v1 state.json 加载迁移到 v2 默认值。
- GUI（Fyne `//go:build gui`）与 `netsh` 实际执行：Windows 手工验收矩阵（类似 017 T021）。

**不做**
- Linux / macOS 桌面防火墙（仅 Windows 有此需求，他处 no-op）。
- 自动信任任何证书 / 关闭 TOFU 首次确认（首次仍需用户显式信任）。
- 防火墙按端口 / 协议细粒度放行（本期只做「全放行 VPN 网段」单开关）。
- 把会话级 `--insecure` 也持久化（TOFU 已替代；`--insecure` 保持每次显式传）。

**依赖 / 关联**
- 依赖 017（复用 panel / state / apiclient，取代其 insecure FR）、010（隧道连接状态驱动防火墙生命周期）。
- 017 的 T021 Windows 手工 GUI 矩阵为独立欠项，与本切片并行补。

---

### 019 — client-session-persist-fix
**背景**
- onboarding（向导登录 / 注册）拿到的 bearer token 只存在向导那个 apiclient 内存里,从不写 keyring。向导结束 `showHome` 新建无 token 的 client → 面板 `start()→LoadSession()` 读 keyring 为空 → 二次弹「Sign in」登录框。注册路径（`Authenticate` 内 Register→Login）同因同果。
- 隐藏面:该 token 实际只在面板手动登录过一次后(`Controller.SignIn`)才进 keyring,故**每次冷启动首登也会弹**,直到登录过一次。一处修复同时消掉向导后弹框与冷启动弹框。

**范围**
- `onboard.Provision()` **全流程成功后**（authenticate → 注册设备 → 存 state 均过）把 `API.Token()` 写入 `keyring.SessionTokenName`,使 token / 设备私钥 / state.json 三者一致落盘。
- onboard 的 `apiClient` interface 增 `Token() string`（`*apiclient.Client` 已实现,补接口声明）。
- `Cleanup()` 追加删除 `SessionTokenName` → 取消 / 失败 onboarding = 彻底空白态（token / 私钥 / state 全清）。
- **不改** `showHome` / 面板:面板 `start()→LoadSession()` 自然从 keyring 读回 token 并经 `Me()` 校验通过,不再弹框（靠 keyring 往返,无需把 client 实例传进去）。

**验收**
- 干净环境走完向导（登录 或 注册）→ 直接进主面板,不再二次要求登录。
- onboard 包无头集成测试（真服务器,复用 `onboard_integration_test.go`）:Provision 成功后 keyring 存在 `SessionTokenName`;Cleanup 后该项不存在。
- 已 onboard 过的冷启动直接进面板,不弹登录。

**不做**
- 触碰 `showHome` / 面板的会话加载逻辑（keyring 往返已足够）。
- token 过期 / 刷新机制（沿用现有 `LoadSession→Me()` 失效再弹登录）。

---

### 020 — client-i18n
**背景**
- 客户端 GUI 全硬编码英文（`internal/client/ui/` 下 panel.go ~116、wizard.go ~66 个用户可见字面量 + `friendly` / `panelMessage` / `tunnelMessage` 错误文案）。需中文 / 英文双语,启动按系统语言,可手动切换。控制器（panel、onboard）无 UI 只回传 typed error,字符串都在 ui 层映射,边界干净。

**范围**
- **翻译范围**:仅 `internal/client/ui/` 用户可见字符串（界面文案 + typed-error 映射层 `friendly` / `panelMessage` / `tunnelMessage`）。服务端、底层零散 `errors.New` 不动,保持「字符串只在 ui 层」边界。约 180 个字符串抽成翻译键（无论用哪种方案抽取工作量一致）。
- **机制**:Fyne 内置 `lang` 包（embed 翻译 JSON + `lang.L`）,系统 locale 自动检测白送。
- **切换时机**:重启生效（选完提示 / 自动重启）,不做运行时实时重绘——VPN 客户端切语言极低频,重启代价可忽略,且避免给 wizard / panel 加「重建当前视图」管线。
- **语言**:zh-Hans + en。
- **偏好存储**:Fyne Preferences（`a.Preferences()`,app 已是 `app.NewWithID("com.lanweave.client")`）,`app.New` 后即可读,与 onboarding 状态解耦。**不能放 `state.Record`**——硬约束:语言要在首次向导、state.json 尚不存在时就能读到。
- **选择器**:向导顶部（`render()` 顶区）+ 面板页脚 各一个三项下拉「跟随系统 / English / 中文」,复用同一偏好读写;空 pref = 跟随系统（首次默认）,选具体语言压过系统检测,选回「跟随系统」清空 pref 回到 auto。

**plan 阶段必验研究点**
- Fyne `lang` 如何让「用户手动选的语言」在启动时**压过系统 locale**（默认按系统 locale 选,运行时强制设定无确定一等公民 API,落地可能靠启动前设 locale / `LANG` env）;research.md 专门验一条。

**验收**
- 中文系统启动默认中文,英文系统默认英文;手动切到另一语言并重启后界面随之改变;选「跟随系统」回到系统语言。
- GUI（Fyne `//go:build gui`）中 / 英两套 + 跟随系统 + 重启生效 + 向导 / 面板两处切换的 Windows 手工验收矩阵（同 018 风格,写入 020 `quickstart.md`）。
- 翻译键抽取不破坏现有无头测试;`unshare -rUn go test ./...` 仍全绿（本期不动 SQLite / nftables / WireGuard,宪法 II 不受影响）。

**不做**
- 服务端日志 / 底层 `errors.New` 散字符串汉化。
- 运行时实时切换（改为重启生效）。
- 第三种及以上语言。

---

### 021 — server-http-mode
**背景**
- 服务端现强制 TLS（`config.go` 校验必须有可读 `tls_cert`/`tls_key`，`app.go` 恒 `ListenAndServeTLS`）。希望支持在 nginx/Caddy 等反向代理终止 TLS 后，服务端只监听本地明文 HTTP。控制面 REST 无状态、用 bearer token，不依赖自身 scheme；数据面 WireGuard 始终加密——反代终止 TLS 不破坏任何服务端逻辑（已核验：限流为全局非按-IP；无 cookie / secure flag / `r.TLS` / 绝对 URL 逻辑；`RemoteAddr` 仅用于访问日志）。

**范围**
- **仅服务端**。客户端零改动：仍连反代的 `https://` 公网地址，TOFU / 证书 / `--insecure` 逻辑不变；数据面 WireGuard 不变。
- **开关**：`[server] tls = false` 才走明文；默认（不写）= HTTPS。⚠️ **实现硬约束**：开关零值必须是安全的 TLS——现存未写该键的配置升级后必须仍 HTTPS（用 load-time 默认 `true`，或反转字段为 `listen_plaintext` 使零值=TLS-on），**绝不静默降级明文**。
- **cert/key**：仅 TLS 模式要求可读；TLS 模式下缺 / 坏 cert 仍**硬失败**（不静默降级）。HTTP 模式忽略 cert/key。
- **绑定告警**：HTTP 模式 + 非回环地址（含 `0.0.0.0`）→ 启动打一条 WARN（提示需反代且勿暴露公网），**不拦截**（兼容 Docker 独立代理容器绑 `0.0.0.0`）。
- **打包**：仅配置开关。postinstall 照旧生成自签证书、默认 HTTPS；反代运维手动改 `tls=false`，未用到的自签证书无害留着。

**DESIGN.md 同步（宪法强制，同 PR）**
- 放宽控制面「全部 HTTPS」措辞为「HTTPS，或 TLS 终止反代后的明文 HTTP」；§11 已知风险新增一条接受项（显式 `tls=false` 才明文、须上游 TLS、勿暴露明文监听公网）；修正 `config.toml.example` 第 7 行「HTTPS only; there is no plaintext listener」。

**验收**
- `tls=false` 起服务，明文 http 客户端打通受保护调用；`tls=true`（或缺省）缺 cert/key 仍硬失败；HTTP 模式 + `0.0.0.0` 启动有 WARN；**现存 TLS 配置升级后仍 HTTPS**（回归）。
- 真实服务端（WG / nft 照常 boot）集成测试覆盖上述（宪法 II，不 mock）。

**不做**
- `X-Forwarded-For` / 可信代理（限流本就全局、无安全影响；反代后日志记代理 IP 作为已知小限制）。
- postinstall 增 HTTP 安装模式（证书生成 / 默认配置不变）。
- 任何客户端改动。

---

### 022 — client-ui-redesign
**背景**
- 主页面（`internal/client/ui/panel.go`）与 Wizard（`wizard.go`）现为 Fyne 默认主题、`widget.Label` 堆叠：状态用 `[在线]` 括号文本、连接/断开两个并排按钮、防火墙 checkbox 在 footer、退出登录常驻、App 标题居中。`docs/UI-DESIGN.md` + `docs/UI-example.png` 给出 Material 3 启发的深色 flat 规范（品牌色、字号层级、App Bar + Hero 卡片 + 列表 avatar/状态点 + Switch + pill 按钮）。本切片按规范全量重做主页面，Wizard 套同一主题。
- 控制器（panel / onboard）无 UI、只回传 typed error + 视图数据，改造集中在 `internal/client/ui/`（+ tunnel 暴露流量计数一处），客户端业务行为零回归。

**范围**
- **自定义主题**（`internal/client/ui/theme.go`，gui tag，挂 `main`）：品牌色 + 字号 + 间距，**强制深色**（VariantDark，忽略系统明暗）。
- **App Bar（48px）**：左对齐 logo（24）+「lanweave」（16/500）+ 底部 0.5px divider；右侧 ⋮ overflow 菜单 = 语言（跟随系统 / 中文 / English 子菜单，重启生效）+ 退出登录（红，置底）。**不新建设置 / 关于**。信任状态进 overflow：insecure 时红字「证书未验证」项，自签 pin 时中性「已在本机信任」项（**已接受的 UX 取舍**：安全信号弱于 018 常驻警告条，换取主界面整洁）。
- **Hero 卡片（本设备）**：status row（● 已连接 / 正在连接 / 未连接 / 连接失败 + ↑ / ↓ 流量）+ 设备名（18）+ VPN IP（13 mono）+ **单一 pill 主按钮**（连 / 断切换，取代双按钮）+ divider + 「允许 VPN 入站访问」Switch 行 + CIDR 副标题（从 footer 移入）。
- **流量计数（新数据源）**：`tunnel` / WG 引擎暴露 per-peer transfer 字节（ReceiveBytes / TransmitBytes），连接期间按更快节奏轮询、Hero 显示 ↑ / ↓，断开隐藏并复位。本切片唯一触达 `tunnel` 包的部分。
- **Tabs + 列表**：「节点 N」「区域 N」带 leading icon + count badge + 2px BrandCyan indicator；节点行 avatar + 右下状态点 | 名 / IP（mono，离线拼接「N 分钟前离线」，数据用现有 `DeviceView.LastSeen`）| 本机行加「本机」chip + `BrandCyanFaded` 高亮，**纯展示不可点**；区域行扁平 avatar + 名 + owner chip，**整行点击 → 区域详情 sheet**（成员 / 退出 / 改密 / 删除 / 踢人均在内）。
- **创建 / 加入区域**：右下角 FAB「+」弹创建 / 加入二选一。
- **自定义控件**：Switch（替代 checkbox）、Avatar + 状态点、Pill chip、状态指示器。
- **Wizard**：仅套主题——保留四步流程 + Back / Cancel / Next 逻辑不动，换配色 / 字号 / pill 主按钮 / 卡片包裹 body，语言选择器留顶部。
- **i18n**：新增 / 改写文案（已连接、断开连接、立即连接、允许 VPN 入站访问、本机、证书未验证、流量等）zh-Hans + en 双语同步；校验 tab 名为「节点 / 区域」。

**验收**
- 每个 user story ≥1 个 Fyne `test` 包 headless 控件测试（单按钮按状态切换、本机 chip + 高亮、Switch 反映 firewall 偏好、流量字节格式化、区域行点击开详情）。
- Mesa OpenGL VM 上肉眼逐条过 `UI-DESIGN.md` §8 验收清单（App Bar 左对齐、单一主按钮、状态圆点非括号、avatar + 状态点、本机 chip、入站 toggle 在 Hero 内、Switch 非 checkbox、pill 按钮、mono IP、无渐变 / 阴影 / glow）。
- `unshare -rUn go test ./...` 仍全绿（本期不动 SQLite / nftables / WireGuard 服务端逻辑，宪法 II 不受影响）。

**不做**
- 新建设置页 / 关于页（overflow 仅放已有项）。
- 信任状态常驻警告条（改为 overflow 菜单项，已接受）。
- 节点详情 / 编辑（节点行纯展示）。
- Wizard 布局 / 交互重构（仅套主题）。
- 浅色主题（强制深色）。
- 第三种语言 / 运行时实时切换语言（沿用 020：重启生效）。

**依赖 / 关联**
- 011（主面板）、020（i18n，新文案走双语）、010（隧道连接状态 + 流量字节计数）。

---

### 023 — per-user-limits
**背景**
- 服务端对每用户可创建的设备（node）与可拥有（创建）的 zone 数量无任何上限，单账号可无限注册 node / 建 zone，缺一道防滥用与容量保护。
- 需求：配置文件加两个全局上限，默认各 10，对所有普通用户一视同仁；admin 豁免。

**范围**
- **配置（全局统一值）**：新增 `[limits]` 段，`max_devices` / `max_zones` 两字段，类型 `*int`（区分「未写」与「显式 0」）。`applyDefaults`：`nil` → 10；`Validate`：负数报错，**0 合法 = 无限制**，正数 = 该上限。常量 `defaultUserLimit = 10`。
- **计数语义**：设备 = 该用户当前存在的 node 数（`nodes.user_id`，删 node 释放名额）；zone = 该用户当前**拥有**的 zone 数（`zones.owner_user_id`，删 zone 释放；**join 他人 zone 不计、不受限**）。
- **强制（store 层事务内，并发不越界）**：`Nodes().Create` / `Zones().Create` 各多收一个 `limit int`，在同一事务内先 `SELECT COUNT(...)` 再 `INSERT`，超限返回新哨兵 `ErrDeviceLimit` / `ErrZoneLimit`；`limit == 0` 跳过检查（= 无限）。强制点：`registerNode`（设备）、`createZone`（zone）。
- **admin 豁免**：handler 取 `id.IsAdmin`（已在 JWT claims），admin 时把有效 limit 当 0 传入 —— 复用「0 = 无限」一套机制，无额外特例分支。
- **错误返回**：HTTP **409**，错误码 `device_limit_reached` / `zone_limit_reached`；`apiclient` 新增 `ErrDeviceLimit` / `ErrZoneLimit` 哨兵按 code 映射；i18n 新增 `wizard.errDeviceLimit`（设备超限在 wizard 设备注册步）、`panel.errZoneLimit`（zone 超限在 panel 创建流）zh-Hans + en 双语。
- **Grandfathering**：运维下调上限到低于现有数量时，**只挡新增、不驱逐**已有；用户降到上限以下前无法再创建。

**验收**
- 默认 10：普通用户建到第 11 个 node / owned zone 被拒 409；admin 不受限可继续；`max_devices=0` / `max_zones=0` 视为无限；缺省 key → 10；负数配置启动校验失败。
- 删 node / 删 zone 后名额释放，可再创建至上限。
- 下调上限低于现有数 → 已有不动、新增被挡（grandfather）。
- 服务端集成测试（真 SQLite，`unshare -rUn`，宪法 II 不 mock）：设备 / zone 超限、admin 豁免、`0`=无限、grandfather 各 ≥1 例；并发不越界可加一条。
- `apiclient` + fake 边界 headless 测覆盖错误码 → 哨兵映射。

**不做**
- 按用户单独配置上限（本期全局统一值，留 v1.1+）。
- 限制 join 他人 zone 的数量（只数 owned zone）。
- 「历史累计注册数」式防滥用（删除即释放名额）。
- 运行时改配置热加载（沿用启动加载）。

**依赖 / 关联**
- 004（node 注册强制点）、005（zone 创建 / owner 计数）、002（JWT `IsAdmin` claim 供豁免）、020（i18n 新文案走双语）。

---

### 024 — session-refresh-tokens
**背景**
- access JWT 默认 2h 过期（`jwt_ttl="2h"`），服务端无 refresh 端点，登录是拿 token 的唯一途径。客户端 `LoadSession→Me()` 一旦发现过期即 `promptSignIn`，用户每用约 2h 就被要求重输密码一次，体验差。
- grill 阶段权衡过三条路：在本地 keyring 存密码自动重登（账号级凭据爆炸半径大、偏离 §7.3，否）；只调长 `jwt_ttl`（削弱吊销窗口、治标，否）；**服务端 refresh-token**（选）——access token 仍短期无状态，另发可吊销的长期 refresh token，客户端静默换新 access token。

**范围（服务端）**
- 新表 `refresh_tokens`：`id, user_id（FK ON DELETE CASCADE）, token_hash, expires_at, revoked_at（可空）, created_at`，`token_hash` 唯一索引。
- RT 形态：`crypto/rand` 32 字节 → base64url **不透明随机串**；服务端只存 **SHA-256 哈希**（明文 RT 仅在签发响应里给客户端一次，不落库）。
- store 层：`Issue(userID)` 生成明文 RT + 落哈希，`expires_at = now+30d`；`Validate(rt)` 按哈希查、校验未吊销且未过期 → 返回 userID；刷新成功**滑动**顺延 30d；`Revoke(rt)` 置 `revoked_at`（幂等）；删用户经 FK 级联清其全部 RT。
- `/api/v1/login` 响应由 `{token}` 改为 `{token, refresh_token}`（access token TTL 仍 **2h** 不变）。
- 新增 `POST /api/v1/refresh`：body `{refresh_token}`，**不经 access-JWT 中间件**（靠 RT 本身鉴权）→ `{token}` 新 access token 并滑动有效期；RT 无效 / 过期 / 吊销 → 401。
- 新增 `POST /api/v1/logout`：body `{refresh_token}` → 204 **幂等吊销**（未知 / 已吊销也回 204）；供 025 登出调用。
- `register` **不变**（仍只回账号信息，客户端注册后照常 `/login` 取 RT）。

**范围（客户端）**
- keyring 新增 `RefreshTokenName`；登录（及注册后登录）、`onboard.Provision` 全流程成功后同存 access token + RT（对齐 019 的落盘点）。
- **惰性刷新**：认证 API 调用遇 401（`ErrSessionExpired`）→ 静默 `POST /refresh` 用存的 RT 换新 access token、回写 keyring、原调用重试一次；`/refresh` 也失败（RT 过期 / 吊销）→ 清会话回落 `promptSignIn`（密码）。**不设主动定时器**。
- `Cleanup` / logout 清理追加删 `RefreshTokenName`。

**DESIGN.md 修订（宪法强制，同 PR）**
- §7.3：access JWT 仍无状态短期（2h）+ 新增**有状态**的 refresh token（30天滑动、服务端存哈希、可逐条吊销）。
- §8 API 表：`/login` 响应加 `refresh_token`；新增 `POST /refresh`、`POST /logout`；§4 数据表加 `refresh_tokens`。
- §11 风险登记：重启换 `jwt_secret` 仅废 access token，RT 经 DB 查验仍有效——「重启=全员吊销」不再成立，全员吊销需删用户或清 `refresh_tokens` 表；本地 keyring 多存一个长期 RT（DPAPI 保护）作为接受项。

**验收**
- 登录响应含 `refresh_token`；access token 过期后客户端**不再弹密码框**，自动用 RT 换新继续；RT 被吊销 / 过期后才回落到密码登录。
- 服务端集成测试（真 SQLite，`unshare -rUn`，宪法 II 不 mock）：`/login` 发 RT；`/refresh` 对有效 RT 发新 token 且滑动 `expires_at`，对过期 / 已吊销 / 未知 RT 返 401；`/logout` 幂等吊销；删用户级联清 RT。
- 客户端 headless 测（`panel` / `apiclient` + fake 边界）：401 → 静默刷新 → 原调用重试成功；刷新失败 → 触发重新登录路径。

**不做**
- RT 轮换（一次一换）/ 重放检测（本期 RT 可重复用至过期 / 吊销）。
- 主动定时刷新（仅惰性按需）。
- 改密码即吊销 RT（当前无自助改密端点，无从触发）。
- admin「全部登出 / 强制全员重登」端点（删用户已够，留后续）。
- `register` 直接发会话（仍走注册后 `/login`）。

**依赖 / 关联**
- 002（`/login`、JWT、鉴权中间件）、009（客户端 keyring + REST client）、019（会话 token 落盘点，RT 同处落盘）。

---

### 025 — client-logout-hardening
**背景**
- 017 的退出登录是「best-effort 远端 + **总是**清本地」：无论服务器可达与否都清掉本地 token / key / state，只在远端可能残留时弹一句「节点可能仍注册」警告。结果——服务器**网络不可达**时退登会在服务端留下无人可清的孤儿 node。
- 需求：服务器 API 连不上（重试 3 次仍失败）时**阻止退出**并弹窗，避免残留；但保留一个「强制退出」逃生口，防止服务器永久失联时用户被困死。依赖 024 的 refresh-token 吊销端点，使登出不留会话残留。

**范围**
- **流程重排**：确认框确认后，**先尝试远端删除（隧道仍连着）→ 确认删除后才拆隧道 / 清防火墙 / 清本地**（API 是公网 HTTPS，与隧道通断无关，连着也能删）。`removeRemoteNode`（列设备 + 删 node）为一次尝试，**最多 3 次、固定 1s 间隔**，期间转无限进度条。
- **按结果分支**：
  - 确认删除（204）/ 已不在（404 或不在节点列表）→ 断隧道 → 清防火墙 → 调 `POST /logout` 吊销本设备 RT → 清 keyring（access token + RT + 设备私钥）+ `state.Clear()` → 回向导（静默）。
  - **纯网络不可达**（超时 / 连接拒绝），3 次仍失败 → **阻止**：隧道 / 防火墙 / 本地一律不动（仍连接、仍登录）→ 弹**两键**窗「取消（默认，保持原样）/ 仍要强制退出」。
  - 会话过期（401）→ 经 024 惰性刷新通常已自动续期；若刷新也失败 → 拉起重新登录，成功后重试删除，用户取消重登 = 中止退出、保持原样。
  - 5xx / 证书变更（非网络错）→ **不阻止**，沿用 017 现有「清本地 + 警告可能残留」路径。
- **强制退出逃生口**：弹窗选「仍要强制退出」→ 走 017 旧的完整拆除（断隧道、清防火墙、清本地），**接受服务端残留**孤儿 node。
- **i18n**：阻止弹窗标题 / 正文 / 两键、强制退出文案 zh-Hans + en 双语。

**DESIGN.md 修订（同 PR）**
- §11 / onboarding 描述：退出登录由「离线也能退、仅警告残留」改为「网络不可达时默认阻止以避免残留，提供显式强制退出逃生口」。

**验收**
- 服务器可达：退登确认 → 远端 node 删除 + 本设备 RT 吊销（服务端 `refresh_tokens` 该行 `revoked_at` 置位）+ 本地清空 + 回向导。
- 服务器网络不可达：退登 → 3 次 1s 重试后弹两键窗；选「取消」→ 仍连接仍登录、本地无任何变化；选「强制退出」→ 完整拆除回向导（接受残留）。
- 客户端 headless 测（`panel.Controller` + fake 边界）：网络错重试 3 次后进阻止分支、不清本地；确认删除分支调 `/logout` 吊销 RT 并清本地；401 → 刷新 / 重登重试。
- GUI 弹窗（Fyne `//go:build gui`）：Mesa OpenGL VM 上手工核对两键窗与强制退出路径。

**不做**
- 对 5xx / 证书错误也阻止（本期阻止只限网络层不可达）。
- 显式「重试」按钮（两键：取消 / 强制退出；手动重试 = 关窗再点退出）。
- 服务端侧孤儿 node 清理工具（残留仍靠 admin 删用户 / 后续运维）。

**依赖 / 关联**
- 017（退出登录编排、`DeleteNode`、`confirmLogout`）、024（refresh-token 吊销端点 + 惰性刷新使 401 分支基本消失）、018（防火墙拆除在登出路径）。

---

### 026 — invite-expiry
**背景**
- 现状：邀请码**一次性、无过期**（DESIGN §7.1），仅 admin 签发。admin 当面 / 带内交付的码永久有效——丢失 / 泄露 / 截图转发的码任何时刻都能拿去注册，缺一道时效收口。
- 需求：给邀请码加有效期，默认 24h，由配置文件全局控制；到期作废。grill 阶段从一个更大的「SMTP 邮件自助注册」设想里剥离出来的独立、最小可成片的那块——本切片**只做有效期**，不含邮件 / 自助注册。

**范围**
- **配置（全局默认，无 per-code 参数）**：`[auth]` 段新增 `invite_ttl`（duration 字符串，如 `"24h"`）。沿用 023 `[limits]` 处理「未写 vs 显式 0」的先例区分 `0`/空：`0` 或留空 = 永不过期。建码 API / `lanweavectl invite` **不加任何 TTL 参数**。`config.toml.example` 写 `invite_ttl = "24h"`（**开箱启用**）。
- **数据模型**：`invites` 加可空列 `expires_at`。建码时若 `invite_ttl > 0` 写 `created_at + invite_ttl`，否则写 `NULL`。`created_by_user_id` 不变（仍 admin 签发）。**`NULL = 永不过期**——三处自洽：旧码 NULL 祖父化、`invite_ttl=0`/空写 NULL 当全局开关、校验只判 `expires_at IS NOT NULL AND expires_at < now()`。
- **存量兼容**：迁移**只加列**，已有未用旧码 `expires_at` 默认 NULL → 自动祖父化、永久有效，**不追溯作废**。上线对现存码零影响，过期仅对「配了 ttl 之后新建的码」生效。
- **注册校验**：`register` 校验邀请码时，过期（`expires_at` 非空且早于 now）**归入现有「邀请码无效」笼统错误**——不单独区分「过期」与「不存在 / 已用」，不泄露码是否曾存在。
- **admin 可见性**：`lanweavectl invite`（建码）**输出带过期时间**（如 `过期: 2026-06-08 14:30 UTC`，或永久码标「永不过期」），便于 admin 当场告知对方时限。**不新增** invite list 命令。
- **过期码清理**：过期未用码**留库不删**——注册时拒绝即可，留作审计痕迹，零成本。**不加后台清理任务**。

**DESIGN.md 修订（宪法强制，同 PR）**
- §7.1：「邀请码一次性、无过期」改为「邀请码一次性；**默认 24h 过期**，有效期由 `invite_ttl` 配置（`0`/空 = 永不过期）；`expires_at` 为 NULL 即永久有效」。
- §4 数据表：`invites` 加 `expires_at`（可空）列。

**验收**
- 配 `invite_ttl="24h"`：新建码 24h 内可注册成功，超 24h 注册被拒并报通用「邀请码无效」；`invite_ttl=0`/空 → 新建码永久有效；旧码（迁移前已存在、无 `expires_at`）迁移后仍永久有效。
- `lanweavectl invite` 建码输出含过期时间（或「永不过期」）。
- 服务端集成测试（真 SQLite，`unshare -rUn`，宪法 II 不 mock）：未过期码注册成功、过期码被拒、NULL（永久）码注册成功、旧行迁移后祖父化各 ≥1 例。

**不做**
- per-code TTL（admin 建码逐个指定有效期）——本期只做全局统一值。
- 区分「过期」与「无效」的差异化错误文案 / 提示。
- 过期码后台清理任务 / `invite list` 命令。
- **SMTP 邮件自助注册 / 邮件投递邀请码 / web 注册页 / URL scheme 深链**——本切片明确剔除，留作后续独立评估。
- 追溯给存量旧码回填过期 / 作废存量未用码。

**依赖 / 关联**
- 002（`invites` 表、admin 建码端点、`register` 校验点）。
- 023（`[limits]` 配置段处理 `*int`「未写 vs 显式 0」的先例，`invite_ttl` 的「空 vs 0」可借鉴同一思路）。

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

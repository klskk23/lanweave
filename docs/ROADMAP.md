# lanweave —— 实施路线（spec-kit /specify 候选清单）

> 状态：v1 设计冻结，按下表 12 个 feature 逐步切；013 为部署联调发现的加固修复，014 为 CI/CD 自动化，015 为创建 zone 自动入组的体验完善，016 为 Windows 客户端图标补齐。
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
| 016 | windows-app-icon                        | 客户端/运维 | 014        | EXE / Fyne 窗口 / 安装器 / 卸载器 / ARP 5 处均显示 lanweave 图标 |

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

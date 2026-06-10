# lanweave —— 设计文档

> 状态：v1 设计冻结（grilling 产出）
> 日期：2026-06-05
> 范围：从零重建，保留概念命名（zone / node / user），代码与历史 spec 不复用。

---

## 1. 项目目标

一款 C/S 架构的轻量 VPN，所有客户端只与中转服务器建立 WireGuard 隧道；
用户在客户端注册节点（node），节点按用户定义的隔离区（zone）互通；
zone 在服务端落地为 nftables set，是 forward 链上的允许规则。

非目标：
- 不做 web 端
- 不做 P2P 直连 / NAT 穿透（所有流量都过中转）
- ~~不做 site-to-site~~ → 自 030 起支持**单向子网宣告**（合成段映射）：具备地址翻译能力的路由器型节点（platform=openwrt）可把背后 LAN 宣告进 zone，服务端从专用合成段池分配全服唯一等长合成段，成员经合成地址单向访问；**LAN 侧主动连接成员为永久非目标**（回程仅靠 conntrack）。真实网段允许跨节点任意重叠（隧道内只出现合成段）。
- v1 不支持出公网（split-tunnel 仅 VPN 网段）

---

## 2. 架构总览

```
┌─────────────────────────────────────────────────────────────┐
│                       Relay Server (Linux)                   │
│                                                              │
│   ┌──────────────┐    ┌──────────────┐    ┌──────────────┐  │
│   │  HTTPS REST  │    │  WireGuard   │    │  nftables    │  │
│   │   (control)  │    │  (data plane)│    │  (forward)   │  │
│   └──────┬───────┘    └──────┬───────┘    └──────┬───────┘  │
│          │                   │                   │           │
│          └──────────┬────────┴────────┬──────────┘           │
│                     │                 │                      │
│              ┌──────▼─────────────────▼──────┐               │
│              │       SQLite (state)          │               │
│              │  users / nodes / zones /      │               │
│              │  zone_members / invites       │               │
│              └───────────────────────────────┘               │
└─────────────────────────────────────────────────────────────┘
                              ▲
              WG + HTTPS      │
       ┌──────────────────────┼──────────────────────┐
       │                      │                      │
  ┌────▼────┐            ┌────▼────┐            ┌────▼────┐
  │ Client  │            │ Client  │            │ Client  │
  │ (Win)   │            │ (Win)   │            │ (Win)   │
  │ node A1 │            │ node A2 │            │ node B1 │
  └─────────┘            └─────────┘            └─────────┘
```

- **控制面**：HTTPS REST + JSON（或经 TLS 终止反代后的明文 HTTP，见 §11），客户端持 JWT 调 API。服务端可选在 `/api/docs/` 暴露交互式 API 文档（OpenAPI + Swagger UI，缺省开启，`api_docs = false` 关闭，见 §11）。
- **数据面**：WireGuard，所有 peer 只与服务器建链，peer 间流量经服务器 forward。
- **隔离面**：nftables 专用 table，按 zone 维护 set，控制 peer 互访。

---

## 3. 网络模型

### 3.1 拓扑
- Hub-and-spoke。客户端 WG `peer` 只有"服务器"一项。
- 服务器内核开启 `net.ipv4.ip_forward = 1`。

### 3.2 IP 池
- TOML 中定义单个 IPv4 网段，例如 `100.127.0.0/16`（CGNAT 段，与公网不冲突）。
- 服务器固定占池中**第一个**可用 IP（如 `100.127.0.1`），作为 WG 接口地址。
- 客户端 node 从第二个 IP 起顺延分配。

### 3.3 IP 分配算法
- 注册新 node：事务内 `SELECT MIN(unused)` 取池中最小未占用 IP，写回 `nodes.ip`。
- 删除 node：IP 立即释放，下一个新 node 复用之。
- SQLite 写锁串行天然消解并发竞争。
- 池耗尽：返回 API 错误，admin 可扩 TOML 池后重启。

### 3.3b 合成段池（子网宣告，030）
- 可选 TOML 段 `[announce] pool`（单个 IPv4 CIDR，建议 CGNAT 内不与 VPN 池冲突的子段，如 `100.100.0.0/16`）；缺省 = 宣告功能停用。
- 每条宣告 =（节点, 真实子网）→ 等长合成块，事务内 first-fit 自然对齐分配，可挂接多个 zone、最后挂接消失即回收复用。
- 服务端为合成池安装一条指向 wg 接口的内核路由（VPN 池靠接口地址隐式路由，合成池没有接口地址）；peer 的 AllowedIPs = 节点 /32 ∪ 其全部合成块。
- nftables：每 zone 第二个 interval set `zone_<id>_routes` + 规则「zone 成员 → 本 zone 合成段 accept」；forward 链头一条全局 `ct state established,related accept` 承载回程（这也使既有成员互通的回包改走 conntrack 快路径，行为等价）。

### 3.4 流量策略（split-tunnel）
- 客户端 `AllowedIPs = 100.127.0.0/16` —— 仅 VPN 网段走隧道。
- 公网、DNS 走本机原路径。
- 服务器**不做** MASQUERADE，**不做** DNS 转发。
- 客户端 `PersistentKeepalive = 25`（NAT 保活 + 在线状态判定基础）。
- 客户端**可选**主机入站放行（feature 018，默认关、Windows-only）：用户在主面板勾选且隧道已连接时，
  客户端用 `netsh advfirewall` 装一条具名入站放行规则 `lanweave-vpn-inbound`（`remoteip=100.127.0.0/16`、
  inbound、所有端口、`profile=any`），让同网段 peer 能反向访问本机服务。规则存在当且仅当（开关开 ∧ 已连接）：
  连接 / 勾选-且-已连接时装，断开 / 取消勾选 / 登出 / 退出时删；具名 + 幂等（删后再加）+ 启动孤儿清扫。
  仅 Windows 生效，其它平台为 no-op（偏好仍持久化）。

### 3.5 WireGuard 密钥
- **客户端生成**密钥对，私钥仅本机存储（Windows DPAPI / keyring）。
- 客户端上传公钥到服务端，服务端把它写入对应 node 的 WG peer。
- 服务端零私钥（除自身接口私钥）。
- node 删除即从 WG peer 列表移除，对应公钥失效。
- 换设备：在新机器上新建 node（旧 node 可保留或删除）。

---

## 4. 数据模型

### 4.1 实体

| 实体        | 关键字段                                                     | 备注                                          |
|-------------|--------------------------------------------------------------|-----------------------------------------------|
| `users`     | id, username UNIQUE, password_hash, is_admin, created_at     | 邀请制注册                                    |
| `invites`   | id, code UNIQUE, created_by_user_id, used_by_user_id, used_at, expires_at| 一次性，`used_at` 非空即作废；`expires_at` 可空，NULL=永不过期（slice 026） |
| `nodes`     | id, user_id, name, wg_pubkey UNIQUE, ip UNIQUE, created_at   | (user_id, name) UNIQUE                        |
| `zones`     | id, name UNIQUE, password_hash, owner_user_id, created_at    | name 全局唯一                                 |
| `zone_members` | zone_id, node_id, joined_at                               | 复合主键 (zone_id, node_id)                   |
| `refresh_tokens` | id, user_id, token_hash UNIQUE, expires_at, revoked_at, created_at | 仅存 SHA-256 哈希；30 天滑动过期；`user_id` FK ON DELETE CASCADE（slice 024） |

### 4.2 约束
- `nodes.name` 在 `(user_id, name)` 维度唯一；不同用户可重名。
- `zones.name` 全局唯一（"Zoom 会议 ID + 密码"模型）。
- 同一 node 可加入多个 zone（M:N，无上限）。
- 用户、zone、node 删除均为**硬删**，事务内级联：
  - 删 user → 删其全部 node → 各 node 从所有 zone_members 移除 → 该用户 owner 的 zone 一并删除（连带所有 member）。
  - 删 zone → 删该 zone 全部 zone_members → 从 nftables 移除对应 set 与规则。
  - 删 node → 释放 IP → 从 WG peer 移除 → 从所有 zone 的 set 移除。

### 4.3 密码 hash
- `users.password_hash` 与 `zones.password_hash` 用 argon2id（推荐）或 bcrypt。
- 配置中的 admin 明文密码在首次启动 import 时 hash 后落库。

---

## 5. Zone 语义

### 5.1 创建
- 任何登录用户可创建 zone，输入唯一 name + 密码。创建者自动成为 owner。
- 创建时**不**自动把 owner 的 node 加入 —— 由 owner 后续显式选择。

### 5.2 加入
- 客户端流程：选自己名下某 node → 输入目标 zone name + 密码 → 提交。
- 服务端：校验密码 → 写入 `zone_members` → 调 nftables `add element zone_<id> { ip }`。

### 5.3 视野
- **member**：能看到同 zone 内所有 node 的 **name + IP + 所属用户名**（全透明）。
- **owner**：同 member，无额外信息。

### 5.4 owner 特权
| 操作              | 影响                                                            |
|-------------------|------------------------------------------------------------------|
| 改 zone 密码      | 旧密码作废；**已加入 node 不被踢**，密码只影响后续加入者         |
| 踢 member node    | 从 zone_members 删 → set 中移除对应 IP                            |
| 删整个 zone       | 所有 member 移除，set 销毁，rule 销毁，name 释放                 |
| 列出所有 member   | 实际上所有 member 都能看，owner 无额外信息                       |

ownership 转让：**v1 不做**，留 v1.1。

---

## 6. 服务端转发与 nftables

### 6.1 专用 table
- 表名建议：`inet lanweave`（与运维其他 firewall 隔离）。
- 仅管 **forward** 链；INPUT/OUTPUT 不动（SSH、HTTPS API 走系统现有 firewall）。

### 6.2 规则结构（一 zone 一 set）
```nft
table inet lanweave {
    set zone_<id> {
        type ipv4_addr
        elements = { 100.127.0.5, 100.127.0.9, ... }
    }

    chain forward {
        type filter hook forward priority 0; policy drop;

        # 每个 zone 一条
        ip saddr @zone_<id> ip daddr @zone_<id> accept

        # 默认 drop（policy 已声明）
    }
}
```

跨 zone 流量默认被链 policy drop。

### 6.3 apply 模型
- **启动**：从 SQLite 全量重建 table（`nft -f` 单次原子提交）。SQLite 是唯一真相源。
- **运行期**：变更走 `nft add element` / `nft delete element` 增量改 set；新增/删 zone 走 add set / add rule / delete set / delete rule。
- 重启不依赖 nftables 持久化；任何漂移以 DB 为准。

### 6.4 WG 接口管理
- 服务端用 wireguard-go（用户态）或内核 WG（生产推荐内核）。
- 用 [wgctrl-go](https://github.com/WireGuard/wgctrl-go) 通过 netlink 管理 peer：增删 peer = 写库 + 调 wgctrl。

### 6.5 在线检测
- 服务端定时（每 30s）拉 `wg show` / wgctrl 取每 peer `last_handshake_time`。
- 阈值：`now - last_handshake < 3 min` → `online`，否则 `offline`。
- 客户端必须配 `PersistentKeepalive = 25` 否则空闲时会被判离线。

---

## 7. 认证与权限

### 7.1 用户来源：邀请制
- 注册接口要求 `invite_code`。
- 邀请码：**一次性、用了作废**；可选有效期。
- 仅 admin 可生成邀请码（API）。
- 有效期由配置全局值 `auth.invite_ttl`（duration 字符串）控制：建码时写 `expires_at = created_at + invite_ttl`；`0`/空 写 NULL = 永不过期，存量旧码祖父化。注册时过期码归入通用「邀请码无效」错误，不区分过期/不存在/已用（无 oracle）。无 per-code 参数、无后台清理（slice 026）。

### 7.2 admin bootstrap
- TOML 中直接定义首位 admin 用户名与**明文**密码。
- 服务首次启动：若库中无此用户，按 TOML hash 入库并标记 `is_admin`。
- **安全注意**（文档强调，运维须自觉）：
  - 配置文件 `chmod 600` root-only
  - 不要提交到 git
  - 修改 admin 密码：改 TOML + 重启（接受风险）

### 7.3 会话：短期 access JWT + 可吊销 refresh token（slice 024）
- **Access token**：短期 JWT（1–2h），算法 HS256，密钥来自 TOML / 启动随机生成。无状态、不可吊销 —— 接受 1–2h 风险窗口。
- **Refresh token（RT）**：登录额外返回一枚不透明随机串（`crypto/rand` 32B → base64url）。服务端只存其 SHA-256 哈希（高熵随机串，无需 argon2id），30 天**滑动**过期，每次成功续期把窗口推到 `now+30d`；不轮换、不做重放检测。
- `/login` 返回 `{token, refresh_token}`；access token 过期时客户端用 RT 静默调 `/refresh` 换新 access token（懒刷新，401 后重试一次），用户无感。
- 客户端把 access token 与 RT 都存 Windows credential manager / keyring（不要纯文件）。
- **吊销**：`/logout` 吊销单枚 RT（幂等）；删用户经 FK 级联清掉其全部 RT。被吊销设备退回密码登录。
- 重启服务 = 换签名密钥 = 所有 **access token** 失效，但 **RT 持久化在 DB**，客户端可立即用 RT 静默续期 —— 重启不再"全用户被踢"，仅造成一次自动刷新。

### 7.4 速率限制
- MVP 仅在 API 中间件加全局 `golang.org/x/time/rate` 令牌桶（如 100 req/s）。
- 账户级失败计数 / 锁定：**v1.1**。

### 7.5 账号密码复杂度（slice 027）
- **规则**：8–64 个字符；至少各含一个 ASCII 大写字母、一个 ASCII 小写字母、一个数字；仅允许 ASCII 可打印字符（`!`–`~`），不含空格、控制字符或非 ASCII（中文/emoji 等一律拒绝）；ASCII 符号允许但不强制。
- **单一真源**：规则由共享包 `pkg/passwordpolicy`（纯函数 `Validate`，返回带类型的失败原因）实现，服务端与客户端共用，二者判定永不漂移。
- **生效点：仅注册**。`register` 在「用户名校验之后、邀请码校验之前」调用 `Validate`，不合规则返回 `validation_error`/400（英文消息，相关原因合并表述）。**登录不校验** —— 早于本策略创建的弱密码账户（及 bootstrap admin）仍可登录，不被锁出、不强制改密。
- **客户端**：Fyne 向导「创建账户」步骤本地拦截提交，按类型化原因显示对应的本地化逐条提示；密码框下方常驻一条规则说明（输入前即可见）。仅创建账户时生效，登录步骤不拦截。
- **范围外**：zone 密码、配置文件中的 bootstrap admin 明文密码不受本策略约束，行为不变（见 §6 zone、§7.2）。哈希仍为 argon2id；64 字符上限是对攻击者输入的防御性上界，非哈希方案的硬限制。

---

## 8. REST API（草案）

> HTTPS（或 TLS 终止反代后的明文 HTTP，见 §11）。Content-Type: application/json。鉴权头：`Authorization: Bearer <jwt>`。

### 8.1 公开端点
| 方法 | 路径               | 说明                                    |
|------|--------------------|-----------------------------------------|
| POST | `/api/v1/register` | body: {invite_code, username, password} |
| POST | `/api/v1/login`    | body: {username, password} → {token, refresh_token} |
| POST | `/api/v1/refresh`  | body: {refresh_token} → {token}；RT 鉴权（access JWT 此时已过期），滑动续期 |
| POST | `/api/v1/logout`   | body: {refresh_token} → 204；吊销该 RT，幂等（未知/已吊销也 204，不作 oracle） |
| GET  | `/api/v1/server`   | 返回 WG 公钥、endpoint、network、MTU 等  |

### 8.2 用户端点
| 方法 | 路径                              | 说明                                    |
|------|-----------------------------------|-----------------------------------------|
| GET  | `/api/v1/me`                      | 当前用户信息                            |
| GET  | `/api/v1/nodes`                   | 名下所有 node                           |
| POST | `/api/v1/nodes`                   | body: {name, wg_pubkey} → {id, ip}      |
| DELETE | `/api/v1/nodes/{id}`            | 删除某 node                             |
| GET  | `/api/v1/zones`                   | 我所在的 zone 列表（按 node 聚合）      |
| POST | `/api/v1/zones`                   | body: {name, password} 创建 zone        |
| POST | `/api/v1/zones/{name}/join`       | body: {node_id, password} 加入 zone     |
| POST | `/api/v1/zones/{name}/leave`      | body: {node_id}                         |
| GET  | `/api/v1/zones/{name}/members`    | 列出该 zone 所有 member node            |

### 8.3 owner 端点
| 方法 | 路径                                       | 说明                              |
|------|---------------------------------------------|-----------------------------------|
| PATCH| `/api/v1/zones/{name}`                      | body: {password} 改密码           |
| DELETE | `/api/v1/zones/{name}`                    | 删整个 zone                       |
| DELETE | `/api/v1/zones/{name}/members/{node_id}`  | 踢某 member node                  |

### 8.4 admin 端点
| 方法 | 路径                       | 说明                              |
|------|----------------------------|-----------------------------------|
| POST | `/api/v1/admin/invites`    | 生成邀请码                        |
| GET  | `/api/v1/admin/invites`    | 列出邀请码（已用/未用）           |
| GET  | `/api/v1/admin/users`      | 用户列表                          |
| DELETE | `/api/v1/admin/users/{id}` | 硬删用户（级联）                |

---

## 9. Windows 客户端

### 9.1 技术栈
- UI：**Fyne**（纯 Go）。
- WG 后端：**嵌入 wireguard-go** 用户态实现 + **WinTun** 驱动。
- 单 .exe + WinTun .dll 打包；安装时申请 UAC（创建虚拟网卡需管理员）。
- HTTP 客户端：`net/http` + JSON。
- 本地密钥存储：Windows credential manager（DPAPI 封装）。

### 9.2 机器与 node 关系：1:1
- 一台机器 = 一个 node（本地存了私钥与 IP 的那个）。
- 客户端**不可**同时拉起多个 node 的隧道。
- 用户面板能查看名下其他机器上的 node（只读、不可远程控制）。
- 换机 = 重新走首次启动 wizard，注册新 node。

### 9.3 首次启动 wizard
1. **服务器地址**：输入 server URL（如 `https://vpn.example.com`）。
2. **证书信任（TOFU，feature 018 取代 017 会话级 opt-in）**：
   - 默认走系统 CA / LE 信任链。
   - 自签名 / 内网 CA 场景：**首次连接**若证书过不了系统 CA，反应式弹窗显示该 server 的叶证书 SHA-256 指纹，
     请用户「在本设备信任此证书?」；接受后把指纹持久化到 `state.json`（按 server 钉，`PinnedCertSHA256`）。
   - 此后验证规则为「叶证书指纹 == 已钉指纹 **或** 过系统 CA」：已信任的自签 server 重启后**静默**连接，
     其它无法验证的证书仍被拒。主面板在 **App Bar overflow 菜单**显示中性项「self-signed (trusted on this device)」（feature 022：由原常驻指示改记为 overflow 菜单项）。
   - 已钉 server 出现**指纹变更且仍过不了系统 CA** → 弹更重的「证书已变更」警告、阻断、需显式接受方可连接，
     接受则覆盖旧钉。拒绝首次信任 / 变更警告均不建立连接。
   - 保留 `--insecure` CLI flag（仅 troubleshooting，完全不验证）；该状态在 App Bar overflow 菜单显示红色项
     「证书未验证」（feature 022：由原常驻警示条改记为 overflow 菜单项，属已接受的 UX 取舍——警示从常驻降级为
     菜单内可见）。UI 不提供「跳过验证」开关，也不再提供 017 的「仅本次会话、不记住」路径——TOFU 已取代之。
3. **账号**：
   - 已有账号 → 登录（username/password）。
   - 新用户 → 输入邀请码 + 用户名 + 密码 → 注册。
4. **node 名**：本机起一个 node 名（同账号下唯一）。
5. **生成密钥**：本地生成 WG 密钥对，私钥写 keyring，公钥上传 POST `/nodes`。
6. **拿配置**：服务端返回 `{ip, server_pubkey, endpoint, network}`，组装 WG 配置，调 wireguard-go 拉起隧道。
7. **完成**：跳到主面板。

### 9.4 主面板（feature 022 改版：Material 3 启发的深色 flat 布局，见 `docs/UI-DESIGN.md`）
- **App Bar**（顶部 48px）：左对齐 logo + 「lanweave」，**非居中大标题**；右侧 ⋮ overflow 菜单——含
  语言切换（跟随系统 / 中文 / English，重启生效）、证书信任状态项（见 §9.3）、置底红色「退出登录」。
  退出登录**不再常驻主界面**，收入 overflow（feature 022 改）。
- **Hero 卡片（本设备）**：状态行用圆点 + 颜色 + 简短文字（已连接 / 正在连接 / 未连接 / 连接失败，**非
  `[离线]` 括号文本**），连接时右侧显示本次连接的累计 ↑/↓ 流量（断开隐藏并复位）；设备名 + 等宽 VPN IP；
  **单一** pill 主按钮按隧道状态在「立即连接 / 断开连接」间切换（**非两个并排按钮**）；分隔线后置「允许 VPN
  入站访问」**Switch**（feature 018 的入站放行偏好，由 checkbox 改为 Switch）+ `100.127.0.0/16` 副标题。
- **节点 / 区域 tab**（带计数，选中有 2px 品牌青指示条；名为「节点 / 区域」**非「我的…」**）：
  - 节点行：avatar + 右下状态点 + 名 + 等宽 IP；离线行拼「N 分钟前离线」且文字最浅（tertiary）；本机行加
    「本机」chip + 高亮背景，纯展示不可点（节点无详情）。
  - 区域行：扁平 avatar + 名 +（owner）chip；**整行可点 → 区域详情 sheet**（成员列表 / 退出 / owner 的改密·
    踢人·删除）。
- **右下「+」FAB**：弹「创建区域 / 加入区域」二选一，分别进既有流程。
- 入站放行的安全语义（开启即让同网段 peer 触达本机所有本地服务、无二次确认、断开/退出即删）不变，仅呈现
  位置从底部页脚迁入 Hero 卡片（feature 018 → 022）。

### 9.5 连接自愈与源端口随机（slice 028）

- **源端口随机（FR-017）**：每次 `Connect()` 都新建一个 wireguard-go `device` 并用 `conn.NewDefaultBind()`，
  **从不写 `listen_port=`** 到 UAPI 配置 → OS 每次分配一个新的临时 UDP 源端口。无固定监听端口、无端口复用；
  这是结构性不变量（由集成回归测试守护：连续两次 `Connect()` 源端口不同）。
- **握手陈旧自愈重连（FR-001~FR-016）**：客户端跑一个**独立**的健康巡检 goroutine（15s ticker，与节点列表
  `pollLoop` 解耦，互不阻塞）。每 tick 读取 wireguard-go 的 `last_handshake_time_sec`：当用户意图为「保持连接」
  且链路存活但最后握手 age 超过 **240s 阈值**（健康链路有 keepalive，age 上界约 120s，恒不触发）→ 判定链路已
  静默断掉，自动重连；重连失败按**固定 15s 周期无退避**重试，直到恢复或用户手动断开。全程**静默**——只更新
  状态行与按钮，不弹任何对话框。
- **`desiredConnected` 用户意图（仅内存态，FR-006/007/008）**：表达「用户是否希望保持连接」，**不持久化**
  （不落 `state.json`/keyring）。仅在**手动连接成功**时置 true、**手动断开**时置 false；手动连接**失败**保持
  false（不触发后台重试）。因此应用重启后意图恒为 false → **开机不自动连接**。自愈重连仅在意图为 true 时发起，
  且不改变意图（自愈只服务用户既有意图，不擅自建连）。
- **单飞守卫（修既有竞态，FR-011/012）**：所有 connect/disconnect 经引擎身份守卫串行化——`Connect` 仅在其创建的
  engine 仍是当前 engine 时才写 `state=Connected`；手动 `Disconnect` 会 nil 掉 engine，使在途的 `Connect` 在握手
  完成后发现身份不符而**放弃**，杜绝「在途 Connect 复活已被用户拆除的隧道 + 泄漏 device」。**手动断开必胜。**
- **UI 状态三输入派生（FR-013/014）**：状态指示由 `(state, desiredConnected, connFailed)` 派生。优先级：
  `Connected`→绿；`Connecting`→黄；`Disconnected && desiredConnected`（自愈重连窗口）→**黄色「连接中…」**且按钮
  「断开」（**优先且不看 connFailed**，故重试期不误显红色「失败」）；`Disconnected && !desiredConnected &&
  connFailed`（手动连接失败）→红「失败」；皆否则灰「未连接」。
- **防火墙镜像（FR-016）**：自愈路径调用与手动路径相同的 `ReconcileFirewall(connected)`，`connected` 取隧道
  **实际生效态**——重连窗口内链路本就不通 → 防火墙**关闭**，与手动断开期一致；重连成功重新 Connected 才重新打开。

---

## 10. 部署与运维

### 10.1 交付形态
- 单个 Go 二进制 `lanweaved`。
- systemd unit。
- .deb 包（Debian/Ubuntu 主流目标）。

### 10.2 文件布局
```
/usr/bin/lanweaved              # 主二进制
/etc/lanweave/config.toml       # 配置（chmod 600）
/var/lib/lanweave/db.sqlite     # SQLite 数据
/var/lib/lanweave/wg_private    # 服务端 WG 私钥（chmod 600）
/var/log/lanweave/              # 日志（journald 也行）
/lib/systemd/system/lanweaved.service
```

### 10.3 配置示例（`config.toml`）
```toml
[server]
listen = "0.0.0.0:443"          # HTTPS API
tls_cert = "/etc/lanweave/cert.pem"
tls_key  = "/etc/lanweave/key.pem"

[wireguard]
listen_port = 51820
interface   = "wg-lanweave"
network     = "100.127.0.0/16"   # 池
mtu         = 1420

[nftables]
table = "inet lanweave"

[auth]
jwt_secret = "<32+ bytes random>"
jwt_ttl    = "2h"

[admin]
username = "alice"
password = "ChangeMeOnFirstLogin!"  # 明文，首次启动后建议改并重启

[limits]
max_devices_per_user     = 10   # 缺省 10；0 = 无限；负数 = 启动报错。admin 不受限。
max_owned_zones_per_user = 10   # 同上，仅统计本人创建（拥有）的 zone；加入他人 zone 不计入。
```

### 10.4 systemd unit 要点
- `User=root`（需要管理 nftables + WG 内核接口）。
- `CapabilityBoundingSet=CAP_NET_ADMIN`（精简权限）。
- `Restart=on-failure`。
- `StandardOutput=journal`。

### 10.5 备份
- `/var/lib/lanweave/db.sqlite` 是全部状态，定时 `sqlite3 .backup` 即可。
- 运维自理；服务本身不内置备份。

### 10.6 日志
- 走 `slog` 输出 JSON 到 stdout，由 journald 收集。
- 关键事件 INFO：用户注册、node 注册、zone 创建/加入/离开/删除、admin 操作。
- 错误 WARN/ERROR。

---

## 11. 已知安全风险（明示并接受）

| 风险                                     | 缓解 / 文档要求                            |
|------------------------------------------|--------------------------------------------|
| TOML 中 admin 明文密码                   | `chmod 600`、不入 git、首次后建议改        |
| Access JWT 不可吊销                      | 1–2h 短期过期；吊销由 refresh token 层做（`/logout`、删用户级联）；重启换签名密钥仍即时使所有 access token 失效（但客户端可凭 RT 静默续期，不再"全用户被踢"） |
| 客户端持有一枚长效（30 天滑动）refresh token（slice 024） | 仅存 DPAPI/keyring（非纯文件）；服务端只存 SHA-256 哈希、明文不入库不入日志（仅签发时一次性返回）；可经 `/logout` 或删用户即时吊销；丢失即等价一次 30 天窗口的会话泄露——以短 access TTL + 可吊销 RT 收敛，接受该取舍 |
| 退出登录可能在服务端残留无人可清的孤儿 node（slice 025 加固，取代 017「离线也能登出、仅警示残留」） | 登出改为**先删远端 node**（控制面走公网 HTTPS，与隧道无关）：网络不可达时 3 次 ×1s 重试仍失败则**阻止登出**并弹两键窗（取消默认 / 强制退出逃生口），零本地改动，避免产生孤儿；reachable 的 5xx/证书变更**不阻止**（回落 017「清本地 + 警示残留」）；登出额外吊销本设备 refresh token（`/logout`）；强制退出接受一枚服务端孤儿 node |
| 无账户级失败计数（仅全局限流）           | v1.1 补；上线后观察                        |
| 跳过证书验证（`--insecure` CLI flag） | 仅 troubleshooting，完全不验证；状态于 App Bar overflow 菜单红色项「证书未验证」可见（feature 022：由常驻警示降级为菜单项，已接受的 UX 取舍）；UI 不暴露此开关 |
| TOFU 信任自签 / 内网证书（feature 018 取代 017 会话级 opt-in） | 首次连接证书过不了系统 CA 时弹窗、显式信任、按 server 持久化叶证书 SHA-256 指纹；验证=指纹或系统 CA；证书变更弹更重警告并阻断、需显式接受；中性「已信任」项于 overflow 菜单（feature 022 改） |
| 客户端主机防火墙入站放行（feature 018，默认关、Windows-only） | 用户显式开启 Hero 卡片内 Switch 且隧道已连接才装具名规则 `lanweave-vpn-inbound`（仅 `remoteip=100.127.0.0/16`、`profile=any`）；开启即让同网段 peer 触达本机所有本地服务（无二次确认，开关旁 CIDR 副标题标识范围）；断开 / 取消 / 登出 / 退出即删，启动清扫孤儿规则 |
| 服务端明文 HTTP 监听（`tls=false`，feature 021） | 仅显式 `tls=false` 才明文；缺省/`tls=true` 仍 HTTPS 且缺证书硬失败（绝不静默降级）；明文绑定非回环地址启动告警；须置于 TLS 终止反代之后、勿暴露明文监听公网；客户端仍连反代 `https://`、数据面 WireGuard 不变 |
| 节点 platform 为客户端自报（feature 030） | 谎报 `openwrt` 可越过宣告能力门禁，但后果限于「宣告了自己兜不住的网段、本 zone 内不通」——隔离仍由 nftables 按合成段管控，不产生越权访问 |
| 宣告端 MASQUERADE 抹掉真实访问源（feature 030/032） | 目标 LAN 内主机只见路由器 LAN IP，无法审计是哪个 zone 成员访问；接受（NAT 方案固有），zone 准入密码即信任边界 |
| 宣告内容的社工风险（feature 030） | 成员可声称任意内网内容（"我的 NAS"）诱导同 zone 成员访问其控制的服务；zone 全透明互信模型下接受，准入密码即边界 |
| API 文档页默认开启、无鉴权（`api_docs`，feature 029） | `/api/docs/` 暴露的仅是 **API 形状**（OpenAPI 文档 + Swagger UI，全静态嵌入，不含任何用户数据 / 机密，示例值全虚构且有测试钳制）；运维可显式 `api_docs = false` 关闭，关闭后 docs 路径与不存在的路径逐字节同 404、不可探测；docs 路径与业务 API 同受全局限流 |
| 服务进程 root 运行                       | systemd 用 CapabilityBoundingSet 缩小      |
| 发布产物未签名（Windows installer / .deb） | 发布说明提示 SmartScreen「更多信息→仍要运行」；附 `SHA256SUMS` 供完整性校验 |
| 桌面 GUI（Fyne 弹窗 / 指示器 / 开关）与主机 `netsh` 防火墙调用以人工 quickstart 矩阵验收，非自动化端到端测试 | 安全相关逻辑（登出序列、证书失败→TOFU 钉扎/比对、`CertError`/`ErrCertChanged` 路径、防火墙开关决策真值表 controller 层 T015 自动覆盖）仍有 apiclient / controller 层自动化验收；仅纯 GUI 呈现与 `netsh` 执行效果（含 018 的 TOFU 首次信任/变更弹窗，及防火墙规则实际装/删 + peer 反向触达本机）走人工（宪法 II 的 GUI/exec 豁免，017 登记，018 延伸；US2 端到端为实机 + 实 peer 的固有结果，无法在 `unshare` 网关内复现，落到 Windows 人工矩阵——接受发现 C1） |

---

## 12. v1.1+ 待办

- ownership 转让
- 账户级失败计数与锁定
- Let's Encrypt 自动续签集成
- 客户端跨 OS（Linux / macOS）
- node 在线状态推送（WebSocket 替换轮询）
- 软删与审计日志保留

---

## 13. 推荐目录骨架

```
lanweave/
├── DESIGN.md                  # 本文档
├── README.md
├── go.mod
├── cmd/
│   ├── lanweaved/             # 服务端二进制入口
│   │   └── main.go
│   └── lanweave-client/       # Windows 客户端入口
│       └── main.go
├── internal/
│   ├── server/
│   │   ├── api/               # REST handler
│   │   ├── auth/              # JWT、邀请、admin bootstrap
│   │   ├── ipam/              # IP 池分配
│   │   ├── nftables/          # set/rule 管理
│   │   ├── wg/                # wgctrl 封装
│   │   ├── store/             # SQLite 仓储层
│   │   └── config/            # TOML 加载
│   └── client/
│       ├── ui/                # Fyne 视图
│       ├── tunnel/            # wireguard-go + WinTun
│       ├── keyring/           # DPAPI 封装
│       └── apiclient/         # REST 客户端
├── pkg/
│   └── protocol/              # 客户端/服务端共享的 DTO 与 JSON 类型
├── migrations/                # SQLite schema
├── deploy/
│   ├── debian/                # .deb 打包
│   └── systemd/               # unit 文件
└── docs/                      # 后续 ADR / 运维手册
```

---

## 14. 实施路线建议

按依赖序，每步独立可跑：

1. **数据层**：schema + migrations + 仓储层 + 集成测试。
2. **IPAM**：池分配器，配套并发测试。
3. **服务端骨架**：配置加载 + admin bootstrap + REST + JWT + 邀请码 + 用户/node CRUD。
4. **WG 集成**：wgctrl 接 peer，验证 split-tunnel 通。
5. **nftables 集成**：table 启动重建 + set 增量改 + 跨 zone drop 验证。
6. **zone 业务流**：创建/加入/离开/owner 操作 + nft 联动。
7. **Fyne 客户端骨架**：首次启动 wizard + 主面板 + REST 调用。
8. **客户端隧道**：嵌入 wireguard-go + WinTun，本机 node 隧道生效。
9. **打包**：systemd unit + .deb；Windows 客户端 .msi/installer。
10. **端到端验证**：两台 Windows 互通同 zone，跨 zone 不通；owner 改密不踢老人；删用户全清理。

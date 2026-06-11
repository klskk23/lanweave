# Feature Specification: OpenWrt 客户端基础（无头 daemon + CLI）

**Feature Branch**: `031-openwrt-client-base`

**Created**: 2026-06-11

**Status**: Draft

**Input**: User description: "OpenWrt 客户端基础（切片 031 openwrt-client-base）：跑在 OpenWrt 路由器上的无头守护进程 lanweave-routerd + CLI 子命令，完成注册/隧道/zone 管理的完整客户端生命周期（不含宣告 032、不含合成段路由消费 033）。无头 daemon + CLI、procd 守护、内核 WireGuard、凭据明文落盘 0600、TOFU 对齐 018、登出对齐 025、静默续期对齐 024、自愈重连对齐 028。"

## Clarifications

### Session 2026-06-11

- Q: 形态？ → A: 无头 daemon + CLI 子命令（grill 会话锁定）；非交互、flag 驱动、密码可经 stdin；procd 守护开机自启；隧道用内核 WireGuard（不嵌用户态实现）。
- Q: 目标硬件下限与首发架构？ → A: 现代路由器（≥64MB flash / 128MB RAM）；首发交叉编译 arm64 + amd64 + mipsle(softfloat)；不为 16MB 老设备做体积特化。
- Q: 凭据存储？ → A: 路由器无 keyring，明文文件 0600（root-only）；写入设计风险登记。
- Q: 与既往客户端语义对齐？ → A: TOFU 证书钉扎=018（CLI 显式信任持久化指纹）、refresh token 静默续期=024、登出先注销 node 不可达则阻止 + 强制逃生口=025、握手陈旧自愈重连=028。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 路由器接入：onboard 并建立隧道 (Priority: P1)

一位用户 SSH 进自己的 OpenWrt 路由器，安装客户端二进制后用 CLI 完成接入：配置服务器地址，用已有账号登录（或用邀请码注册新账号），给本机起个节点名完成注册——本机密钥对自动生成、平台自动上报为 openwrt。随后启动守护进程：隧道自动建立，路由器获得 VPN 地址，能 ping 通服务器。配置开机自启后，路由器重启隧道自动恢复，无需任何人工干预。

**Why this priority**: 接入 + 隧道是客户端存在的全部前提；不通则 032/033 无从谈起。

**Independent Test**: 干净 Linux 环境（CI：netns + 真服务端）执行 onboard 子命令序列 → 启动 daemon → 接口出现、获得 VPN 地址、ping 服务器通；杀进程重启 daemon → 隧道自动恢复（凭据/状态落盘生效）。

**Acceptance Scenarios**:

1. **Given** 干净路由器与可达的 lanweave 服务器，**When** 用户依次执行「设服务器地址 → 登录 → 注册节点」CLI 命令，**Then** 每步有明确成功输出；节点在服务端以 `platform=openwrt` 出现；凭据与状态文件落盘且权限为仅 root 可读写（0600）。
2. **Given** onboard 完成，**When** 启动守护进程，**Then** 内核 WireGuard 接口出现、携带分配的 VPN 地址与 VPN 网段路由、与服务器完成首次握手（宪法 IV：≤3 秒）并保持 keepalive；从路由器 ping 服务器 VPN 地址通。
3. **Given** 守护进程在跑，**When** 路由器重启（或进程被杀后由 procd 拉起），**Then** 隧道自动恢复，无需重新登录或重新注册。
4. **Given** 登录态的 access token 过期，**When** 任意 CLI/daemon 操作触发 API 调用，**Then** 客户端用 refresh token 静默换新（024 语义），操作成功且用户无感知。
5. **Given** 服务器证书过不了系统 CA（自签），**When** 首次连接，**Then** CLI 显示证书指纹并要求显式确认（或经 flag 预置指纹）；确认后指纹持久化，后续连接静默通过；证书变更时拒绝连接并提示重新信任（018 语义）。
6. **Given** 注册时用户已达设备配额或名称冲突，**Then** CLI 以非零退出码结束并输出与服务端错误码对应的人类可读信息。

---

### User Story 2 - zone 管理与状态查看 (Priority: P2)

接入后，用户在路由器上直接用 CLI 管理互通关系：创建 zone（本机自动入组）、用名称+密码加入他人 zone、退出 zone、列出自己所在的 zones 与成员清单（含成员名称/IP/归属），以及一条 status 命令查看本机概况（连接状态、VPN 地址、最后握手时间、所属 zones）。

**Why this priority**: 没有 zone 操作，路由器接入后谁也访问不了；但它依赖 US1 的会话与隧道。

**Independent Test**: CI 集成测试：onboard 后经 CLI 创建 zone、第二个节点加入、双向列表可见；status 输出含连接/握手/IP；（netns 拓扑下）同 zone 互 ping 通。

**Acceptance Scenarios**:

1. **Given** 已 onboard 的路由器，**When** 执行创建 zone 命令（名称+密码），**Then** zone 创建成功且本机自动成为成员（015 语义）；列表命令立即可见。
2. **Given** 他人 zone 的名称与密码，**When** 执行加入命令，**Then** 加入成功；成员列表显示 zone 内全部成员的名称、VPN 地址与归属用户；密码错误时报「zone 或密码无效」（不可枚举）。
3. **Given** 本机在某 zone 中，**When** 执行退出命令，**Then** 退出成功并从成员列表消失。
4. **When** 执行 status 命令，**Then** 输出至少包含：守护进程运行状态、隧道连接状态、本机 VPN 地址、最后握手时间、所属 zone 清单；输出为人类可读且字段稳定（可被脚本 grep）。
5. **Given** 隧道已连接但与服务器的握手超过陈旧阈值（028 同款），**Then** 守护进程自动重建隧道会话并持续重试直至恢复，全程无需人工。

---

### User Story 3 - 退出登录与清盘 (Priority: P3)

用户要把路由器从某服务器迁走或转手：执行登出命令——客户端先调用服务器注销本机节点并吊销本设备的长期凭据，然后清空本地凭据、密钥与状态，停止隧道。服务器不可达时登出被阻止并明确提示，提供强制选项作为逃生口（接受服务端残留孤儿节点的后果）。

**Why this priority**: 生命周期收尾；缺失则换服务器只能手工抠文件（025 在 Windows 端解决过同样的问题）。

**Independent Test**: CI 集成测试：登出后服务端 node 消失、RT 失效、本地文件清空、wg 接口拆除；服务器不可达时登出失败且本地完好；`--force` 跳过远端清理仅清本地。

**Acceptance Scenarios**:

1. **Given** 已 onboard 且隧道在跑，**When** 执行登出命令且服务器可达，**Then** 服务端该节点消失（IP 释放、peer 移除、zone 成员清除——008 级联）、本设备 refresh token 被吊销、本地凭据/私钥/状态全部清除、隧道拆除；再次使用需重新 onboard。
2. **Given** 服务器不可达，**When** 执行登出，**Then** 命令以非零退出码失败并说明原因，本地状态保持完好（避免产生服务端孤儿，025 语义）。
3. **Given** 服务器永久不可达（机器报废迁移），**When** 执行登出加强制标志，**Then** 仅清理本地并明确警告服务端将残留孤儿节点。

---

### Edge Cases

- **onboard 中断**：任一步失败（网络断、配额拒绝）后重试不得留下半成品状态——要么可幂等续接，要么干净回滚（019 的教训：凭据/私钥/状态三者必须一致落盘）。
- **重复 onboard**：已 onboard 再次执行登录/注册类命令时给出明确提示（先登出），不得静默产生第二个节点。
- **daemon 未跑时的 CLI**：zone/status 等命令在守护进程未运行时仍能工作（直连 API 的部分照常，隧道相关字段如实显示「未连接」）。
- **时钟漂移路由器**：路由器时间不准导致 token 校验失败时，错误信息能指引用户（提示检查时间/NTP），不得表现为无限静默重试。
- **并发 CLI 调用**：两个 CLI 同时写状态文件不得损坏状态（文件锁或原子写）。
- **接口名冲突**：设备上已存在同名接口时启动失败要有清晰报错，不得静默覆盖他人接口。
- **IPv6-only 或 DNS 失败**：连接错误如实透传可操作的信息（与现有 apiclient 错误语义一致）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 客户端 MUST 以「守护进程 + CLI 子命令」形态交付单一可执行文件；CLI 全部非交互（flag 驱动），密码/邀请码可经标准输入传递；每个命令以退出码区分成败、输出对人类可读。
- **FR-002**: onboard MUST 覆盖：服务器地址配置 → 登录（已有账号）或注册（邀请码+新账号）→ 本机生成密钥对 → 以 `platform=openwrt` 注册节点；全流程成功后凭据（长期令牌）、设备私钥与状态一致落盘；任一步失败不得留下不一致的半成品（019 语义）。
- **FR-003**: 凭据/私钥/状态文件 MUST 以仅属主可读写（0600，root）权限落盘；任何命令输出与日志 MUST NOT 出现明文密码、私钥或令牌（宪法 Security）。
- **FR-004**: 守护进程 MUST 在启动时建立隧道：创建内核 WireGuard 接口、配置服务器 peer（AllowedIPs=VPN 网段）、本机 VPN 地址、VPN 网段路由、keepalive=25s（DESIGN §3 拓扑）；停止时拆除接口。
- **FR-005**: 守护进程 MUST 周期检查最后握手时间，已连接但握手陈旧（028 同款阈值语义）时自动重建会话并持续重试直至恢复；重试过程不退出进程。
- **FR-006**: access token 过期 MUST 用 refresh token 静默续期（024 语义）；续期也失败时停止重试并在日志/status 中明示需要重新登录。
- **FR-007**: TLS 信任 MUST 对齐 018：默认系统 CA 验证；过不了时 CLI 显式展示指纹并要求确认（或经 flag 预置指纹完成非交互信任），确认后指纹持久化；证书变更 MUST 拒绝并要求显式重新信任；保留完全跳过验证的逃生 flag（不持久化）。
- **FR-008**: zone 管理 CLI MUST 覆盖：创建（本机自动入组，015 语义）、按名称+密码加入、退出、列出所在 zones、列出指定 zone 成员（名称/VPN 地址/归属用户）；错误码映射为人类可读信息（zone 或密码无效不可枚举等，与现有协议语义一致）。
- **FR-009**: status 命令 MUST 输出守护进程运行状态、隧道状态、本机 VPN 地址、最后握手时间、所属 zones；字段名稳定可脚本化。
- **FR-010**: 登出 MUST 对齐 025：先调服务器注销本机节点并吊销本设备长期令牌，成功后清空本地（凭据/私钥/状态）并拆隧道；服务器不可达（有限重试后）则失败退出且本地完好；强制标志仅清本地并警告服务端残留。
- **FR-011**: MUST 附 procd init 脚本：开机自启、崩溃拉起、stop 时优雅拆隧道；以及最小安装文档（拷贝二进制 → onboard → 启用自启）。
- **FR-012**: 构建 MUST 提供 arm64 / amd64 / mipsle(softfloat) 三个 Linux 交叉编译目标（Makefile）；二进制大小适配 ≥64MB flash 设备（无需特化压缩）。
- **FR-013**: 本切片 MUST NOT 改变服务端任何行为，MUST NOT 触碰 Windows 客户端（`internal/client` 中 GUI/keyring/wireguard-go 相关部分零改动）；可复用的协议与 API 访问层按现状复用。
- **FR-014**: 并发 CLI 调用与 daemon 对状态文件的读写 MUST 不损坏状态（原子写或锁）。

### Key Entities

- **路由器节点（openwrt node）**: 与现有 node 同一服务端实体；platform=openwrt 使其具备 032 的宣告资格。
- **本地状态（router state）**: 服务器地址、节点身份（ID/VPN 地址/服务器公钥与 endpoint）、TOFU 指纹；0600 落盘；与凭据、私钥三者一致性是 onboard 的不变量。
- **凭据（credentials）**: 长期 refresh token + 短期 access token（仅内存或随状态落盘的 RT）；登出时吊销并清除。
- **守护进程（lanweave-routerd）**: 隧道生命周期的唯一属主：建立/保活/自愈/拆除。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 在干净环境按文档执行，从安装二进制到「ping 通服务器 VPN 地址」全流程 ≤10 分钟（含 onboard 三条命令）；首次握手 ≤3 秒（宪法 IV）。
- **SC-002**: 守护进程被杀/设备重启后，隧道在无人工干预下自动恢复；access token 过期跨越不引起任何用户可见失败。
- **SC-003**: zone 全生命周期（创建自动入组/加入/退出/列表/成员）100% 经 CLI 完成，无需任何 curl；同 zone 两节点（netns 拓扑）互 ping 通。
- **SC-004**: 登出后服务端与本地双向零残留（node/RT/凭据/私钥/状态/接口六处）；不可达时本地零损伤。
- **SC-005**: 现有全部自动化测试保持绿色（服务端与 Windows 客户端零回归）；三个交叉编译目标构建通过。
- **SC-006**: 全部失败路径（配额/名称冲突/密码错/证书变更/服务器不可达）CLI 均以非零退出码 + 可操作信息结束，无静默失败。

## Assumptions

- 守护进程与 CLI 以 root 运行（OpenWrt 常态；内核 wg 与 netlink 本就需要）。
- 复用 `pkg/protocol` 与现有 API 访问层（含 TOFU/重试语义）；复用方式（共包或抽取）属 plan 阶段决策。
- 配置/状态路径遵循自有文件而非 UCI（LuCI/UCI 集成留给后续 LuCI 切片）；具体路径 plan 阶段定（倾向 `/etc/lanweave/` 风格）。
- CI 验收在 Linux netns 中以真内核 WireGuard + 真服务端完成（宪法 II）；实机 OpenWrt（arm64 或 mipsle 任一台）走人工 quickstart 矩阵——GUI/实机豁免先例（017/018 登记）同样适用于「真实路由器硬件」维度。
- 凭据明文 0600 落盘是接受的风险（设备无 keyring；root 被攻破则一切皆失）；随本切片登记 DESIGN §11。
- 不做：032 宣告 CLI 与地址翻译下发、033 合成段路由消费（本期 AllowedIPs 固定为 VPN 网段）、LuCI、.ipk 打包与发布流水线、多语言 CLI 输出（英文单语，i18n 是 GUI 范畴）。

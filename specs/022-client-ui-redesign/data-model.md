# Phase 1 Data Model: client-ui-redesign

本切片是视觉/交互改版,几乎不引入新的持久化数据;模型以**视图数据 + 一处新瞬时数据(流量)+ 设计令牌**为主。下列实体多为现有结构的复用说明,新增项明确标注。

---

## 1. TrafficStats(新增,瞬时,不落盘)

隧道暴露的 per-peer 累计收发字节,驱动 Hero 卡片的 ↑/↓ 显示。

| 字段 | 类型 | 说明 |
|------|------|------|
| RxBytes | int64 | 下行累计字节(从 WG UAPI `rx_bytes=` 解析/求和) |
| TxBytes | int64 | 上行累计字节(从 WG UAPI `tx_bytes=` 解析/求和) |

- **来源**:`Tunnel.Transfer() (rx, tx int64, err error)`(新增);底层 `wgEngine.transfer()` 解析 `dev.IpcGet()` 文本(复用 `handshaked()` 同一路径)。
- **生命周期**:仅 `State()==Connected` 时有效;`Disconnected/Connecting` 或 `eng==nil` → `(0,0,nil)`。断开后下次连接为新设备实例,自然从 0 起(满足「断开复位」)。
- **刷新**:连接期间 UI 以 ~2s 节奏轮询;断开停止并隐藏。
- **验证规则**:累计值单调不减(同一连接内);UI 显示前经字节→可读单位格式化(B/KB/MB/GB)。
- **不落盘**:不写 `state.json`、不入 keyring。

---

## 2. DeviceView(复用,不改)

`internal/client/panel.DeviceView`,改版只新用其既有字段,**不新增后端字段**。

| 字段 | 类型 | 改版用途 |
|------|------|----------|
| Name | string | 节点行设备名 + Hero 设备名 |
| IP | string | 等宽 VPN IP |
| LastSeen | string(RFC3339,可空) | **离线行「N 分钟前离线」的数据源**(`now - LastSeen` 取分钟;空则只显「离线」) |
| Online | bool | avatar 右下状态点颜色 + 在线/离线判定 |
| IsThisMachine | bool | 本机行「本机」chip + 高亮背景 + 纯展示(不可点) |

- 由 `Controller.Devices()` 组装(现有);改版消费方从 `widget.NewLabel(fmt...)` 改为 avatar+状态点行。

---

## 3. ZoneView / MemberView(复用,不改)

`panel.ZoneView{Name, IsOwner}`、`panel.MemberView{NodeID, NodeName, Owner, IP}`。

- **ZoneView**:区域行(扁平 avatar + 名 + owner chip);`IsOwner` 决定区域详情内是否显示改密/踢人/删除。
- **MemberView**:区域详情成员列表行(名/IP/owner);owner 视角对非空 `Owner` 成员显示踢人。
- 由 `Controller.Zones()` / `Controller.Members(name)` 组装(现有,不动)。

---

## 4. TrustStatus(派生,呈现态)

当前会话证书信任态,决定 App Bar overflow 菜单中的信任项(三选一,互斥)。

| 态 | 判定 | overflow 呈现 |
|----|------|---------------|
| system-CA | 非 insecure 且 `PinnedCertSHA256==""` | 无信任项 |
| pinned(TOFU) | `PinnedCertSHA256!=""` | 中性「已在本机信任」(`trust.selfSignedNote`) |
| insecure | `Controller.Insecure()==true` | 红色「证书未验证」(`trust.notVerified`) |

- **来源**:`Controller.Insecure()`(现有)+ `state.Record.PinnedCertSHA256`(现有)。改版只把渲染从「常驻警告条/页脚标签」迁到 overflow 菜单项。无新增数据。

---

## 5. FirewallPreference(复用,不改)

`state.Record.FirewallAllowVPN`(bool,默认 false,来自 018)。

- 改版仅以**自绘 Switch** 替代 `widget.Check` 呈现同一偏好;读写仍走 `Controller.FirewallAllowed()` / `SetFirewallAllowed(on, connected)` / `ReconcileFirewall(connected)`(全部现有,语义/生命周期不变)。
- CIDR 副标题取 `firewall.VPNSubnet`(现有常量)。

---

## 6. UILanguagePreference(复用,不改)

Fyne Preferences `ui.language`(空=跟随系统),来自 020。

- overflow 菜单的语言子菜单复用 `newLanguageSelect` 的偏好读写(`i18n.PrefForIndex/IndexForPref`),重启生效(沿用 020)。

---

## 7. ThemeTokens(新增,常量,非运行时数据)

`internal/client/ui/theme.go` 顶层共享的设计令牌(取自 `UI-DESIGN.md §2/§3/§6`),供主题与自绘控件引用。

| 组 | 令牌 |
|----|------|
| 品牌色 | brandIndigo `#1A1B3A`、brandCyan `#06D9D5`、brandCyanFaded(本机行高亮) |
| 表面 | surfaceBase `#12131A`、surfaceA `#1C1D28`、surfaceB `#22232E`(卡片) |
| 文本 | textPrimary `#E6E7EA`、textSecondary `#9CA0AB`、textTertiary `#6A6E7A`(离线文字) |
| 语义 | success `#4ADE80`(在线/已连接)、警告红(insecure/连接失败)、分隔线 `rgba(255,255,255,0.08)` |
| 字号 | text 14 / caption 12 / subHeading 16 / heading 18;设备名 18、IP/CIDR 13 mono |
| 间距/圆角 | padding 12 / inner 8 / 卡片圆角 12 / pill 999 |

- 强制深色:`LanweaveTheme.Color` 未覆盖项以 `theme.VariantDark` 回落,忽略系统 variant。

---

## 关系与不变量

- **Hero 卡片** 聚合:`tunnel.State()`(状态行)+ `DeviceView`(本机名/IP)+ `TrafficStats`(↑/↓)+ `FirewallPreference`(Switch)。
- **流量不变量**:`TrafficStats` 显示 ⟺ `State()==Connected`;断开 → 隐藏 + 复位。
- **信任不变量**:三态互斥,同一会话至多一个 overflow 信任项。
- **本机不变量**:`IsThisMachine` 行不可点击(纯展示),其余节点行同样纯展示(节点无详情)。
- **零回归不变量**:所有控制器方法签名/语义不变;唯一新增是 `Tunnel.Transfer()` 与 `engine.transfer()`。

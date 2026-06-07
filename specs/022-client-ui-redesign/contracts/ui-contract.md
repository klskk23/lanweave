# Contract: UI 改版的包内 API 面 + 控件/主题/布局约定

桌面应用无对外网络/CLI 接口;此契约约束**包内 API 面**与**客户端内部 UI 约定**,供 tasks/implement 与测试对齐。带 `*` 的为本切片新增,其余为**稳定性契约**(签名/语义不得变,守住「行为零回归」)。

---

## C1 — `internal/client/tunnel` 流量暴露(新增,唯一非 UI 改动)

```go
// engine 接口新增(seam,可注入 fake):
transfer() (rx, tx int64, err error)

// Tunnel 新增:连接期间返回累计收发字节;未连接(eng==nil / 非 Connected)返回 (0,0,nil)。
func (t *Tunnel) Transfer() (rx, tx int64, err error)
```

**契约要点**
- `wgEngine.transfer()` 调既有 `dev.IpcGet()`,逐行解析 `rx_bytes=` / `tx_bytes=`(WG UAPI per-peer 累计字节;多 peer 行求和)。复用 `handshaked()` 同一文本路径,**不新增系统调用面、不新增依赖**。
- `Tunnel.Transfer()` 加 `t.mu` 读 `t.eng`;`eng==nil` → `(0,0,nil)`,不返回错误。
- 累计语义(非速率):值单调不减;断开后下次连接为新 `device` 实例,自然从 0 起。
- `State()`/`Connect()`/`Disconnect()`/`Close()`/`InterfaceName()` 签名与语义**不变**。
- 可测:fake engine 喂固定 UAPI 文本,断言 `Transfer()` 解析/求和(`tunnel_test.go`,纯逻辑,无 gui tag)。

---

## C2 — `internal/client/panel.Controller` 面(稳定性契约,不改)

UI 改版**不得改动**下列方法签名或语义(只改调用位置/呈现):

```go
LoadSession() (needSignIn bool, err error)
SignIn(username, password string) error
Devices() ([]DeviceView, error)            // DeviceView{Name,IP,LastSeen,Online,IsThisMachine}
Zones() ([]ZoneView, error)                // ZoneView{Name,IsOwner}
Members(zoneName string) ([]MemberView, error)
CreateZone(name, password string) error    // 自动入组(015)
JoinZone(name, password string) error
LeaveZone(name string) error
ChangePassword(name, password string) error
KickMember(name string, nodeID int64) error
DeleteZone(name string) error
Logout() (remoteRemoved bool, err error)
Insecure() bool
FirewallAllowed() bool
SetFirewallAllowed(on, connected bool) error
ReconcileFirewall(connected bool) error
SetPinnedCert(fp string) error
UseClient(a api)
```

**契约要点**:改版把区域操作(成员/退出/改密/删除/踢人)迁入区域详情 sheet、把退出迁入 overflow、把防火墙开关迁入 Hero,均**只换触发位置**,底层仍调上述方法。`onboard.Provisioner` 同样不改。

---

## C3 — 自绘控件契约(`internal/client/ui/`,gui tag,新增)

| 控件 | 构造/行为 |
|------|-----------|
| `Switch`* | `NewSwitch(onChange func(bool)) *Switch`;`On bool`;`Tapped` 翻转 `On`、调 `OnChange(On)`、`Refresh()`。renderer:圆角 track + thumb 左/右定位。**替代 `widget.Check`**(FR-007)。可 headless 断言 `On` 翻转 + 回调。 |
| Avatar* | `makeAvatar(initial string, online bool) fyne.CanvasObject`:圆底 + 首字母 + 右下角状态点(`statusColor(online)`)。 |
| Pill chip* | `makeChip(text string, bg, fg color.Color) fyne.CanvasObject`:`CornerRadius:999` 矩形 + padded text。用于「本机」「owner」标签。 |
| 状态指示器* | 着色 `canvas.Circle`(圆点)+ 着色 `canvas.Text`(简短文本),**非 `[...]` 括号文本**(FR-005)。 |

**契约要点**:控件颜色引用 `theme.go` 顶层共享令牌;均可在 `fyne.io/fyne/v2/test` 下无头实例化测试。

---

## C4 — 主题契约(`internal/client/ui/theme.go`,gui tag,新增)

```go
func NewTheme() fyne.Theme   // *LanweaveTheme,内嵌 theme.DefaultTheme()
```

**契约要点**
- `Color(name, variant)`:覆盖 `Primary=brandCyan`、`Background=surfaceBase`、`InputBackground=surfaceB`、`Foreground=textPrimary`、`PlaceHolder=textTertiary`、`Success=success`、`Separator=半透明白`;**未覆盖项以 `theme.VariantDark` 回落**(忽略传入 variant)→ 强制深色(FR-001)。
- `Size(name)`:padding 12 / inner 8 / text 14 / caption 12 / subHeading 16 / heading 18 / 输入圆角 8。
- 顶层导出/包内共享设计令牌(brand*/surface*/text*/success),供 C3 控件复用,避免重复定义。
- 无渐变/阴影/glow(FR-001 / §8)。

---

## C5 — 主面板布局契约(`internal/client/ui/panel.go`,gui tag,重写)

| 区域 | 约定(对应 FR) |
|------|----------------|
| App Bar | 顶部 48px;左对齐 logo(`AppIcon()`)+「lanweave」(16/500);底部 0.5px 分隔线;**标题不居中**。右侧 ⋮ overflow。(FR-002) |
| overflow ⋮ | 语言子菜单(跟随系统/中文/English,复用偏好读写,重启生效)+ 置底红色「退出登录」(走 `confirmLogout`→`Logout()`);信任项:insecure→红「证书未验证」、pinned→中性「已在本机信任」、system-CA→无项。**不新建设置/关于**。(FR-003/004) |
| Hero 卡片 | surfaceB 圆角卡;状态行(圆点+文本:已连接/正在连接/未连接/连接失败 + 连接时 ↑/↓ 流量)+ 设备名(18)+ VPN IP(13 mono)+ **单一 pill 主按钮**(按 `tunnel.State()` 切「立即连接/断开连接」)+ 分隔线 + 「允许 VPN 入站访问」Switch + CIDR(`firewall.VPNSubnet`)。(FR-005/006/007/012) |
| Tabs | 「节点 N」「区域 N」带计数 + leading icon;选中 2px brandCyan 指示条;名为「节点/区域」**非「我的…」**。(FR-008) |
| 节点行 | avatar+右下状态点 + 名 + IP(mono);离线拼「N 分钟前离线」(用 `DeviceView.LastSeen`);本机行 +「本机」chip + 高亮 + **纯展示不可点**;离线文字用 textTertiary。(FR-009) |
| 区域行 | 扁平 avatar + 名 +(owner)owner chip;**整行可点 → 区域详情 sheet**(成员/退出/改密/删除/踢人,owner 判定沿用 `ZoneView.IsOwner`)。(FR-010) |
| FAB | 右下角「+」浮按钮 → 弹「创建/加入」二选一(复用 `onCreateZone`/`onJoinZone`)。(FR-011) |

**不变量**:连接状态经 Hero 在主屏常驻可见(宪法 III);破坏性操作仍二次确认且点名实体;长操作仍有进度反馈。

---

## C6 — i18n 文案契约(`en.json` + `zh-Hans.json`,双语同步)

- 新增/改写键(示例,最终以实现为准):`status.connected/connecting/disconnected/failed`、`panel.connect`(立即连接)、`panel.disconnect`(断开连接)、`panel.allowInbound`、`panel.thisMachineTag`(本机)、`panel.offlineSince`(`%s 分钟前离线`)、`panel.tabNodes`(节点)、`panel.tabZones`(区域)、`traffic.up`/`traffic.down`、`trust.notVerified`(复用,入 overflow)、`trust.selfSignedNote`(复用,入 overflow)、overflow/FAB/区域详情相关键。
- **键集一致**(不变量):`keys(en)==keys(zh-Hans)`,`i18n_test.go` 双向断言;删除旧键(居中标题/旧 tab 名)时两边同删。
- 占位 `%s/%d` 中英数量与顺序一致;禁含敏感值。

---

## C7 — 启动挂主题(`cmd/lanweave-client/main.go`,gui tag)

1. `a := app.NewWithID("com.lanweave.client")`(既有)。
2. **新增一行**:`a.Settings().SetTheme(ui.NewTheme())`(在 `i18n.Init` 与任何 `ui.NewWizard`/`ui.NewPanel` 之前或之后均可,但须在 `ShowAndRun` 前)。
3. 其余启动逻辑(提权、i18n、图标、firewall 清扫、StartupTarget 分支)**不变**。

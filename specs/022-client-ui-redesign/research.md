# Phase 0 Research: client-ui-redesign

研究目标:把 spec 与 `UI-DESIGN.md` 的视觉/交互要求落到 Fyne 的具体可行手段,并验证两个有实现风险的点——**自绘控件 + 强制深色主题** 与 **隧道暴露 per-peer 流量字节**。所有结论以仓库现状(已读 `tunnel.go`、`panel.go`、`panel/panel.go`、`wizard.go`、`main.go`、`i18n.go`、`UI-DESIGN.md §6`)为依据。

---

## 1. 自定义 Theme:强制深色

**Decision**: 在 `internal/client/ui/theme.go`(`//go:build gui`,`package ui`)实现一个嵌入 `fyne.Theme` 的 `LanweaveTheme`,`Color()` 用品牌色覆盖 `ColorNamePrimary/Background/InputBackground/Foreground/PlaceHolder/Success/Separator`,**未覆盖项一律以 `theme.VariantDark` 调用内嵌默认主题**(忽略传入 variant),从而无视系统明暗强制深色;`Size()` 覆盖 padding/text/heading 等。`cmd/lanweave-client/main.go` 在 `app.NewWithID` 后加一行 `a.Settings().SetTheme(ui.NewTheme())`。

**Rationale**: 这正是 `UI-DESIGN.md §6` 给出的样例做法,且 Fyne 主题 API 稳定。把 fallback 固定为 `VariantDark` 是「强制深色」的关键一招——无需任何运行时探测系统主题。色板/字号直接采用 §6 常量(BrandIndigo/Cyan、surfaceBase/A/B、textPrimary/Sec/Ter、success)。

**Alternatives considered**: 监听系统 variant 再切换 → 被否(spec 明确强制深色,不要浅色);用全局 canvas 颜色硬编码绕过主题 → 被否(破坏 Fyne 控件一致性,且 §5 反例与宪法 I 都反对散落样式)。

**Risk/Note**: 主题色常量被自绘控件(Switch/Avatar/Chip)直接引用,故把这些 `color.NRGBA` 常量定义在 `theme.go` 顶层、包内共享,避免重复。

---

## 2. 自绘控件:Switch / Avatar+状态点 / Pill chip / 状态指示器

**Decision**: Fyne 无原生 Switch,按 §6 用 `widget.BaseWidget` + 自定义 renderer 实现 `Switch`(track 圆角矩形 + thumb 圆,`Tapped` 翻转并回调)。Avatar 用 `container.NewWithoutLayout(bg圆, 首字母text, 右下角 statusDot圆)`;Pill chip 用 `canvas.Rectangle{CornerRadius:999}` + padded text;状态指示器 = 一个着色小圆 `canvas.Circle` + 一段着色 `canvas.Text`(替代 `[在线]` 括号文本)。全部放 `internal/client/ui/widgets.go`(或按控件拆分),gui tag。

**Rationale**: 均为 §6 已给出骨架的纯 Fyne 组合,无第三方依赖。自绘控件可在 `fyne.io/fyne/v2/test` 下 headless 实例化并断言状态(`On` 翻转、回调触发),满足宪法 II 的「每 story ≥1 自动化测试」。

**Alternatives considered**: 用 `widget.Check` 充当开关(被 §5 反例与 spec FR-007 明确禁止);引入第三方 Material 控件库(违反宪法 I「无新依赖/无早抽象」且 Fyne 生态无成熟件)。

**Risk/Note**: 自绘 renderer 的 `Layout/MinSize` 要写对,否则窄窗口溢出(edge case)。pill 主按钮用内置 `widget.Button` + `HighImportance` + 主题 `SelectionRadius`/button radius 调大到 999 近似;若内置按钮圆角受限,退化为 `canvas.Rectangle{CornerRadius:999}` 叠 `widget.Button` 的自绘按钮——plan 阶段不锁死,tasks 里先试内置。

---

## 3. 隧道暴露 per-peer 流量字节(唯一非 UI 改动)

**Decision**: 复用 `wgEngine` 已有的 `dev.IpcGet()` UAPI 文本路径(`handshaked()` 已在解析它)。在 `engine` 接口加 `transfer() (rx, tx int64, err error)`;`wgEngine.transfer()` 调 `IpcGet()`,逐行解析 `rx_bytes=` / `tx_bytes=`(WireGuard UAPI 的 per-peer 累计字节,单 peer 场景直接取该 peer 行的值,多行则求和);`Tunnel.Transfer() (rx, tx int64, err error)` 加锁读 `t.eng` 后委托,未连接(`eng==nil`)返回 `(0,0,nil)`。UI 侧 `panel.go` 在 Connected 时以 ~2s 节奏调一次,`fyne.Do` 更新 Hero 的 ↑/↓;断开置零并隐藏。

**Rationale**: WireGuard 的 UAPI `get` 操作对每个 peer 输出 `rx_bytes`/`tx_bytes`,与 `last_handshake_time_sec`(现已解析)同一份文本——**零新增系统调用面、零新依赖**,且与现有 `handshaked()` 解析风格一致,符合宪法 I 的最小改动。fake engine 可喂一段固定 UAPI 文本断言解析正确,符合宪法 II「不 mock 真实系统、但接口 seam 可注入」。

**Alternatives considered**: 用 `wgctrl` 重新打开设备查 peer 统计(被否:客户端用 wireguard-go 用户态设备,`wgctrl` 走 netlink/UAPI socket,重复且更重);自己在 tun 读写处加计数器(被否:侵入数据面、易错、与 WG 既有计数重复)。

**Risk/Note**: `rx_bytes`/`tx_bytes` 是**累计**值,不是速率;Hero 直接显示累计(↑总上行/↓总下行),与 `UI-example.png` 的「↑2.4MB·↓18.7MB」一致(累计量)。断开后下次连接是新设备实例,计数自然从 0 起,符合「断开复位」。`transfer()` 在 `Connecting` 中途可能返回 0/err,UI 容错显示 0。

---

## 4. Tabs 计数徽标 / 区域详情 / FAB / 列表

**Decision**:
- **Tabs**:`container.NewAppTabs` + `NewTabItemWithIcon("节点 N", computerIcon, ...)`,标题内联计数;`refresh()` 拿到 devs/zones 长度后更新 tab 文本。2px 品牌青色选中指示条由主题 `ColorNamePrimary=brandCyan` 自然驱动(AppTabs 选中下划线取 primary)。
- **区域详情**:整行 `widget.Button`(或可点 `tappable` 容器)→ `dialog.ShowCustom`/自定义 sheet,内含成员列表 + 退出/改密/删除/踢人(把现有 `showMembers`/`confirmLeave`/`changePassword`/`confirmDelete`/`confirmKick` 逻辑迁入详情,owner 判定沿用 `ZoneView.IsOwner`)。
- **FAB**:右下角浮动「+」用 `container.NewStack`/`NewBorder` 把一个圆形 `widget.Button` 叠在内容右下;点弹「创建/加入」二选一(复用 `onCreateZone`/`onJoinZone`)。
- **列表**:节点/区域行用 `container.NewBorder(nil,nil, avatar, trailing, content)` 组装(§6 列表渲染范式);节点行纯展示(非 Button),区域行整行可点。

**Rationale**: 全部是 Fyne 内置容器/控件组合,§6 已示范。把现有 zone 操作迁入详情 sheet 不改控制器方法,只换触发位置——守住「行为零回归」。

**Alternatives considered**: 自写 tab 控件以画 count badge 圆形角标(被否:内联「节点 N」已满足 spec FR-008,圆角标是 §7「可选」之外的过度工程);用独立窗口而非 sheet 展示区域详情(被否:与单窗口 VPN 客户端的轻量交互不符)。

---

## 5. i18n 文案增量(沿用 020)

**Decision**: 在 `internal/client/i18n/en.json` 与 `zh-Hans.json` 同步新增/改写键:`status.connected/connecting/disconnected/failed`(状态文本)、`panel.connect`→「立即连接」、`panel.disconnect`→「断开连接」、`panel.allowInbound`、`panel.thisMachineTag`→「本机」、`panel.offlineSince`(含 `%s` 分钟,如「%s 分钟前离线」)、`trust.notVerified`/`trust.selfSignedNote`(复用,放进 overflow)、`traffic.up`/`traffic.down`、`panel.tabNodes`→「节点」、`panel.tabZones`→「区域」、overflow/FAB/区域详情相关键。删除/改写不再使用的旧键(如居中标题、旧 tab 名),由 `i18n_test.go` 的 catalog-parity 测试守住双语齐平。

**Rationale**: 020 已确立「字符串只在 ui 层 + 双 catalog + parity 测试」,本期只是增量;不引入新机制。`T(key, args...)` 的 fmt 能力直接支持「N 分钟前离线」。

**Alternatives considered**: 复用 Fyne `lang` 的 plural 形式做「分钟」复数 → 被否(中文无复数、英文「minute(s)」用简单措辞即可,不值得引 ICU 复杂度)。

---

## 6. headless 控件测试策略

**Decision**: 用 `fyne.io/fyne/v2/test`(无需 OpenGL,CI headless 可跑)在 `*_test.go`(gui tag)里:`test.NewApp()` 挂 `LanweaveTheme`,构建控件/面板片段,断言——单按钮文案随 `tunnel.State` 切换、Switch `On` 反映并回写 firewall 偏好(用 fake controller/api)、本机行带「本机」chip + 高亮色、区域行 `Tapped` 打开详情、状态指示器渲染圆点而非括号文本。纯函数(流量字节格式化、离线相对时间、`Transfer()` UAPI 解析)走无 gui-tag 的普通 `go test`。

**Rationale**: Fyne `test` 包正是为无头控件验证设计,既满足宪法 II「每 story ≥1 自动化验收」,又把 OpenGL 依赖留给 Mesa VM 人工矩阵(纯视觉)。沿用 017/018/020 的「controller/逻辑自动化 + GUI 人工矩阵」分工。

**Alternatives considered**: 截图比对(golden image)→ 被否(`rsvg`/渲染跨版本字节不稳,016 已记同类教训;视觉交人工)。

---

## 7. DESIGN.md 同步(宪法强制)

**Decision**: 同 PR 修订 `DESIGN.md`:
- **§9.4 主面板**:重写为新布局——App Bar(logo + lanweave + ⋮ overflow:语言/退出登录/信任状态);Hero 卡片(状态圆点+设备名+VPN IP+单一连/断 pill 按钮+「允许 VPN 入站访问」Switch+CIDR);Tabs「节点 N / 区域 N」;节点行 avatar+状态点+本机 chip(纯展示);区域行点击进详情(成员/退出/改密/删除/踢人);右下 FAB 创建/加入;连接时 Hero 显示 ↑/↓ 流量。注明「feature 022 改版」。
- **§282 / §285**:把「主面板显示中性指示」「`--insecure` 常驻『证书未验证』警示」改记为「位于 App Bar overflow(⋮)菜单的菜单项(自签已钉=中性『已在本机信任』、insecure=红色『证书未验证』);022 改版后不再为常驻警告条——已接受的 UX 取舍(信号弱于 018 常驻条,换主界面整洁)」。

**Rationale**: 宪法「DESIGN.md authority」:spec 可细化但不得抵触 DESIGN.md,若抵触须同 PR 更新。改版的 tab 改名、页脚→Hero、常驻信任条→overflow 都与 §9.4/§282/§285 字面冲突,故必须改。§11 风险登记无需新增(信任弱化属 UX 取舍,非新安全风险;`--insecure` 仍不入 UI 开关、行为不变)。

**Alternatives considered**: 不改 DESIGN.md、仅在 spec 记取舍 → 被否(违反宪法,DESIGN.md 会与实现长期不符)。

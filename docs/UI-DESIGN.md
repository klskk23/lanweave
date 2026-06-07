# Lanweave 客户端 GUI 设计规范

> 这份文档是 lanweave 桌面客户端（Fyne 实现）的视觉与交互规范，给 Claude Code 在实现 UI 代码时遵循。
> 风格定位：**Material 3 启发，深色优先，flat 设计，品牌一致**。

---

## 1. 核心理念

- **Flat 优先**：无渐变、无阴影（除了功能性的 focus ring）、无 glow / blur 装饰
- **状态用颜色和位置表达，不用文字括号**：`[离线]` 这种 ASCII 风格状态文本一律改为视觉指示（圆点 + 颜色 + 位置）
- **每个 UI 单元只回答一个语义问题**：Hero 卡片回答"我是谁、我在做什么"，列表回答"网络里还有谁"
- **次要操作收纳到 overflow 菜单**，不在主界面上堆砌
- **去掉冗余前缀**：在自己的 app 里 "我的节点" 简化为 "节点"，"我的区域" 简化为 "区域"
- **副标题携带技术细节**：主标题给意图，副标题给上下文（CIDR、IP、时间戳等）

---

## 2. 色彩系统

### 品牌色

| 名称 | 色值 | 用途 |
|------|------|------|
| `BrandIndigo` | `#1A1B3A` | 主背景、按钮填充、Switch 轨道 |
| `BrandCyan` | `#06D9D5` | 主要操作色、状态高亮、Tab indicator、按钮文字 |
| `BrandCyanFaded` | `rgba(6, 217, 213, 0.06)` | 当前/选中行的浅色背景 |
| `BrandCyanChipBg` | `rgba(6, 217, 213, 0.12)` | "本机" 等小标签的背景 |
| `BrandCyanChipText` | `#0F6E56` | "本机" 标签的文字色 |

### 语义色（沿用 Fyne theme 或自定义）

| 状态 | 色值（深色模式参考） |
|------|-------------------|
| 在线 / 已连接 / 成功 | `#4ADE80`（绿） |
| 警告 / 连接中 | `#FAC775`（黄） |
| 错误 / 失败 | `#E24B4A`（红） |
| 离线 / 中性 | 文字 `tertiary`，背景 `secondary` |

### 中性色（深色模式）

| Token | 用途 | 参考色值 |
|-------|------|---------|
| `BackgroundBase` | 应用底色 | `#12131A` |
| `SurfacePrimary` | 主卡片背景 | `#1C1D28` |
| `SurfaceSecondary` | 次卡片 / Hero 卡片 | `#22232E` |
| `Divider` | 分割线（0.5px） | `rgba(255,255,255,0.08)` |
| `TextPrimary` | 主文字 | `#E6E7EA` |
| `TextSecondary` | 副文字 | `#9CA0AB` |
| `TextTertiary` | 离线 / 提示 | `#6A6E7A` |

---

## 3. 排版规则

### 字号层级

| 用途 | 字号 | 字重 |
|------|------|------|
| App 标题 / Section 标题 | 16-18 px | 500 |
| 列表项主标题 | 14 px | 500 |
| 卡片副标题 | 18 px | 500 |
| 正文 / Tab 文字 | 14 px | 400 |
| 提示 / 副标题 / IP / 时间 | 12-13 px | 400 |

### 字体选择

- 中文 + 英文文字：系统默认 sans-serif（Fyne 默认即可）
- IP 地址、CIDR、端口号、token、时间戳：**等宽字体（monospace）**，提高对齐性和可读性
- **不用 weight 600 / 700**（太重，与 Material 3 不符）：只用 400 和 500

### 排版约定

- 字号不低于 11 px
- 全部 sentence case，不用 ALL CAPS，不用 Title Case
- 不在句子中间加粗
- 行间距 1.5-1.7

---

## 4. 布局模式

### 顶层结构

```
┌─────────────────────────────────────────┐
│ App Bar                          ⋮      │  ← 高 48px
├─────────────────────────────────────────┤
│                                         │
│   Hero 卡片（本设备状态 + 设置）          │  ← 上半部分
│                                         │
│   ──────────                            │
│   Tab 1   Tab 2                         │  ← Tab indicator (2px)
│   ━━━                                   │
│                                         │
│   列表（节点 / 区域 / …）                 │  ← 下半部分，可滚动
│                                         │
└─────────────────────────────────────────┘
```

### App Bar (顶部应用栏)

- 高度 48px
- **左对齐**：logo（24x24） + 应用名（"lanweave"，16px / weight 500）
- 右对齐：overflow 菜单（⋮ 三点图标）
- 不要居中放大标题
- 底部有 0.5px divider

**Overflow 菜单内容**:
- 语言切换（跟随系统 / 中文 / English ）
- 设置
- 关于
- 退出登录（放在最后，红色文字）

### Hero 卡片（本设备）

整个卡片表达"本设备在 VPN 中的完整状态"。结构:

```
┌─────────────────────────────────────────┐
│ ● 已连接          ↑ 2.4 MB · ↓ 18.7 MB  │  status row
│                                         │
│ GAME-PC                                 │  title (18px/500)
│ 100.127.0.2                             │  subtitle (13px mono)
│                                         │
│ [        断开连接        ]              │  pill button
│                                         │
│ ─────────────────────────────────────   │  divider
│                                         │
│ 允许 VPN 入站访问            ⚪──      │  toggle row
│ 100.127.0.0/16                          │
└─────────────────────────────────────────┘
```

- 背景：`SurfaceSecondary`
- 圆角：`border-radius-lg` (12px)
- 内边距：20px
- 主要按钮是 pill 形（`border-radius: 999px`），全宽
- 分割线后面放设备级 toggle（如"允许入站"），不要放到主界面其它位置

### 状态指示器 (Status Indicator)

```
● 已连接
```

- 8-10px 圆点 + 文字
- 颜色配套：
  - 绿 `#4ADE80` + "已连接"
  - 黄 `#FAC775` + "正在连接…"
  - 灰 `TextTertiary` + "未连接"
  - 红 `#E24B4A` + "连接失败"
- 文字字号 13px，weight 500，颜色与圆点一致

### Tabs

- 横向布局，gap 24px
- 每个 tab 含可选 leading icon (16px) + 文字 + 可选 count badge
- 活动 tab: weight 500，底部 2px BrandCyan indicator
- 非活动 tab: 文字 `TextSecondary`，无 indicator
- 整体下方 0.5px divider 跨整宽

### 列表项（Peer / Zone）

固定结构: `Avatar (36px) | Title/Subtitle (flex) | Trailing`

```
┌─────────────────────────────────────────┐
│ ⬤   GAME-PC                       本机  │
│  ●   100.127.0.2                        │
└─────────────────────────────────────────┘
```

- 上下 padding 12px，左右 padding 8px
- 整行点击是该实体的"详情/编辑"操作
- Avatar:
  - 36x36 圆形
  - 内容：首字母（大写）/ 设备图标 / zone 标识
  - **右下角叠加 11px 状态点**（在线绿、离线灰、警告黄），带 2px 背景色描边
- 主标题：14px / 500
- 副标题：12px / mono / TextSecondary，可拼接信息（如 `100.127.0.3 · 3 分钟前离线`）
- 自己设备 / 当前选中的项：背景换 `BrandCyanFaded`，圆角 8px
- 状态文字（如"本机"）放在 trailing 位置，pill chip 样式

### Pill Chip（小标签）

```
本机    管理员    新设备
```

- 字号 11-12px / weight 500
- 内边距：horizontal 10px / vertical 3px
- `border-radius: 999px`
- 背景色 + 文字色用同色系（如 BrandCyanChipBg + BrandCyanChipText）

### Switch

代替 checkbox。所有"启用/禁用"的开关一律用 Switch。

```
开启状态:  ━●  →  cyan dot 在右边，背景 BrandIndigo
关闭状态:  ●━  →  灰 dot 在左边，背景 灰色
```

- 轨道宽 36px / 高 20px
- 滑块 16x16
- 整行结构：左侧主+副标题 (flex)，右侧 Switch (固定宽)

### 主操作按钮

- **Filled button**（主要操作如"断开连接"、"创建 zone"）：
  - 背景 `BrandIndigo` / 文字 `BrandCyan`
  - 圆角 999px (pill)
  - 高 44px，内边距 12px
  - 全宽（在 Hero 卡片里）或固定宽
- **Outlined button**（次要操作）：
  - 透明背景，0.5px BrandCyan 边框，文字 BrandCyan
  - 圆角 8px

### 间距

| 用途 | 值 |
|------|-----|
| 卡片内边距 | 20px |
| 卡片之间间距 | 16-20px |
| 列表项垂直间距 | 12px |
| Section 之间间距 | 20-24px |
| 相邻元素 gap | 8-12px |

---

## 5. 反例（不要做什么）

❌ **不要在标题里写状态括号**: `hyperv-test [离线]`  
✅ 用 avatar 的状态圆点 + trailing 描述时间

❌ **不要同时显示"连接"和"断开"按钮**（其中一个永远 disabled）  
✅ 用状态切换的单一主按钮："断开连接" / "立即连接"

❌ **不要用 checkbox 表达开关状态**  
✅ 用 Switch

❌ **不要把"退出登录"放在主界面常驻**  
✅ 移到 overflow 菜单底部

❌ **不要 App Bar 用居中大标题**  
✅ Logo + 左对齐文字

❌ **不要 "我的节点" / "我的区域"**  
✅ "节点" / "区域"

❌ **不要混用全角和半角**: `状态:未连接`  
✅ `状态: 未连接` 或 `已连接` 单独成行

❌ **不要在按钮和卡片用同样的圆角**  
✅ 卡片 12px，按钮 999px（pill）

❌ **不要彩色渐变 / drop shadow / glow / 半透明遮罩**  
✅ Flat 配色 + 0.5px divider

❌ **不要把信息塞在一行括号里**: `(100.127.0.0/16)`  
✅ 用主+副标题两行表达

---

## 6. Fyne 实现要点

### 自定义 Theme

```go
package theme

import (
    "image/color"
    "fyne.io/fyne/v2"
    "fyne.io/fyne/v2/theme"
)

type LanweaveTheme struct{ fyne.Theme }

func New() *LanweaveTheme { return &LanweaveTheme{Theme: theme.DefaultTheme()} }

var (
    brandIndigo = color.NRGBA{R: 0x1A, G: 0x1B, B: 0x3A, A: 0xFF}
    brandCyan   = color.NRGBA{R: 0x06, G: 0xD9, B: 0xD5, A: 0xFF}
    surfaceBase = color.NRGBA{R: 0x12, G: 0x13, B: 0x1A, A: 0xFF}
    surfaceA    = color.NRGBA{R: 0x1C, G: 0x1D, B: 0x28, A: 0xFF}
    surfaceB    = color.NRGBA{R: 0x22, G: 0x23, B: 0x2E, A: 0xFF}
    textPrimary = color.NRGBA{R: 0xE6, G: 0xE7, B: 0xEA, A: 0xFF}
    textSec     = color.NRGBA{R: 0x9C, G: 0xA0, B: 0xAB, A: 0xFF}
    textTer     = color.NRGBA{R: 0x6A, G: 0x6E, B: 0x7A, A: 0xFF}
    success     = color.NRGBA{R: 0x4A, G: 0xDE, B: 0x80, A: 0xFF}
)

func (t *LanweaveTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
    switch name {
    case theme.ColorNamePrimary:
        return brandCyan
    case theme.ColorNameBackground:
        return surfaceBase
    case theme.ColorNameInputBackground:
        return surfaceB
    case theme.ColorNameForeground:
        return textPrimary
    case theme.ColorNamePlaceHolder:
        return textTer
    case theme.ColorNameSuccess:
        return success
    case theme.ColorNameSeparator:
        return color.NRGBA{R: 255, G: 255, B: 255, A: 20}
    }
    return t.Theme.Color(name, theme.VariantDark)
}

func (t *LanweaveTheme) Size(name fyne.ThemeSizeName) float32 {
    switch name {
    case theme.SizeNamePadding:        return 12
    case theme.SizeNameInnerPadding:   return 8
    case theme.SizeNameInputBorder:    return 0.5
    case theme.SizeNameInputRadius:    return 8
    case theme.SizeNameSelectionRadius: return 8
    case theme.SizeNameText:           return 14
    case theme.SizeNameCaptionText:    return 12
    case theme.SizeNameSubHeadingText: return 16
    case theme.SizeNameHeadingText:    return 18
    }
    return t.Theme.Size(name)
}
```

main 里挂上:

```go
a := app.New()
a.Settings().SetTheme(theme.New())
```

### 自定义 Switch 控件

Fyne 没原生 Switch，自己实现:

```go
type Switch struct {
    widget.BaseWidget
    On       bool
    OnChange func(bool)
}

func NewSwitch(onChange func(bool)) *Switch {
    s := &Switch{OnChange: onChange}
    s.ExtendBaseWidget(s)
    return s
}

func (s *Switch) Tapped(*fyne.PointEvent) {
    s.On = !s.On
    if s.OnChange != nil {
        s.OnChange(s.On)
    }
    s.Refresh()
}

func (s *Switch) CreateRenderer() fyne.WidgetRenderer {
    track := canvas.NewRectangle(brandIndigo)
    track.CornerRadius = 10
    thumb := canvas.NewCircle(brandCyan)
    return &switchRenderer{sw: s, track: track, thumb: thumb}
}

// ... renderer 实现负责定位 thumb 在左/右
```

### 状态点叠加在 Avatar

```go
func makeAvatar(initial string, online bool) fyne.CanvasObject {
    bg := canvas.NewCircle(avatarBgColor)
    bg.Resize(fyne.NewSize(36, 36))
    
    label := canvas.NewText(initial, textPrimary)
    label.Alignment = fyne.TextAlignCenter
    label.TextStyle = fyne.TextStyle{Bold: true}
    label.TextSize = 13
    
    statusDot := canvas.NewCircle(statusColor(online))
    statusDot.Resize(fyne.NewSize(11, 11))
    statusDot.Move(fyne.NewPos(25, 25))  // 右下角
    
    return container.NewWithoutLayout(bg, label, statusDot)
}
```

### Pill Chip

```go
func makeChip(text string, bgColor, textColor color.Color) fyne.CanvasObject {
    bg := canvas.NewRectangle(bgColor)
    bg.CornerRadius = 999
    label := canvas.NewText(text, textColor)
    label.TextSize = 11
    return container.NewStack(bg, container.NewPadded(label))
}
```

### Hero 卡片

```go
func buildHeroCard(state *AppState) fyne.CanvasObject {
    statusRow := container.NewHBox(
        statusDot(state.Connected),
        statusLabel(state.Connected),
        layout.NewSpacer(),
        trafficLabel(state),  // "↑ 2.4 MB · ↓ 18.7 MB"
    )
    
    nameLabel := canvas.NewText(state.DeviceName, textPrimary)
    nameLabel.TextSize = 18
    nameLabel.TextStyle = fyne.TextStyle{Bold: true}
    
    ipLabel := canvas.NewText(state.VPNIP, textSec)
    ipLabel.TextSize = 13
    ipLabel.TextStyle = fyne.TextStyle{Monospace: true}
    
    actionBtn := widget.NewButton(state.PrimaryActionLabel(), state.PrimaryAction)
    actionBtn.Importance = widget.HighImportance
    
    inboundRow := container.NewBorder(
        nil, nil, nil, NewSwitch(state.ToggleInbound),
        container.NewVBox(
            canvas.NewText("允许 VPN 入站访问", textPrimary),
            canvas.NewText(state.VPNCIDR, textSec),
        ),
    )
    
    inner := container.NewVBox(
        statusRow,
        nameLabel,
        ipLabel,
        actionBtn,
        widget.NewSeparator(),
        inboundRow,
    )
    
    // 卡片背景 + 圆角
    bg := canvas.NewRectangle(surfaceB)
    bg.CornerRadius = 12
    
    return container.NewStack(bg, container.NewPadded(inner))
}
```

### Tab 容器

```go
tabs := container.NewAppTabs(
    container.NewTabItemWithIcon("节点", theme.ComputerIcon(), peerListView),
    container.NewTabItemWithIcon("区域", theme.FolderIcon(), zoneListView),
)
tabs.SetTabLocation(container.TabLocationTop)
```

count badge（"节点 2"中的 "2"）需要自定义 Tab 标签，可以用 `container.NewTabItem(label, content)` 然后通过 `tabs.OnSelected` 动态更新 label。

### 列表渲染

```go
peerList := widget.NewList(
    func() int { return len(state.Peers) },
    func() fyne.CanvasObject { return newPeerRow() },
    func(i widget.ListItemID, item fyne.CanvasObject) {
        updatePeerRow(item, state.Peers[i], state.IsSelf(state.Peers[i]))
    },
)
```

每行通过 `container.NewBorder(nil, nil, avatarWidget, trailingWidget, content)` 组装。

---

## 7. 实现优先级

如果时间有限，按这个顺序实现:

1. **必做**：自定义 Theme（色彩、字号、间距） + App Bar + Hero 卡片
2. **必做**：列表项的 avatar + 状态点 + 主副标题结构
3. **应做**：Switch 自定义控件
4. **应做**：Tab indicator 颜色
5. **可选**：Pill chip 组件
6. **可选**：Hero 卡片内部 divider + 入站 toggle 行
7. **可选**：流量统计实时更新

---

## 8. 验收清单

实现完成后用这个清单自查:

- [ ] App Bar 是左对齐 logo + 文字，不是居中大标题
- [ ] 顶部没有"退出登录"按钮
- [ ] 连接/断开是单一主按钮，不是两个按钮并排
- [ ] 状态用圆点 + 颜色 + 简短文字表达，不是 `[离线]` 这种括号文本
- [ ] 节点列表每行有 avatar + 状态点
- [ ] 自己的设备一行有"本机" chip 标签 + 浅色背景高亮
- [ ] 离线节点的文字颜色是 textTertiary（最浅），不是 textPrimary
- [ ] "允许 VPN 入站访问"在 Hero 卡片内部底部，不是单独一行
- [ ] 用 Switch 不是 checkbox
- [ ] Tab 名字是"节点"/"区域"，不是"我的节点"/"我的区域"
- [ ] 没有渐变、阴影、blur、glow 效果
- [ ] 主操作按钮是 pill 形圆角（999px），不是 8px 小圆角
- [ ] IP 地址、CIDR 用 monospace 字体显示

---

End of design spec.
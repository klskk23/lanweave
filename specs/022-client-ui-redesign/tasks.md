# Tasks: client-ui-redesign

**Input**: Design documents from `/specs/022-client-ui-redesign/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/ui-contract.md, quickstart.md

**Tests**: 宪法 II(NON-NEGOTIABLE)要求每个 user story ≥1 个验收测试。本切片用 Fyne `test` 包做 headless 控件测试(gui tag,无需 OpenGL)+ 纯逻辑测试(`Transfer()` UAPI 解析、字节格式化、离线相对时间);纯视觉走 Mesa VM 人工矩阵(quickstart C/D)。`unshare -rUn go test ./...` 须全绿。

**Organization**: 任务按 user story 分组,P1(Hero)→ P2(App Bar/列表)→ P3(流量/Wizard)。自定义深色主题与共享自绘控件为各故事的阻塞性基础(Phase 2)。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行(不同文件、无未完成依赖)
- **[Story]**: US1..US5(对应 spec 用户故事);Setup/Foundational/Polish 无 Story 标签

## Path Conventions

单体 Fyne 桌面客户端。改造 99% 落在 `internal/client/ui/`(`//go:build gui`,`*_test.go` 纯逻辑除外);唯一非 UI 改动在 `internal/client/tunnel/`;文案在 `internal/client/i18n/`;启动挂主题在 `cmd/lanweave-client/main.go`。

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 确认改造前基线绿色,锁定零回归起点。

- [X] T001 建立基线:运行 `go build -tags gui ./cmd/lanweave-client`、`go test ./internal/client/...`、`unshare -rUn go test ./...`,确认改造前全绿(作为 SC-004/005 的对照基准)。

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 自定义强制深色主题 + 共享自绘控件 + 启动挂主题。这是 US1/US2/US3/US5 全部 UI 故事的共享地基。

**⚠️ CRITICAL**: 本阶段完成前,任何 UI 故事都无法开始(US4 的 tunnel/format 纯逻辑部分例外,可独立先行)。

- [X] T002 在 `internal/client/ui/theme.go` 新增 `LanweaveTheme`(内嵌 `theme.DefaultTheme()`)与顶层共享设计令牌(brand*/surface*/text*/success,取自 UI-DESIGN §2/§3/§6);`Color()` 覆盖 Primary/Background/InputBackground/Foreground/PlaceHolder/Success/Separator,**未覆盖项一律以 `theme.VariantDark` 回落**(忽略传入 variant)→ 强制深色;`Size()` 覆盖 padding 12 / inner 8 / text 14 / caption 12 / subHeading 16 / heading 18 / 输入圆角 8;无渐变/阴影/glow(契约 C4,FR-001)。
- [X] T003 [P] 在 `internal/client/ui/switch.go` 新增自绘 `Switch`(`NewSwitch(onChange func(bool)) *Switch`,`On bool`,`Tapped` 翻转 `On`+调 `OnChange`+`Refresh()`,renderer:圆角 track + thumb 左/右定位),引用 `theme.go` 共享令牌;**替代 `widget.Check`**(契约 C3,FR-007)。依赖 T002。
- [X] T004 [P] 在 `internal/client/ui/widgets.go` 新增 Avatar(圆底+首字母+右下状态点)、Pill chip(`CornerRadius:999` 矩形+padded text)、状态指示器(着色 `canvas.Circle`+着色 `canvas.Text`,**非 `[...]` 括号文本**),引用共享令牌(契约 C3,FR-005/009)。依赖 T002。
- [X] T005 [P] 在 `cmd/lanweave-client/main.go` 的 `app.NewWithID(...)` 之后、`ShowAndRun` 之前新增一行 `a.Settings().SetTheme(ui.NewTheme())`;其余启动逻辑(提权、i18n.Init、图标、firewall 清扫、StartupTarget 分支)不变(契约 C7,FR-001)。依赖 T002。
- [X] T006 [P] 在 `internal/client/ui/theme_test.go` 断言强制深色:`NewTheme().Color(theme.ColorNameBackground, theme.VariantLight)` 仍返回深色 surfaceBase(忽略浅色 variant)(SC-001 支撑,FR-001)。依赖 T002。
- [X] T007 [P] 在 `internal/client/ui/widgets_test.go`(gui tag,`fyne.io/fyne/v2/test`)断言:`Switch.Tapped` 翻转 `On` 并触发回调;状态指示器渲染出圆点 + 文本(非括号);chip 可实例化渲染(SC-002)。依赖 T003、T004。

**Checkpoint**: 主题与共享控件就绪,UI 故事可开始(panel.go 为单一重写文件,故 US1→US2→US3→US4 对其改动按优先级顺序进行)。

---

## Phase 3: User Story 1 - 主页面 Hero 卡片 (Priority: P1) 🎯 MVP

**Goal**: 一张「本设备」Hero 卡片——状态圆点+文本(非括号)、设备名(18)、等宽 VPN IP、**单一**连/断 pill 主按钮、分隔线、「允许 VPN 入站访问」Switch + CIDR 副标题;整体强制深色扁平。

**Independent Test**: 打开主面板:Hero 呈现圆点状态/设备名/等宽 IP/单一主按钮;点主按钮在连/断间切换且文案随之变;Switch 开关与防火墙偏好一致;无头测试断言「按 `tunnel.State()` 渲染出唯一连或断按钮」「Switch 反映并回写 `FirewallAllowed()`」。

### Tests for User Story 1 ⚠️

- [X] T008 [US1] 在 `internal/client/ui/panel_hero_test.go`(gui tag,fake controller/tunnel)断言:未连接→唯一「立即连接」按钮且无第二按钮;`State()==Connected`→按钮文案「断开连接」;Switch `On` 反映并回写 `FirewallAllowed()`/`SetFirewallAllowed()`(US1-1/2/3,SC-002/003)。

### Implementation for User Story 1

- [X] T009 [US1] 重写 `internal/client/ui/panel.go` 骨架 + Hero 卡片:surfaceB 圆角卡,含状态行(状态指示器圆点+文本:已连接/正在连接/未连接/连接失败)、设备名(18)、VPN IP(13 mono)、**单一 pill 主按钮**(按 `tunnel.State()` 切「立即连接/断开连接」)、分隔线、「允许 VPN 入站访问」Switch、`firewall.VPNSubnet` CIDR 副标题(契约 C5,FR-005/006/007)。依赖 T002/T003/T004。
- [X] T010 [US1] 在 `panel.go` 将 Hero 的 Switch 接 `Controller.FirewallAllowed()`/`SetFirewallAllowed(on, connected)`/`ReconcileFirewall(connected)`(只换控件,语义/生命周期不变);主按钮接既有连/断逻辑,Connecting 期间禁用(FR-007/FR-015)。依赖 T009。
- [X] T011 [P] [US1] 在 `internal/client/i18n/en.json` + `zh-Hans.json` 同步新增键:`status.connected/connecting/disconnected/failed`、`panel.connect`(立即连接)、`panel.disconnect`(断开连接)、`panel.allowInbound`(允许 VPN 入站访问)(FR-014,契约 C6)。

**Checkpoint**: US1 独立可演示——焕新的 Hero 卡片 + 单一主按钮 + Switch,强制深色。MVP 完成。

---

## Phase 4: User Story 2 - App Bar 与 overflow 菜单 (Priority: P2)

**Goal**: 48px App Bar(左对齐 logo + 「lanweave」,底部 0.5px 分隔线,标题不居中)+ 右侧 ⋮ overflow(语言子菜单 / 置底红色退出登录 / 信任状态项)。

**Independent Test**: App Bar 左对齐 logo+lanweave+底 divider;⋮ 含语言三项 + 置底红退出;insecure 会话见红「证书未验证」项、TOFU 钉扎见中性「已在本机信任」项、system-CA 两者皆无;退出走既有退登编排。

### Tests for User Story 2 ⚠️

- [X] T012 [US2] 在 `internal/client/ui/panel_overflow_test.go`(gui tag,fake controller)断言:`Insecure()==true`→菜单含红「证书未验证」项;`PinnedCertSHA256!=""`→含中性「已在本机信任」项;system-CA→两者皆无;菜单含语言子菜单 + 置底退出项(US2-2/3/4,SC-002)。

### Implementation for User Story 2

- [X] T013 [US2] 在 `panel.go` 新增 App Bar:顶部 48px,左对齐 `AppIcon()` + 「lanweave」(16/500),底部 0.5px 分隔线,标题不居中,右侧 ⋮ 容器(契约 C5,FR-002)。依赖 T009。
- [X] T014 [US2] 在 `panel.go` 实现 ⋮ overflow 菜单:语言子菜单(跟随系统/中文/English,复用 `lang_select.go` 的偏好读写,重启生效)+ 置底红色「退出登录」(走既有 `confirmLogout`→`Logout()`)+ 信任项(insecure→红「证书未验证」、pinned→中性「已在本机信任」、system-CA→无项);不新建设置/关于(契约 C5,FR-003/004)。依赖 T013。
- [X] T015 [P] [US2] 在 `en.json` + `zh-Hans.json` 同步新增/复用键:`trust.notVerified`(证书未验证)、`trust.selfSignedNote`(已在本机信任)、overflow 菜单 + 退出登录相关键(FR-014,契约 C6)。

**Checkpoint**: US1 + US2 各自独立可用;导航与信任/语言/退出归入统一入口。

---

## Phase 5: User Story 3 - 节点/区域 Tabs、列表与区域详情 (Priority: P2)

**Goal**: 「节点 N」「区域 N」带计数 tab(选中 2px 品牌青指示条);节点行 avatar+状态点+名+等宽 IP(离线拼「N 分钟前离线」,本机行 +「本机」chip+高亮且不可点);区域行整行可点 → 区域详情 sheet(成员/退出/改密/删除/踢人);右下「+」FAB 创建/加入二选一。

**Independent Test**: 两 tab 带计数+青指示条;节点行有 avatar+状态点+名+等宽 IP,本机行 chip+高亮且点击无响应,离线行「N 分钟前离线」;点区域行开详情,owner 可改密/踢人/删除、非 owner 只读;点「+」FAB 出创建/加入二选一。

### Tests for User Story 3 ⚠️

- [X] T016 [US3] 在 `internal/client/ui/panel_list_test.go`(gui tag,fake controller)断言:本机行含「本机」chip+高亮+不可点;离线行文案含「N 分钟前离线」且用 textTertiary;状态点颜色随在线/离线;区域行 `Tapped` 触发打开详情;owner 详情含改密/踢人/删除、非 owner 只读(US3-1..5,SC-002)。

### Implementation for User Story 3

- [X] T017 [P] [US3] 新增 `internal/client/ui/format.go` 的 `offlineSince(lastSeen string) string`(`now-LastSeen` 取分钟→「N 分钟前离线」;空/无效→只「离线」)+ `internal/client/ui/format_test.go` 边界断言(FR-009)。
- [X] T018 [US3] 在 `panel.go` 新增「节点 N」「区域 N」tab(`container.NewAppTabs`,标题内联计数 + leading icon,选中 2px brandCyan 指示条由 `Primary=brandCyan` 驱动);名为「节点/区域」非「我的…」(契约 C5,FR-008)。依赖 T009。
- [X] T019 [US3] 在 `panel.go` 渲染节点行:avatar(右下状态点)+ 设备名 + 等宽 IP;离线且有 `LastSeen`→拼 `offlineSince(...)` 且 textTertiary;本机行 +「本机」chip + 高亮背景 + **纯展示不可点**(FR-009)。依赖 T018、T017、T004。
- [X] T020 [US3] 在 `panel.go` 渲染区域行(扁平 avatar + 名 + owner chip)+ 整行可点 → 区域详情 sheet:迁入既有 `showMembers`/`confirmLeave`/`changePassword`/`confirmDelete`/`confirmKick`,owner 判定沿用 `ZoneView.IsOwner`(只换触发位置,控制器方法不变)(契约 C5,FR-010/FR-015)。依赖 T018。
- [X] T021 [US3] 在 `panel.go` 右下角新增「+」FAB(圆形按钮叠内容右下)→ 弹「创建区域/加入区域」二选一(复用 `onCreateZone`/`onJoinZone`)(契约 C5,FR-011)。依赖 T018。
- [X] T022 [P] [US3] 在 `en.json` + `zh-Hans.json` 同步新增键:`panel.thisMachineTag`(本机)、`panel.offlineSince`(`%s 分钟前离线`)、`panel.tabNodes`(节点)、`panel.tabZones`(区域)、区域详情/FAB 相关键;删除不再用的旧键(旧 tab 名)(FR-014,契约 C6)。

**Checkpoint**: US1/US2/US3 各自独立可用;内容浏览与区域管理完整。

---

## Phase 6: User Story 4 - Hero 卡片实时上下行流量 (Priority: P3)

**Goal**: 连接后 Hero 状态行显示实时 ↑/↓ 流量并以更快节奏刷新;断开后隐藏并复位。唯一触达隧道包处。

**Independent Test**: 连接后 Hero 出现 ↑/↓ 数值并增长;断开后隐藏复位;无头测试断言 `Transfer()` UAPI 解析/求和正确、字节格式化正确。

### Tests for User Story 4 ⚠️

- [X] T023 [P] [US4] 在 `internal/client/tunnel/tunnel_test.go`(纯逻辑,无 gui tag)用 fake engine 喂含多 peer `rx_bytes=`/`tx_bytes=` 的 UAPI 文本,断言 `Transfer()` 求和正确;未连接(`eng==nil`)→`(0,0,nil)` 不报错(契约 C1,FR-012,SC-006)。
- [X] T024 [P] [US4] 在 `internal/client/ui/format_test.go` 断言 `formatBytes` 边界:0/1023/1024/1.5MB/GB → 期望可读串(B/KB/MB/GB)(SC-006)。

### Implementation for User Story 4

- [X] T025 [US4] 在 `internal/client/tunnel/tunnel.go`:`engine` 接口加 `transfer() (rx, tx int64, err error)`;`wgEngine.transfer()` 调既有 `dev.IpcGet()`,逐行解析 `rx_bytes=`/`tx_bytes=`(多 peer 求和,复用 `handshaked()` 同一文本路径);`Tunnel.Transfer() (rx, tx int64, err error)` 加 `t.mu` 读 `t.eng`,`eng==nil`→`(0,0,nil)`;其余方法签名/语义不变(契约 C1,FR-012/FR-015)。
- [X] T026 [US4] 在 `internal/client/ui/format.go` 新增 `formatBytes(n int64) string`(字节→B/KB/MB/GB 可读串,纯函数)(SC-006)。依赖 T017(同文件)。
- [X] T027 [US4] 在 `panel.go` Hero:Connected 时以 ~2s 节奏调 `Tunnel.Transfer()`,`fyne.Do` 更新状态行 ↑/↓(`traffic.up`/`traffic.down`);断开停止轮询并隐藏复位(契约 C5,FR-012)。依赖 T009、T025。
- [X] T028 [P] [US4] 在 `en.json` + `zh-Hans.json` 同步新增键:`traffic.up`(上行)、`traffic.down`(下行)(FR-014,契约 C6)。

**Checkpoint**: 连接时 Hero 显示非零 ↑/↓ 流量,断开复位。

---

## Phase 7: User Story 5 - Wizard 套用同一深色主题 (Priority: P3)

**Goal**: Wizard 沿用主面板深色品牌主题(配色/字号/pill 主按钮/卡片包裹 body,顶部保留语言选择器);四步流程与 Back/Cancel/Next 逻辑零改动。

**Independent Test**: 全新环境进 Wizard,外观与主面板一致;走完四步(服务器→登录/注册→节点名→配置)与改版前完全相同,成功进主面板。

### Tests for User Story 5 ⚠️

- [X] T029 [US5] 在 `internal/client/ui/wizard_test.go`(gui tag)冒烟:套主题后四步可渲染,Back/Cancel/Next 回调与流程逻辑不变(US5,SC-004)。

### Implementation for User Story 5

- [X] T030 [US5] 在 `internal/client/ui/wizard.go` 套主题:配色/字号/pill 主按钮/卡片包裹 body,顶部保留语言选择器;四步流程与 Back/Cancel/Next 逻辑**不改**(仅换肤)(契约 C5,FR-013/FR-015)。依赖 T002。

**Checkpoint**: 全部故事独立可用,视觉统一。

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: 跨故事收尾、宪法强制的 DESIGN.md 同步、零回归门禁、人工验收。

- [X] T031 [P] 同 PR 修订 `DESIGN.md`(宪法 DESIGN.md authority):§9.4 主面板重写为新布局(App Bar+overflow / Hero+Switch / 节点·区域 tab / 区域详情 / FAB / 流量);§282/§285 信任指示改记为 App Bar overflow 菜单项(insecure 红项 / 自签中性项),注明 022 已接受的 UX 取舍(quickstart E)。
- [X] T032 [P] i18n 键集一致:确保 `keys(en)==keys(zh-Hans)`,删除所有不再使用的旧键(居中标题、旧 tab 名),`internal/client/i18n/i18n_test.go` 双向 parity 断言全绿(契约 C6,FR-014)。
- [X] T033 `gofmt`/`go vet`/`staticcheck` 在改动包(`internal/client/ui`、`internal/client/tunnel`、`cmd/lanweave-client`)干净(宪法 I)。
- [X] T034 运行 `go test ./internal/client/...`(headless 控件 + 纯逻辑)全绿(SC-002/004)。
- [X] T035 运行 `unshare -rUn go test ./...` 全量门禁全绿(服务端 SQLite/nftables/WireGuard 逻辑未动)(SC-005)。
- [ ] T036 Mesa OpenGL VM 人工矩阵:对照 `docs/UI-DESIGN.md §8` 逐条肉眼核对 12 项 100% 通过 + quickstart C 补充项(SC-001,FR-016)。
- [ ] T037 执行 quickstart D 行为零回归核对:连接/断开、防火墙生命周期、退出编排、区域 CRUD、TOFU/insecure 判定与改版前一致(SC-004,FR-015)。

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖,立即开始。
- **Foundational (Phase 2)**: 依赖 Setup;**阻塞所有 UI 故事**(US4 的 tunnel/format 纯逻辑可与之并行先行)。
- **User Stories (Phase 3-7)**: 均依赖 Foundational。因 `panel.go` 为单一重写文件,US1→US2→US3→US4 对其改动按优先级**顺序**进行;US5(wizard.go)与 US4 的 tunnel/format 部分可独立并行。
- **Polish (Phase 8)**: 依赖所有目标故事完成。

### User Story Dependencies

- **US1 (P1)**: Foundational 后开始,建立新 `panel.go` 骨架 + Hero(MVP)。
- **US2 (P2)**: 依赖 US1 的 `panel.go` 骨架(在其上加 App Bar/overflow)。
- **US3 (P2)**: 依赖 US1 的 `panel.go` 骨架(加 tab/列表/详情/FAB);`format.go` 的 `offlineSince` 本故事新建。
- **US4 (P3)**: tunnel/format 纯逻辑(T023-T026)独立;Hero 流量显示(T027)依赖 US1 Hero;`format.go` 与 US3 共文件(US3 先建)。
- **US5 (P3)**: 仅依赖主题(T002),与其他故事独立(改 `wizard.go`,不碰 `panel.go`)。

### 单文件顺序约束

- `internal/client/ui/panel.go`:T009→T010→T013→T014→T018→T019/T020/T021→T027 顺序(同文件)。
- `internal/client/ui/format.go`:T017(US3)→T026(US4)顺序(同文件)。
- `internal/client/i18n/{en,zh-Hans}.json`:T011/T015/T022/T028 各自追加;因故事顺序执行,不冲突;T032 最后统一 parity + 清旧键。

### Parallel Opportunities

- Foundational:T003、T004、T005、T006 在 T002 后可并行;T007 待 T003/T004。
- 各故事内 i18n 任务([P])可与该故事 panel 实现并行(不同文件)。
- US4 的 T023(tunnel_test)、T024(format_test)可与 Foundational 并行先行(纯逻辑、不碰主题)。
- US5(T029/T030)可在主题就绪后与 US2/US3/US4 并行(独立文件 `wizard.go`)。
- Polish:T031(DESIGN.md)、T032(i18n parity)可并行。

---

## Parallel Example: Foundational

```bash
# T002 完成(theme.go 令牌就绪)后并行:
Task: "switch.go 自绘 Switch (T003)"
Task: "widgets.go Avatar/chip/状态指示器 (T004)"
Task: "main.go SetTheme 挂主题 (T005)"
Task: "theme_test.go 强制深色断言 (T006)"
```

## Parallel Example: 跨故事先行

```bash
# US4 纯逻辑可与 Foundational 并行启动(不依赖主题):
Task: "tunnel_test.go Transfer 解析/求和 (T023)"
Task: "format_test.go 字节格式化边界 (T024)"
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 Setup → Phase 2 Foundational(主题 + 控件)→ Phase 3 US1(Hero)。
2. **STOP & VALIDATE**:在 Mesa VM 上独立验证 US1(Hero/单按钮/Switch/强制深色),无头测试绿。
3. 即可演示「焕然一新且操作更清晰」的主面板。

### Incremental Delivery

1. Setup + Foundational → 地基就绪。
2. + US1(Hero)→ 独立测试 → 演示(MVP)。
3. + US2(App Bar/overflow)→ 独立测试 → 演示。
4. + US3(列表/区域详情/FAB)→ 独立测试 → 演示。
5. + US4(流量)+ US5(Wizard 换肤)→ 独立测试 → 演示。
6. Polish:DESIGN.md 同步 + i18n parity + 全量门禁 + GUI 人工矩阵 + 零回归核对。

---

## Notes

- [P] = 不同文件、无未完成依赖;`panel.go`/`format.go`/i18n json 的跨任务改动按上文顺序约束执行。
- 宪法 II:每个 user story 已配 ≥1 验收测试(US1 T008、US2 T012、US3 T016、US4 T023+T024、US5 T029);不 mock 真实系统,tunnel transfer 用 fake engine 喂真实 UAPI 文本;`unshare -rUn go test ./...` 保持全绿。
- 零回归(FR-015/SC-004):控制器方法签名/语义不变,区域/退出/防火墙操作只迁移触发位置;唯一新增是 `Tunnel.Transfer()` + `engine.transfer()`。
- TODO.md(仓库根)为个人待办,提交时排除,勿用 `git add .`。
- 验证测试先失败再实现;每个 checkpoint 可独立验证后再进下一优先级。

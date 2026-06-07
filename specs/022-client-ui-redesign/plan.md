# Implementation Plan: client-ui-redesign

**Branch**: `022-client-ui-redesign` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/022-client-ui-redesign/spec.md`

## Summary

按 `docs/UI-DESIGN.md` + `docs/UI-example.png`(Material 3 启发的深色 flat 规范)全量重做 Windows 客户端主面板与 Wizard。落地集中在 gui-tagged 的 `internal/client/ui/` 包:新增自定义 `fyne.Theme`(强制深色)、若干自绘控件(Switch / Avatar+状态点 / Pill chip)、重写 `panel.go`(App Bar + overflow 菜单、Hero 卡片、节点/区域 tab、区域详情、FAB)、给 `wizard.go` 套同一主题。唯一的非 UI 改动是在隧道暴露 per-peer 收发字节(复用 `wgEngine` 已有的 `IpcGet()` UAPI 文本,新增 `rx_bytes=`/`tx_bytes=` 解析)以驱动 Hero 的实时流量。控制器(`panel.Controller` / `onboard.Provisioner`)无 UI、只回传视图数据 + typed error,业务行为零回归。i18n 沿用 020 的双 catalog,新增/改写文案 zh-Hans + en 同步。宪法「DESIGN.md authority」要求同 PR 修订 DESIGN.md §9.4 与 §282/§285 以匹配新布局(信任指示从常驻警示移入 overflow,页脚 checkbox 移入 Hero 改 Switch,tab 改名「节点/区域」)。

## Technical Context

**Language/Version**: Go 1.26.2

**Primary Dependencies**: Fyne v2(`fyne.io/fyne/v2`,GUI,`//go:build gui`)、`fyne.io/fyne/v2/lang`(i18n,沿用 020)、`golang.zx2c4.com/wireguard`(隧道引擎,本期新增读取 UAPI transfer 字段)。无新增第三方依赖。

**Storage**: 无新增持久化。复用 `state.Record`(已含 `FirewallAllowVPN`、`PinnedCertSHA256`、`LastSeen` 经由设备视图);语言偏好仍存 Fyne Preferences(`ui.language`);流量为瞬时内存值,不落盘。

**Testing**: Fyne `test` 包做 headless 控件测试(gui tag,`fyne.io/fyne/v2/test`,无需 OpenGL);纯逻辑(流量字节格式化、状态→标签映射、`Transfer()` UAPI 解析)走普通 `go test`;隧道 transfer 解析在 `tunnel_test.go` 用 fake engine + UAPI 文本断言。GUI 视觉走 Mesa OpenGL VM 人工矩阵(quickstart.md)。`unshare -rUn go test ./...` 须全绿。

**Target Platform**: Windows 10/11 桌面(最终用户);Linux + Mesa OpenGL VM(GUI 人工验收);headless Linux(CI 跑无头控件 + 逻辑测试)。

**Project Type**: 桌面 GUI 应用(Fyne 单体客户端 + 无 UI 控制器层)。

**Performance Goals**: 沿用宪法 IV「客户端 UI 输入→服务端反映状态 ≤ 1s」。数据刷新保持现有 15s 轮询;新增**流量专用轮询**仅在 Connected 时以更快节奏(~2s)调一次 `Tunnel.Transfer()`(一次 `IpcGet()` 字符串解析,开销可忽略);断开即停。主题/控件渲染保持 60fps 体感,扁平无特效本就轻量。

**Constraints**: 强制深色(忽略系统明暗);所有用户可见字符串只在 `ui` 层、经 i18n(维持控制器边界干净);`--insecure` 仍 CLI-only 不入 UI 开关;客户端业务行为零回归(连接/断开、防火墙生命周期、退出编排、区域 CRUD、TOFU/insecure 判定不变)。唯一触达 `tunnel` 包处 = 暴露 transfer 字节。

**Scale/Scope**: 单一主面板 + 4 步 Wizard;约 2 个 tab、≤ 数十节点/区域行;新增 ~1 个 theme 文件 + 3~4 个自绘控件文件 + panel 重写 + wizard 套主题 + tunnel 一个方法 + i18n 文案增量。

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality** — 改造集中在 `ui` 包,每个自绘控件单文件单职责(theme / switch / avatar / chip);不引入投机抽象(三处相似行优先于早抽象)。`gofmt`/`go vet`/`staticcheck` 须干净。控制器边界不动,无散落配置读取。**PASS**。
- **II. Testing Standards (NON-NEGOTIABLE)** — 每个 user story ≥1 个 headless 控件/逻辑测试(US1 单按钮按状态切换 + Switch 反映 firewall;US2 overflow 项随信任态;US3 本机 chip+高亮、区域行点击开详情;US4 流量字节格式化 + `Transfer()` UAPI 解析;US5 wizard 套主题后流程不变的渲染冒烟)。不 mock SQLite/nftables/WireGuard:隧道 transfer 用 fake engine 喂真实 UAPI 文本断言解析,服务端逻辑不动故 `unshare -rUn go test ./...` 维持全绿。纯 GUI 呈现 + Windows 实机走人工矩阵(沿用 017/018/020 已登记的宪法 II GUI 豁免,记入 quickstart)。**PASS**。
- **III. User Experience Consistency** — 字段表示统一(IP/CIDR 等宽、本机标注、离线相对时间);长操作仍有进度反馈(沿用现有 `run`/progress dialog);破坏性操作仍二次确认且点名实体(退出/删区域/踢人,逻辑不动);Wizard 每步可 Back/Cancel(不动);错误仍走 `panelMessage`/`friendly` 人类可读映射;**连接状态经 Hero 卡片在主屏常驻可见**。⚠ 取舍:信任指示从「常驻警示」降级为 overflow 菜单项——已在 spec Assumptions 记为**已接受的 UX 取舍**,并触发下方 DESIGN.md 同步(非违反,而是经记录的弱化)。**PASS(含已记录取舍)**。
- **IV. Performance Requirements** — 新增流量轮询为单次 `IpcGet()` 解析,远低于任何预算;UI 输入→状态反映保持 ≤1s。**PASS**。
- **Security & Operational Discipline** — 不打印任何密钥/令牌;流量为字节计数无敏感信息;`--insecure` 不入 UI。**PASS**。
- **DESIGN.md authority(Development Workflow)** — 改版与 DESIGN.md §9.4(主面板:「我的 nodes/zones」tab、页脚 checkbox + 常驻内联警告)及 §282/§285(主面板常驻信任指示 / `--insecure` 常驻「证书未验证」警示)**字面冲突**。宪法要求**同 PR 更新 DESIGN.md**:§9.4 重写为新布局(App Bar+overflow、Hero 卡片+Switch、tab「节点/区域」、区域详情、FAB、流量),§282/§285 改记信任指示位于 App Bar overflow 菜单(非常驻条)。**经本计划登记为同 PR 必做项**,非未justified 违规。

无未经论证的违规 → 进入 Phase 0。

## Project Structure

### Documentation (this feature)

```text
specs/022-client-ui-redesign/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   └── ui-contract.md   # 控制器/隧道/控件 的 UI 依赖契约
└── checklists/
    └── requirements.md  # /speckit-specify 产出
```

### Source Code (repository root)

```text
internal/client/ui/                 # 改造主战场(均 //go:build gui,除 *_test 纯逻辑外)
├── theme.go            # 新增:LanweaveTheme(fyne.Theme),强制 VariantDark,品牌色/字号/间距
├── widgets.go          # 新增:自绘 Switch、Avatar+状态点、Pill chip、状态指示器(可拆多文件)
├── panel.go            # 重写:App Bar+overflow / Hero 卡片 / tab(节点·区域)/ 区域详情 sheet / FAB / 流量
├── wizard.go           # 改:套主题(配色/字号/pill 主按钮/卡片包裹 body),四步流程与 Back/Cancel/Next 不动
├── format.go           # 新增(可选):流量字节→可读单位、离线相对时间("N 分钟前离线")等纯函数
├── lang_select.go      # 复用(overflow 内语言子菜单仍复用其偏好读写)
├── icon.go / icon.png  # 复用(App Bar logo)
└── *_test.go           # 新增:headless 控件测试 + 纯逻辑测试(format/状态映射)

cmd/lanweave-client/main.go         # 改:a.Settings().SetTheme(ui.NewTheme()) 一行(强制深色)

internal/client/tunnel/tunnel.go    # 唯一非 UI 改动:engine 接口加 transfer();wgEngine 解析 IpcGet 的
                                    #   rx_bytes=/tx_bytes=;Tunnel.Transfer()(rx,tx int64,err) 暴露
internal/client/tunnel/tunnel_test.go  # 新增:fake engine 喂 UAPI 文本,断言 Transfer 解析/聚合

internal/client/i18n/en.json        # 改:新增/改写文案键(已连接/断开连接/立即连接/允许 VPN 入站访问/
internal/client/i18n/zh-Hans.json   #   本机/N 分钟前离线/证书未验证/已在本机信任/上行下行/节点/区域 等),双语同步

DESIGN.md                           # 同 PR 修订:§9.4 主面板重写;§282/§285 信任指示改记 overflow 菜单
```

**Structure Decision**: 单体桌面客户端,无 backend/frontend 拆分。改造 99% 落在 `internal/client/ui/`(gui tag),控制器(`internal/client/panel`、`internal/client/onboard`)零改动以守住「字符串只在 ui 层、行为零回归」边界;`internal/client/tunnel` 仅加一个只读的 `Transfer()` 方法暴露 WG 引擎已有的收发字节。i18n 沿用 020 的双 catalog 机制。`cmd/lanweave-client/main.go` 仅加一行挂主题。

## Complexity Tracking

> 无宪法违规需论证。DESIGN.md 同步为宪法明文要求的常规动作(非违规),已在 Constitution Check 登记,不在此表。

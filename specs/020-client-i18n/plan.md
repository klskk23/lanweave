# Implementation Plan: client-i18n

**Branch**: `020-client-i18n` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/020-client-i18n/spec.md`

## Summary

客户端 GUI 中/英(zh-Hans + en)双语:启动按系统 locale 自动选择,可在向导顶部与面板页脚手动切换(重启生效),偏好与 onboarding 状态解耦。

**技术取向(经 Phase 0 研究修正)**:ROADMAP 设想「直接用 Fyne `lang.L` + 系统 locale 自动检测」。研究证实 Fyne `lang` **无任何公开 API 可让用户手动所选语言在启动时压过系统 locale**(`setupLang` 不导出;`updateLocalizer()` 恒读 `go-locale`,而 `go-locale` 在 **Windows** 上走 Win32 `GetUserDefaultLocaleName`,**不读 `LANG`/`LC_*` 环境变量**)。因此手动覆盖(FR-004)无法经 `lang.L` 落地。改为引入**自有的极小本地化包** `internal/client/i18n`(嵌入两份 JSON 目录,显式「偏好→否则系统 locale」语言解析,`T(key, args...)` 查表 + 英文回退),在所有平台上确定性地满足覆盖,且解析/目录校验为无头可测纯逻辑。Fyne `lang` 仅用于**读取**系统 locale(`lang.SystemLocale()`)。

## Technical Context

**Language/Version**: Go 1.26.2

**Primary Dependencies**: Fyne v2.7.4(GUI,`//go:build gui`);`fyne.io/fyne/v2/lang` 仅用其 `SystemLocale()` 读系统 locale;新增 in-tree 包 `internal/client/i18n`(标准库 `embed` + `encoding/json` + `fmt`,无新增第三方依赖)。

**Storage**: Fyne Preferences(`fyne.CurrentApp().Preferences()`,app 已是 `app.NewWithID("com.lanweave.client")`),键 `ui.language`,三态:缺省/`en`/`zh-Hans`。**不写 state.json**(FR-007)。

**Testing**: Go 无头单元测试(`internal/client/i18n`,**无 gui tag**):语言解析(偏好 vs 系统 locale)、两目录键集一致、`T` 缺键回退;`unshare -rUn go test ./...` 全绿(本期不触 SQLite/nftables/WireGuard)。GUI 渲染 + 选择器 + 重启生效由 Windows 手工验收矩阵(quickstart.md,同 016/018 风格)覆盖。

**Target Platform**: Windows 10/11 桌面客户端。

**Project Type**: desktop-app(单 Go module 的 client 子树)。

**Performance Goals**: 启动期语言解析为 O(1) map 装载,对冷启动预算(宪法 IV)无可测影响。

**Constraints**: 偏好须在首次向导(state.json 尚不存在)前即可读;手动覆盖须在 Windows 上确定性压过系统 locale;不做运行时实时重绘(重启生效)。

**Scale/Scope**: `internal/client/ui/` 约 180 个用户可见字符串(panel.go + wizard.go 字面量 + `friendly`/`panelMessage`/`tunnelMessage` 映射文案)抽成翻译键;2 种语言;2 处选择器。

## Constitution Check

*GATE: 通过(含一条已论证的取向偏离,见 Complexity Tracking)。*

- **I. Code Quality**:新增 `internal/client/i18n` 单一职责(语言解析 + 文案查表)。无散落 `os.Getenv`(偏好集中经 Fyne Preferences 单点读写)。`gofmt`/`go vet`/`staticcheck` 须干净。注释只解释 WHY。**通过**。
- **II. Testing(NON-NEGOTIABLE)**:本特性不跨进程/内核边界(无 DB/nft/WG),故「三层强制」不全绑;但每个用户故事 ≥1 验收。可测核心(解析/目录校验/回退)下沉到无头 `i18n` 包做单元测试;GUI 渲染层沿用本项目既有 gui 切片惯例(016/018)以 Windows 手工验收矩阵覆盖。无 mock 禁区涉及。**通过**。
- **III. UX Consistency**:两处选择器读写同一偏好、同一组三选项;选语言后立即弹「下次启动生效」提示(非静默等待);错误文案经 ui 层 typed-error 映射,语言恒与界面一致。**通过**。
- **IV. Performance**:启动期 map 装载,可忽略;无运行时重绘开销。**通过**。
- **Security & Operational Discipline**:翻译目录为静态文案,**不含任何密钥/口令/邀请码**;偏好仅存语言枚举。维持「用户可见字符串只在 ui 层」边界,不动服务端/底层 `errors.New`(FR-009)。**通过**。
- **Workflow**:遵循 specify→plan→tasks→implement;ROADMAP 020 完成时于合并提交勾选。**通过**。

## Project Structure

### Documentation (this feature)

```text
specs/020-client-i18n/
├── plan.md              # 本文件
├── research.md          # Phase 0:Fyne lang 覆盖能力调研 + 取向决策
├── data-model.md        # Phase 1:语言偏好 / 翻译目录 / 解析表
├── quickstart.md        # Phase 1:Windows 手工验收矩阵 + 无头测试命令
├── contracts/
│   └── i18n-surface.md  # Phase 1:i18n 包 API + 偏好键契约 + 选择器行为
└── tasks.md             # Phase 2(/speckit-tasks 生成,非本命令)
```

### Source Code (repository root)

```text
internal/client/i18n/                 # 新增,非 gui tag,可无头测试
├── i18n.go                           # Lang 常量、Resolve、Init、T、Languages、活动/回退目录
├── i18n_test.go                      # 解析(偏好 vs 系统 locale)、键集一致、T 回退
├── en.json                           # 英文目录(键的权威来源 + 缺键回退)
└── zh-Hans.json                      # 简体中文目录(键集须与 en 完全一致)

internal/client/ui/                   # 既有,gui tag
├── wizard.go                         # 字面量 → i18n.T(...);render() 顶区加语言选择器
├── panel.go                          # 字面量 + friendly/panelMessage/tunnelMessage → i18n.T(...);页脚加语言选择器
└── lang_select.go                    # 新增:共享的语言 Select 构造(三选项 ↔ 偏好读写 + 重启提示)

cmd/lanweave-client/main.go           # 既有,gui tag:构建 UI 前 i18n.Init(pref, sysLocale)
```

**Structure Decision**:可测核心置于 **非 gui** 的 `internal/client/i18n`,使语言解析与目录键集校验在 `go test ./...`(无 gui tag)下即可断言;`ui`(gui tag)只做「键→渲染」与选择器交互;`main.go` 在任何 UI 构建前完成语言装载。此分层直接服务宪法 II(可测核心)与 ROADMAP「解析/校验下沉为无头纯逻辑」的要求。

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|--------------------------------------|
| 偏离 ROADMAP「直接 `lang.L`」,新增 in-tree `i18n` 包 | FR-004/005 要求手动所选语言在**启动时压过系统 locale**;Fyne `lang` 的 `setupLang` 不导出,`updateLocalizer()` 恒读系统 locale,且 `go-locale` 在 Windows 走 Win32、忽略 `LANG`/`LC_*` 环境变量——经公开 API 无法实现覆盖 | 直接 `lang.L`:无法满足覆盖(已证伪)。设 `LANG` 环境变量:Windows 上 `go-locale` 不读,无效。改用户 Windows 区域设置:越权且非「应用内切换」。自有包是满足覆盖的最小且最可测方案,非投机抽象 |

---
description: "Task list for client-i18n"
---

# Tasks: client-i18n

**Input**: Design documents from `specs/020-client-i18n/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/i18n-surface.md, quickstart.md

**Tests**: 必须(宪法 II)。本特性**不跨进程/内核边界**(不触 SQLite/nftables/WireGuard),故「三层强制」不全绑;每个用户故事仍 ≥1 验收。可测核心(`Resolve` 解析、目录键集一致、`T` 回退、选项↔偏好映射)以无头单元测试覆盖;GUI 渲染/选择器/重启生效以 `quickstart.md` 的 Windows 手工矩阵(M1–M10)覆盖,沿用本项目 gui 切片惯例(016/018)。

**Organization**: 按用户故事分阶段,每个故事独立可测。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行(不同文件、无未完成依赖)
- **[Story]**: US1 / US2 / US3
- 描述含确切文件路径

## Path Conventions

单一 Go module,client 子树。新增**非 gui** 包 `internal/client/i18n/`(可无头测试);改动 `internal/client/ui/`(gui tag)与 `cmd/lanweave-client/main.go`(gui tag)。注意:`i18n.go`、`i18n_test.go`、`wizard.go`、`panel.go`、`lang_select.go` 各为单文件,同文件内任务**串行**(不可 [P]);两份 JSON 目录 `en.json`/`zh-Hans.json` 被多个键化任务共享 → 触及目录的任务**不互相 [P]**。

---

## Phase 1: Setup

**Purpose**: 建基线与包骨架

- [X] T001 运行 `unshare -rUn sh -c 'ip link set lo up && go test ./...'` 确认改动前全绿(回归基线);创建目录 `internal/client/i18n/`,放入 `i18n.go`(仅 `package i18n`)、`en.json`(`{}`)、`zh-Hans.json`(`{}`),确认 `go build ./internal/client/i18n/` 通过。

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 提供所有 UI 键化所依赖的查表引擎;阻塞全部故事

**⚠️ CRITICAL**: 本阶段未完,US1 的键化无法编译

- [X] T002 在 `internal/client/i18n/i18n.go`:定义 `type Lang string` 与常量 `En="en"`、`ZhCN="zh-Hans"`;`//go:embed en.json zh-Hans.json` + `init()` 用 `encoding/json` 解析为 `map[string]string`,设包级变量 `enFallback`(英文,永远存在)与 `active`(默认指向 `enFallback`)。
- [X] T003 在 `internal/client/i18n/i18n.go`:实现 `T(key string, args ...any) string`——`active[key]` 命中 → 否则 `enFallback[key]` → 否则返回 `key` 本身;有 `args` 时对结果 `fmt.Sprintf`。绝不返回空串占位。(同文件,串行于 T002)

**Checkpoint**: 查表引擎就绪,可开始 US1 键化

---

## Phase 3: User Story 1 - 界面随系统语言自动呈现 (Priority: P1) 🎯 MVP

**Goal**: 抽取 `internal/client/ui/` 全部用户可见字符串为翻译键,填充中/英两套目录,启动按系统 locale 选择,使中文系统进中文、英文系统进英文。

**Independent Test**: 无头跑 `Resolve("",sysLocale)` 系统分支 + 目录键集一致 + `T` 回退;真机 M1/M2/M3。

### Tests for User Story 1 (REQUIRED) ⚠️

> 先写、先红,再做 T005/T006/T007 转绿;键集一致测试随目录填充持续守护。

- [X] T004 [US1] 新增 `internal/client/i18n/i18n_test.go`:(a) `Resolve` 系统分支表——`("","zh-Hans-CN")==ZhCN`、`("","en-US")==En`、`("","fr-FR")==En`;(b) `T` 回退——缺键回退英文、英文亦缺则返回 key、占位 `fmt.Sprintf` 生效;(c) **目录键集一致**——加载 `en.json`/`zh-Hans.json`,断言双向差集为空(SC-004)。(a)(b) 红到 T005,(c) 在空目录时即绿、随键化持续守护。

### Implementation for User Story 1

- [X] T005 [US1] 在 `internal/client/i18n/i18n.go`:实现纯函数 `Resolve(pref, sysLocale string) Lang`(`pref` 为 `en`/`zh-Hans` 直接生效并压过系统;未知/空 `pref` 时 `sysLocale` 不区分大小写以 `zh` 开头→`ZhCN`,否则→`En`)与 `Init(pref, sysLocale string)`(据 `Resolve` 把 `active` 指向对应目录)。使 T004 (a)(b) 转绿。(同文件,串行于 T003)
- [X] T006 [US1] 键化 `internal/client/ui/wizard.go`:将所有用户可见字面量替换为 `i18n.T(...)`——含 `render()` 的 Cancel/Back/`nextLabel`、insecure/pinned 提示、各 `step*` 标题与说明/字段标签/占位/错误串、`runProvision` 进度文案、`offerTrust` 两段对话框文案、`friendly()` 全部分支;句中运行时值(服务器 URL、指纹)改 `%s` 占位 + `T(key, arg)`。为每个键同时写入 `en.json` 与 `zh-Hans.json`(键集保持一致,守 T004c)。(gui tag;触及共享目录,串行)
- [X] T007 [US1] 键化 `internal/client/ui/panel.go`:将所有用户可见字面量替换为 `i18n.T(...)`——含连接状态(`"Status: "+st.String()` 改为 `T("status."+st.String())`,**不改 tunnel 包**)、Connect/Disconnect/this-device、两个 Tab、Create/Join、`zoneRow`(owner/Members/Leave/Change password/Delete)、确认框(leave/delete/kick/logout)、members 行、`run()` 各进度文案、firewall 勾选标签、trust 横幅、`panelMessage`/`tunnelMessage` 全部分支、`onlineText`(online/offline→`T`);句中值(设备名/IP/zone 名/owner/服务器 URL)用 `%s`/`%q` 占位。为每个键同时写入两套目录(守 T004c)。(gui tag;触及共享目录,串行于 T006)
- [X] T008 [US1] 在 `cmd/lanweave-client/main.go`:`a := app.NewWithID(...)` 之后、任何 `ui.NewWizard`/`ui.NewPanel` 之前,读 `pref := a.Preferences().StringWithFallback("ui.language","")`,调用 `i18n.Init(pref, string(lang.SystemLocale()))`(import `fyne.io/fyne/v2/lang`)。(gui tag)

**Checkpoint**: `go build -tags gui ./cmd/lanweave-client` 通过;真机 M1/M2/M3 — 首次设置与面板随系统语言呈现(MVP,可独立交付)

---

## Phase 4: User Story 2 - 手动选择界面语言(重启生效) (Priority: P2)

**Goal**: 向导顶部与面板页脚各放一个共享语言选择器,选具体语言写偏好(压过系统)、弹「下次启动生效」、不实时重绘;重启后界面随之改变并跨重启保留。

**Independent Test**: 无头跑 `Resolve` 覆盖分支 + 选项↔偏好映射;真机 M4/M5/M6/M7。

### Tests for User Story 2 (REQUIRED) ⚠️

> 先写、先红,再做 T010/T011 转绿。

- [X] T009 [US2] 扩 `internal/client/i18n/i18n_test.go`:(a) `Resolve` 覆盖分支——`("zh-Hans","en-US")==ZhCN`、`("en","zh-Hans-CN")==En`(偏好压过系统);(b) 选项↔偏好纯映射——`PrefForIndex(1)=="en"`、`PrefForIndex(2)=="zh-Hans"`、`IndexForPref("en")==1`、`IndexForPref("zh-Hans")==2`。(a) 若 T005 已含覆盖即绿,(b) 红到 T010。(同文件,串行于 T004)

### Implementation for User Story 2

- [X] T010 [US2] 在 `internal/client/i18n/i18n.go`:加选择器纯映射——规范顺序 `[跟随系统, en, zh-Hans]`;`LabelKeys() []string` 返回 `["lang.followSystem","lang.english","lang.chinese"]`;`PrefForIndex(int) string`(0→"",1→"en",2→"zh-Hans")、`IndexForPref(string) int`(""/未知→0)。使 T009(b) 转绿。(同文件,串行于 T005)
- [X] T011 [US2] 新增 `internal/client/ui/lang_select.go`(gui):导出共享构造 `newLanguageSelect(win) *widget.Select`——选项为 `LabelKeys()` 经 `i18n.T` 本地化;初始选中由 `IndexForPref(当前偏好)` 定;`OnChanged` 中选具体语言→`Preferences().SetString("ui.language", PrefForIndex(i))` 并弹信息框 `i18n.T("lang.restartNotice")`,**不重绘**。向 `en.json`/`zh-Hans.json` 加 `lang.followSystem/english/chinese/restartNotice` 四键(两套一致)。(新文件)
- [X] T012 [US2] 在 `internal/client/ui/wizard.go` `render()` 顶区(与 insecure/pinned 提示同列)插入 `newLanguageSelect(z.win)`。(gui;编辑 wizard.go,串行于 T006)
- [X] T013 [P] [US2] 在 `internal/client/ui/panel.go` 页脚(与 Log out / trust 指示同区)插入 `newLanguageSelect(p.win)`。(gui;编辑 panel.go,与 T012 不同文件可并行,串行于 T007)

**Checkpoint**: 真机 M4/M5/M6/M7 — 两处切换、重启生效、跨重启保留

---

## Phase 5: User Story 3 - 复位为「跟随系统」 (Priority: P3)

**Goal**: 选择器的「跟随系统」项清除偏好(`RemoveValue`),重启后回到系统 locale;空/未知偏好默认显示「跟随系统」。

**Independent Test**: 无头跑空偏好回到系统分支 + 映射 `IndexForPref("")==0`;真机 M8/M9。

### Tests for User Story 3 (REQUIRED) ⚠️

- [X] T014 [US3] 扩 `internal/client/i18n/i18n_test.go`:断言清除偏好后的复位语义——`PrefForIndex(0)==""`、`IndexForPref("")==0`、`IndexForPref("bogus")==0`,且 `Resolve("", "en-US")==En` / `Resolve("", "zh-Hans-CN")==ZhCN`(空偏好回系统)。(同文件,串行于 T009)

### Implementation for User Story 3

- [X] T015 [US3] 在 `internal/client/ui/lang_select.go` 的 `OnChanged`:选「跟随系统」(index 0)→ `Preferences().RemoveValue("ui.language")` + 同样弹重启提示;确认初始选中对空/未知偏好回退到「跟随系统」(已由 `IndexForPref` 保证)。(gui;编辑 lang_select.go,串行于 T011)

**Checkpoint**: 真机 M8/M9 — 复位跟随系统、全新安装默认「跟随系统」

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T016 [P] 对 `internal/client/i18n`、`internal/client/ui`、`cmd/lanweave-client` 跑 `gofmt -l`、`go vet`、`staticcheck`(三者干净,宪法 I);`unshare -rUn sh -c 'ip link set lo up && go test ./...'` 全绿(SC-005,宪法 I/II)。注:`ui`/`cmd` 为 `//go:build gui`,vet/staticcheck 需带 `-tags gui`。
- [X] T017 [P] 安全/边界核对:两套 JSON 目录**不含**任何密钥/口令/邀请码/指纹(宪法 Secrets;敏感值仅经 `%s` 占位由 ui 注入);确认未改动服务端或底层 `errors.New`(FR-009);确认语言偏好仅在 Fyne Preferences、**未**写入 `state.Record`/state.json(FR-007)。
- [X] T018 真机 M10 通览:走完向导三步 + 面板(节点/区域 tab、创建/加入/成员/改密/删除/踢人确认、防火墙开关、退出登录确认),无残留英文硬编码、无可见原始键名/空白。
- [X] T019 [P] 按 `quickstart.md` 验收矩阵逐条对齐(M1–M10 + 无头映射),勾选。

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001)**:无依赖。
- **Foundational (T002–T003)**:依赖 Setup;`T` 引擎阻塞所有键化。
- **US1 (T004–T008)**:依赖 Foundational。MVP。
- **US2 (T009–T013)**:依赖 US1(键化后界面已可渲染;选择器复用 `Init`/`Resolve` 与目录)。
- **US3 (T014–T015)**:依赖 US2(在 US2 的 `lang_select.go` 上加 `RemoveValue` 分支)。
- **Polish (T016–T019)**:依赖期望故事完成。

### 关键串行约束(同文件 / 共享目录)

- `i18n.go`:T002 → T003 → T005 → T010(顺序编辑)。
- `i18n_test.go`:T004 → T009 → T014(顺序编辑)。
- 共享目录 `en.json`/`zh-Hans.json`:被 T006、T007、T011 先后写入 → 互不 [P],每次成对改两套以守键集一致。
- `wizard.go`:T006 → T012;`panel.go`:T007 → T013;`lang_select.go`:T011 → T015。

### Parallel Opportunities

- T012(wizard.go)与 T013(panel.go)不同文件、各自前置已完成 → 可并行。
- T016、T017、T019 为只读/核对 → 可并行。

---

## Parallel Example: User Story 2 接线

```bash
# 选择器构造(T011)落地后,两处接线不同文件可并行:
Task: "T012 向导顶区插入语言选择器 in internal/client/ui/wizard.go"
Task: "T013 面板页脚插入语言选择器 in internal/client/ui/panel.go"
```

---

## Implementation Strategy

### MVP First (US1)

1. T001 基线 + 包骨架 → 2. T002–T003 查表引擎 → 3. T004 先红 → 4. T005 `Resolve`/`Init` 转绿 → 5. T006/T007 键化 wizard+panel(成对填两套目录)→ 6. T008 main 装载 → **STOP & VALIDATE**:`go build -tags gui`,真机 M1/M2/M3。此时「随系统双语」已成,可手工冒烟。

### Incremental Delivery

1. Setup + Foundational → 引擎就绪
2. US1 → 随系统双语(MVP)
3. US2 → 加手动切换 + 重启生效 + 持久
4. US3 → 加复位跟随系统
5. Polish → 全量绿 + lint + 安全/边界核对 + M10 通览

---

## Notes

- [P] = 不同文件、无依赖;本切片实现集中在 `i18n.go` 单文件与两套共享目录,故多数键化/引擎任务串行。
- 每个故事先写测试、确认先红,再实现转绿(宪法 II);GUI 行为以 M 矩阵真机验收。
- 无服务端、无 state schema、无 SQLite/nftables/WireGuard 改动;`ui` 为 `//go:build gui`,可测核心下沉 `internal/client/i18n`(无 gui tag)。
- 每完成一个任务或逻辑组即可提交。

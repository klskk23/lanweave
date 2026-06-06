---
description: "Task list for client-session-persist-fix"
---

# Tasks: client-session-persist-fix

**Input**: Design documents from `specs/019-client-session-persist-fix/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/onboard-session.md, quickstart.md

**Tests**: 必须（宪法 II）。这是 bug 修复——回归测试随附（修复前红、修复后绿，diff 入 PR）；跨进程边界（客户端认证 ↔ 真实服务器）用真实 SQLite/WG/nftables，不 mock。

**Organization**: 按用户故事分阶段，每个故事独立可测。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: US1 / US2 / US3
- 描述含确切文件路径

## Path Conventions

单一 Go module，client 子树。改动集中在 `internal/client/onboard/`（非 `//go:build gui`，CI 无头可验）。注意：`onboard.go`、`onboard_test.go`、`onboard_integration_test.go` 各为单文件，同文件内的任务**串行**（不可 [P]）。

---

## Phase 1: Setup

**Purpose**: 建立回归基线

- [X] T001 运行 `unshare -rUn go test ./internal/client/onboard/...` 确认改动前全绿，作为 bug-fix 回归基线（宪法 II：修复前测试应在加入新断言后才变红）。

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 让 `Provisioner` 能读到登录后的 token；阻塞所有故事

**⚠️ CRITICAL**: 本阶段未完，US1/US3 的实现无法编译

- [X] T002 在 `internal/client/onboard/onboard.go` 的 `apiClient` 接口新增 `Token() string` 方法声明；确认 `var _ apiClient = (*apiclient.Client)(nil)` 仍编译（`*apiclient.Client.Token()` 已存在于 apiclient/client.go:154，无需改实现）。
- [X] T003 [P] 在 `internal/client/onboard/onboard_test.go` 的 `fakeAPI` 增加 `token string` 字段与 `func (f *fakeAPI) Token() string { return f.token }`，使既有与新增单元测试满足扩展后的接口。

**Checkpoint**: 接口就绪，可开始用户故事实现

---

## Phase 3: User Story 1 - 首次设置完成后直接进入主面板 (Priority: P1) 🎯 MVP

**Goal**: onboarding（登录 / 创建账号）成功后把会话 token 持久化到安全存储，使进入面板时既有 `LoadSession` 读得到、不再二次弹登录。

**Independent Test**: 干净环境完整 `Provision`（登录与创建账号两条路径），断言安全存储中 `SessionTokenName` 已写入且有效。

### Tests for User Story 1 (REQUIRED) ⚠️

> 先写、先红，再做 T006 转绿。

- [X] T004 [P] [US1] 在 `internal/client/onboard/onboard_test.go` 新增单元断言：fake api 设 `token: "tok-xyz"`，`Provision` 成功后 `fk.Get(keyring.SessionTokenName)` == `"tok-xyz"`；并扩 `TestProvisionAuthAndNameErrors`——认证失败时 `SessionTokenName` 为 `keyring.ErrNotFound`（token 仅在全成功结尾写）；再加一例：用只在 `Set(SessionTokenName,…)` 时返回错误的失败 Store 包装 `keyring.Fake`，断言 `Provision` 返回包装错误（FR-005 / R4）。
- [X] T005 [P] [US1] 在 `internal/client/onboard/onboard_integration_test.go` 扩 `TestOnboardIntegrationCreateAccount` 与 `TestOnboardIntegrationSignIn`：`Provision` 成功后断言 `fk.Get(keyring.SessionTokenName)` 非空且等于 `client.Token()`（真实服务器，真实 token）。

### Implementation for User Story 1

- [X] T006 [US1] 在 `internal/client/onboard/onboard.go` 的 `Provision`：`state.Save` 成功**之后**追加 `if err := p.Keys.Set(keyring.SessionTokenName, []byte(p.API.Token())); err != nil { return state.Record{}, fmt.Errorf("cache session: %w", err) }`，作为成功路径最后一步。使 T004、T005 转绿。（同文件，串行于 T002）

**Checkpoint**: US1 完成——首次设置后不再二次弹登录（核心修复，可独立交付）

---

## Phase 4: User Story 2 - 再次启动复用已保存的会话 (Priority: P2)

**Goal**: 冷启动时仅凭安全存储里的 token 即可复用会话，无需重登（机制 = US1 持久化 + 既有 `LoadSession`，无新增生产代码）。

**Independent Test**: Provision 写入 token 后，仅用该持久化 token 新建的客户端能直接通过受保护调用。

### Tests for User Story 2 (REQUIRED) ⚠️

- [X] T007 [US2] 在 `internal/client/onboard/onboard_integration_test.go` 新增 `TestColdStartReusesSession`：`Provision` 成功后，新建一个**不带内存 token** 的 `apiclient.New(url, WithRootCAs(pool))`，`SetToken(string(fk.Get(SessionTokenName)))`（复刻 `Controller.LoadSession` 的 SetToken 序列），断言一次受保护调用（`ListNodes()`/`Me()`）成功——证明持久化 token 单独即可认证。（依赖 T006，无生产代码改动；同文件串行于 T005）

**Checkpoint**: US1+US2 均成立——首次设置与日常冷启动都不重复登录

---

## Phase 5: User Story 3 - 取消或失败的设置不留残余凭据 (Priority: P3)

**Goal**: `Cleanup`（取消 / 失败）在删设备私钥 + 清 state 之外，一并删除 `SessionTokenName`，回到彻底空白态。

**Independent Test**: Provision 成功后 `Cleanup`，断言 `SessionTokenName`、`DeviceKeyName` 均 `ErrNotFound`、state 已清。

### Tests for User Story 3 (REQUIRED) ⚠️

> 先写、先红，再做 T009 转绿。

- [X] T008 [US3] 扩 `internal/client/onboard/onboard_test.go` 的 `TestCancelCleanup`：fake api 设非空 `token`，新增前置断言「`Provision` 后 `fk.Get(SessionTokenName)` 存在且非空」，及清理后断言「`fk.Get(SessionTokenName)` 为 `keyring.ErrNotFound`」（与既有 DeviceKeyName + state 断言并列）。（同文件串行于 T004）

### Implementation for User Story 3

- [X] T009 [US3] 在 `internal/client/onboard/onboard.go` 的 `Cleanup`：把 `p.Keys.Delete(keyring.SessionTokenName)` 加入现有 `errors.Join(...)`（与 `DeviceKeyName` 删除并列，删除不存在项保持幂等）。使 T008 转绿。（同文件串行于 T006）

**Checkpoint**: 三个故事均独立可用

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T010 运行全量 `unshare -rUn go test ./...` 全绿；对改动文件跑 `gofmt -l`、`go vet`、`staticcheck` 干净（宪法 I）。
- [X] T011 [P] 安全核对（FR-007 / 宪法 Secrets in logs）：确认 token 仅经 `keyring.Set` 写入、绝不出现在 `onboard.go` 的日志/错误信息中，且 `state.Record` schema 未变（token 不入 state.json）。
- [X] T012 [P] 按 `quickstart.md` 自动化验收映射逐条对齐（US1/US2/US3 断言到位），勾选其验收矩阵。

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001)**：无依赖，立即可做。
- **Foundational (T002–T003)**：依赖 Setup；阻塞所有故事。T002 必须在 T006/T009 之前（同文件 onboard.go 且提供接口方法）。
- **US1 (T004–T006)**：依赖 Foundational。MVP。
- **US2 (T007)**：依赖 US1（T006 的持久化）。无生产代码。
- **US3 (T008–T009)**：依赖 Foundational；T009 同文件串行于 T006，故实际排在 US1 实现之后。
- **Polish (T010–T012)**：依赖全部期望故事完成。

### 关键串行约束（同文件）

- `onboard.go`：T002 → T006 → T009（顺序编辑，不可 [P]）。
- `onboard_test.go`：T003 → T004 → T008（顺序编辑，不可 [P]）。
- `onboard_integration_test.go`：T005 → T007（顺序编辑，不可 [P]）。

### Parallel Opportunities

- T004（onboard_test.go）与 T005（onboard_integration_test.go）不同文件 → 可并行。
- T003（onboard_test.go）与 T002 完成后即可；与 US1 同阶段无并行价值（同文件）。
- T011、T012 为只读核对 → 可并行。

---

## Parallel Example: User Story 1

```bash
# US1 的两条测试不同文件，可同时写（均需在 T006 前先红）：
Task: "T004 单元断言 token 持久化 in internal/client/onboard/onboard_test.go"
Task: "T005 集成断言 token 持久化 in internal/client/onboard/onboard_integration_test.go"
```

---

## Implementation Strategy

### MVP First (US1)

1. T001 基线 → 2. T002–T003 接口 → 3. T004–T005 先红 → 4. T006 转绿 → **STOP & VALIDATE**：`unshare -rUn go test ./internal/client/onboard/...`，确认首次设置后 token 已持久化。此时核心 bug 已修复，可出版给手工冒烟。

### Incremental Delivery

1. Setup + Foundational → 地基就绪
2. US1 → 独立验证 → MVP（修掉二次登录）
3. US2 → 加冷启动复用确认测试
4. US3 → 加取消清理保障
5. Polish → 全量绿 + lint + 安全核对

---

## Notes

- [P] = 不同文件、无依赖；本切片大部分实现集中在 `onboard.go` 单文件，故实现任务串行。
- 每个故事先写测试、确认先红，再实现转绿（宪法 II）。
- 无服务端、无 UI、无 state schema 改动；面板 `NewPanel` 自带 `go p.start()`（panel.go:55），US1 的持久化经既有 `LoadSession` 自动生效。
- 每完成一个任务或逻辑组即可提交。

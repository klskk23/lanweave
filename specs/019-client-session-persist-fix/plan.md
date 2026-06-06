# Implementation Plan: client-session-persist-fix

**Branch**: `019-client-session-persist-fix` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/019-client-session-persist-fix/spec.md`

## Summary

首次设置（向导登录 / 创建账号）期间已认证的会话 token 从不写入安全存储，导致进入主面板后 `LoadSession` 读不到而二次弹登录框。修复：在 `onboard.Provisioner.Provision` 全流程成功后（认证 → 注册设备 → 写 state 之后的**最后一步**）把 `API.Token()` 写入 keyring 的 `SessionTokenName`；在 `Cleanup` 中一并删除 `SessionTokenName`，使「取消 / 失败」回到干净空白态。面板与向导的 UI 不改——面板既有的 `start()→LoadSession()` 会从安全存储读回 token 并经 `Me()` 校验通过，自然不再弹框（顺带消除「冷启动首登也弹」的隐藏面）。改动集中在无 GUI 的 `onboard` 包，可全程无头测试。

## Technical Context

**Language/Version**: Go 1.26.2

**Primary Dependencies**: 现有内部包 `internal/client/{onboard,apiclient,keyring,state}`；不引入新第三方依赖。

**Storage**: 会话 token 存于操作系统安全存储（`keyring.Store`，名 `lanweave-session-token`）。`state.json`（非密设置记录）**schema 不变**（仍 v2，token 不入 state）。

**Testing**: `go test`；集成测试对真实服务器（real SQLite + WireGuard + nftables，经 httptest TLS）跑，需 `testutil.RequireNetAdmin` → CI 在 `unshare -rUn` 下执行；单元测试用 `keyring.NewFake()`。回归测试随修复同 PR（修复前失败、修复后通过）。

**Target Platform**: Windows 桌面客户端。注意：修复代码在 `onboard` 包（**非** `//go:build gui`），跨平台纯 Go，可在 Linux CI 无头运行。

**Project Type**: 桌面客户端（单一 Go module，client 子树）。

**Performance Goals**: onboarding 完成时多一次安全存储写入（`keyring.Set`），数量级毫秒，远在预算内；登录态复用路径不变。

**Constraints**: token 仅在**完整成功后**持久化（FR-005）；取消 / 失败必须清除（FR-006）；凭据仅入安全存储，绝不写明文 state 或日志（FR-007，符合宪法「Secrets in logs」）。

**Scale/Scope**: 极小。改动面 = `onboard` 包的 `Provision` / `Cleanup` / `apiClient` 接口 + 对应测试。无服务端改动、无 schema 迁移、无 UI 改动。

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality**：改动小、显然、可逆——`onboard` 包内三处局部修改（接口加一个方法声明、`Provision` 末尾加一步、`Cleanup` 加一项删除）。无新抽象。`gofmt`/`go vet`/`staticcheck` 须干净。✅
- **II. Testing Standards (NON-NEGOTIABLE)**：这是 bug 修复，**回归测试必须随附**（修复前红、修复后绿，diff 入 PR）。跨进程边界（客户端认证 → 真实服务器），按宪法用**真实** SQLite/WG/nftables（复用 `onboard_integration_test.go` 的 `realServer` 模式），**不 mock**。每个用户故事至少一条验收测试：US1/US2（Provision 后 keyring 有 token、`Me()` 可用）、US3（Cleanup 后 token 不存在）。✅
- **III. User Experience Consistency**：移除「刚登录又被要求登录」的不一致，纯改进；无新 UI、无新文案。失效再登录路径保持不变（FR-004）。✅
- **IV. Performance Requirements**：仅 onboarding 末尾一次 keyring 写入，无热路径影响，满足所有预算。✅
- **Security & Operational Discipline**：token 仅写安全存储，不入 state.json / 日志 / fixture（FR-007）；无新密码学原语；无新增可接受风险（不需动 `DESIGN.md §11`）。✅

**结论**：无违规，**Complexity Tracking 留空**。

## Project Structure

### Documentation (this feature)

```text
specs/019-client-session-persist-fix/
├── plan.md              # 本文件
├── research.md          # Phase 0 输出
├── data-model.md        # Phase 1 输出
├── quickstart.md        # Phase 1 输出
├── contracts/
│   └── onboard-session.md   # onboard 包行为契约 + apiClient 接口增量
├── checklists/
│   └── requirements.md  # /speckit-specify 输出
└── tasks.md             # /speckit-tasks 输出（本命令不产出）
```

### Source Code (repository root)

```text
internal/client/
├── onboard/
│   ├── onboard.go                     # 改：Provision 末尾持久化 token；Cleanup 删 token；apiClient 接口加 Token()
│   ├── onboard_test.go                # 改：单元——fake keyring + fake api，断言 token 持久化/清理逻辑
│   └── onboard_integration_test.go    # 改：集成——Provision 后 keyring 有 SessionTokenName；新增 Cleanup 清理用例
├── apiclient/
│   └── client.go                      # 不改（Token() 已存在，client.go:154）
├── keyring/
│   └── store.go                       # 不改（SessionTokenName 常量已存在，store.go:25）
└── ui/
    ├── wizard.go                      # 不改（showHome 经 keyring 往返自然生效）
    └── panel.go                       # 不改（start()→LoadSession() 既有路径）
```

**Structure Decision**：沿用既有 client 子树。修复落点是无 GUI 的 `onboard` 包，故无需触碰任何 `//go:build gui` 文件，所有验收可在 CI 无头完成。`apiclient.Client.Token()` 与 `keyring.SessionTokenName` 均已存在，本切片只是把「登录后缓存 token」这一既有动作从「面板内手动登录」前移到「onboarding 完成」。

## Complexity Tracking

> 无宪法违规，无需填写。

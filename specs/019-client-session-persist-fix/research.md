# Research: client-session-persist-fix

本切片无 `NEEDS CLARIFICATION`——所有取舍已在前置 grill 阶段解决。下记关键技术决策、理由与被否方案。

## R1. 会话 token 在何时持久化

- **Decision**：在 `Provisioner.Provision` 中，**全流程成功后的最后一步**（`Authenticate` → 生成密钥 → 存设备私钥 → `registerDevice` → `ServerInfo` → `state.Save` 之后）调用 `Keys.Set(SessionTokenName, []byte(API.Token()))`。
- **Rationale**：保证 token / 设备私钥 / state.json 三者**要么都成功落盘、要么都不**，杜绝「有可用会话却无对应设备设置记录」的中间态（FR-005）。放在 `state.Save` 之后，是因为 state 是「已设置」的权威标记，token 作为其附属凭据最后写最自然。
- **Alternatives considered**：
  - 在 `Authenticate` 成功后立即写 token——被否：注册设备阶段失败会留下「有 token、无 state」的孤儿，需额外清理，且 token 对应的会话无设备可用，语义混乱。
  - 在 UI 层 `showHome` 写 token——被否：`showHome` 已新建一个无 token 的 client，原始已认证 client 的 token 不在手；且冷启动路径（main.go）根本不经过 `showHome`，仍会缺 token。持久化必须发生在仍持有已认证 client 的 onboarding 内。

## R2. UI 如何取到持久化的 token（是否要改向导/面板）

- **Decision**：**不改** `wizard.go` / `ui/panel.go`。面板 `start()` 既有逻辑先调 `Controller.LoadSession()`，它从 `keyring.SessionTokenName` 读 token → `api.SetToken` → `Me()` 校验。token 既已在 R1 落盘，这条既有路径自然读到并通过，不再 `needSignIn`。
- **Rationale**：最小改动、最大复用。靠安全存储「写—读」往返解耦，无需把已认证 client 实例穿过 UI 传递。额外代价仅是面板启动时一次 `Me()` 网络校验——这与既有冷启动路径完全一致，本就如此。
- **Alternatives considered**：把 onboarding 的已认证 client 直接传进 `panel.New`，省掉 `Me()` 往返——被否：要改 `showHome` 与 `main.go` 两处构造、破坏「面板自带会话加载」的单一入口，且冷启动仍需 keyring 读取，省不掉那条路径，净增复杂度。

## R3. 取消 / 失败的清理对称性

- **Decision**：`Provisioner.Cleanup` 在现有「删 `DeviceKeyName` + `state.Clear`」基础上，追加删除 `SessionTokenName`（用 `errors.Join` 合并三者错误，沿用现有写法）。
- **Rationale**：取消 onboarding 语义应是「当作没发生过」。即便 R1 使 token 仅在成功结尾才写（取消时通常尚无新 token），仍可能存在**上一账号未登出残留**的 token；统一在 Cleanup 清掉可保证取消后 100% 干净（US3 / FR-006），消除残留凭据隐患。成本一行。
- **Alternatives considered**：只清本次 onboarding 产生物、不动 token——被否：放过潜在残留凭据，与「彻底空白态」的验收（SC-003）不符。

## R4. 「持久化 token」这步本身失败如何处理

- **Decision**：`Keys.Set(SessionTokenName, ...)` 失败时，`Provision` **返回包装错误**（如 `fmt.Errorf("cache session: %w", err)`），与现有 `state.Save` 失败的处理方式一致；向导按既有错误路由把用户带回某一步重试。
- **Rationale**：宁可让用户看到明确的设置错误并重试，也不要静默把用户带进一个其实没保存会话、随即又弹登录的面板（spec Edge Case 之一）。重试时 `registerDevice` 对 `pubkey_taken` 幂等，重认证 + 重存 token 安全可重入。
- **退路说明**：即便此步失败而 state/key 已存，下次冷启动 `StartupTarget=Home → 面板 → LoadSession` 读不到 token 会退化为「弹一次登录」——即修复前的旧行为，无回归风险。

## R5. 是否需要改 state schema

- **Decision**：**不需要**。`SchemaVersion` 保持 2。token 属敏感凭据，只进安全存储（`keyring`），绝不写入 `state.json`。
- **Rationale**：符合宪法「设备私钥 / 会话凭据仅存安全存储、state.json 非密」的既定边界（store.go 注释、DESIGN §8）。无迁移 = 无兼容负担。

## R6. apiClient 接口增量

- **Decision**：给 `onboard` 包内部的 `apiClient` 接口新增 `Token() string`。`*apiclient.Client` 已实现该方法（client.go:154），仅补接口声明；`onboard_test.go` 的 fake api 同步加一个返回固定串的 `Token()`。
- **Rationale**：`Provision` 需要读到刚登录获得的 token 才能持久化。接口是测试可替身的注入点，扩一个只读方法成本最低、不破坏既有实现。
- **Alternatives considered**：让 `Provisioner` 持有具体 `*apiclient.Client` 而非接口——被否：破坏现有「接口 + fake」的无头可测设计（onboard 包刻意无 Fyne / 具体网络依赖）。

## R7. 测试策略（宪法 II）

- **集成（真实系统，`unshare -rUn`）**：复用 `onboard_integration_test.go` 的 `realServer`（真 SQLite/WG/nftables + httptest TLS）。
  - 扩 `TestOnboardIntegrationCreateAccount` / `SignIn`：`Provision` 成功后断言 `fk.Get(SessionTokenName)` 非空，且该 token 能驱动一次受保护调用（如 `client.Me()` / `ListNodes()` 成功）。
  - 新增清理用例：`Provision` 成功 → `Cleanup` → 断言 `SessionTokenName`、`DeviceKeyName` 均 `ErrNotFound`、`state` 已清。
- **单元（fake）**：fake api 返回固定 token + `keyring.NewFake()`，断言 `Provision` 末尾写入、`Provision` 早期失败（如认证失败）时**不**写 token、`Cleanup` 删除 token。
- **回归证明**：在加入持久化代码前，集成断言「`Provision` 后 keyring 有 token」应为**红**；加入后**绿**。该 diff 入 PR（宪法 II「回归测试随每个 bug 修复」）。
- **不 mock** SQLite/nftables/WireGuard（宪法 II）。GUI 不改 → 无需 Windows 手工矩阵。

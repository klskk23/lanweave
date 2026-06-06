# Data Model: client-session-persist-fix

本切片不新增持久化结构、不改 schema。它只是把一个**既有**的安全存储条目，在一个新的时机写入 / 清除，并明确它与设置记录的存在性不变量。

## 实体

### SessionToken（会话 token）

- **载体**：操作系统安全存储 `keyring.Store`，名 `lanweave-session-token`（`keyring.SessionTokenName`，已存在）。
- **值**：不透明 bearer token（服务器 `POST /api/v1/login` 签发的 `LoginResponse.Token`）。客户端不解析其内容。
- **敏感性**：敏感。**只**存安全存储；**绝不**写入 `state.json`、日志、错误信息或测试 fixture（FR-007 / 宪法 Secrets in logs）。
- **绑定**：对应完成设置时登录的那个用户 + 服务器（与 `state.json` 的 `ServerURL` 同源）。
- **生命周期 / 状态迁移**：
  - **未设置 → 已缓存**：`Provisioner.Provision` 全流程成功的最后一步写入。
  - **已缓存 → 已清除**：`Provisioner.Cleanup`（取消 / 失败）或既有 `Controller.Logout`（登出）删除。
  - **失效**：服务器拒绝（过期）时，既有 `Controller.LoadSession` 经 `Me()` 判 `ErrSessionExpired/ErrAuthFailed` → `needSignIn=true`，由面板重新登录覆盖写入（行为不变，FR-004）。

### SetupRecord（设置记录）—— 既有，未改

- **载体**：`state.json`（`state.Record`，非密），`SchemaVersion=2`，本切片**不动**。
- **与 SessionToken 的不变量**：
  - **成功设置后**：`{设备私钥, SetupRecord(state.json), SessionToken}` 三者**同时存在**。
  - **取消 / 失败后**：三者**同时不存在**（`Cleanup` 保证）。
  - 不允许出现「SessionToken 存在但 SetupRecord 缺失」的中间态（FR-005）——故 token 在 `state.Save` **之后**才写。

### DeviceKey（设备私钥）—— 既有，未改

- 存安全存储 `lanweave-device-private-key`。仅作为上文不变量中与 SessionToken / SetupRecord 同进退的第三方，本切片不改其写入逻辑（仍在注册设备前写入），只在 `Cleanup` 时与 token 一并删除（删除逻辑已存在）。

## 不涉及

- 无数据库表、无 schema 迁移、无服务端数据结构变化。
- 无新增 `state.json` 字段（token 不入 state）。

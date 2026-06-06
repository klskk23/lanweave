# Quickstart: client-session-persist-fix

本切片改动在无 GUI 的 `onboard` 包，**验收以自动化为主**（宪法 II 三层中可机测的两层全覆盖）；GUI 未改，仅附一项可选的 Windows 人工冒烟做最终确认。

## 自动化验收（CI，权威）

需要 `CAP_NET_ADMIN`（真实 WireGuard/nftables），在特权 netns 下运行：

```bash
# 全量（与 CI 一致）
unshare -rUn go test ./...

# 仅本切片相关包
unshare -rUn go test ./internal/client/onboard/...
```

### 验收映射

| 用例 | 覆盖 | 断言要点 |
|------|------|----------|
| `TestOnboardIntegrationCreateAccount`（扩） | US1 / SC-001（创建账号路径） | `Provision` 成功后 `keyring.Get(SessionTokenName)` 非空；该 token 可驱动一次受保护调用成功（`Me()`/`ListNodes()`） |
| `TestOnboardIntegrationSignIn`（扩） | US1 / SC-001（登录路径） | 同上，登录路径下 token 同样已持久化 |
| `TestOnboardCleanupClearsSession`（新增，集成或单元） | US3 / SC-003 / FR-006 | `Provision` 成功 → `Cleanup` → `SessionTokenName`、`DeviceKeyName` 均 `ErrNotFound`，`state` 已清 |
| 单元（fake api + fake keyring） | US1 / FR-005 | `Provision` 末尾写入 token；认证失败 / 注册失败时**不**写 token；token-save 失败时 `Provision` 返回错误 |
| 回归证明 | 宪法 II | 未加持久化代码前「Provision 后 keyring 有 token」断言为红；加入后转绿，diff 入 PR |

> US2（冷启动复用）由「token 已在安全存储 + 既有 `LoadSession` 读回」共同保证；其核心可机测部分即「Provision 后 token 在 keyring 且有效」，与 US1 同一断言覆盖。失效再登录（FR-004）属既有行为，不在本切片新增测试范围。

## 可选 Windows 人工冒烟（确认性，非门禁）

在一台干净 Windows 客户端：

1. **登录路径**：启动客户端 → 向导填服务器 → 选「Sign in」→ 输已有账号密码 → 命名设备 → Finish。**预期**：直接进入主面板，**不**再弹「Sign in」对话框；设备 / zone 列表正常加载。
2. **创建账号路径**：干净设备重走向导 → 选「Create account」→ 邀请码 + 账号密码 → 命名设备 → Finish。**预期**：直接进面板，无二次登录。
3. **冷启动复用**：关闭客户端再打开。**预期**：会话有效期内直接进面板，无登录框。
4. **取消清理**：干净设备进向导，在任一步点 Cancel → 重启客户端。**预期**：从向导第一步（填服务器地址）开始，无残留登录态。

## 失败排查

- 若步骤 1/2 仍弹登录：确认 `Provision` 的持久化是否在 `state.Save` **之后**执行、且 `API.Token()` 在该时点非空（登录已成功）。
- 若步骤 4 后仍跳过向导直达面板：确认 `Cleanup` 是否真的清了 `state`（`StartupTarget` 依赖 state 缺失才走向导）。

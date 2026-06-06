# Contract: onboard 会话持久化

本切片的「接口」是客户端内部 `internal/client/onboard` 包的行为契约，及其依赖的 `apiClient` 接口增量。无外部 / 网络 API 变更。

## C1. `apiClient` 接口增量（onboard 包内部）

`onboard` 包定义的 `apiClient` 接口新增一个只读方法：

```go
type apiClient interface {
    Register(invite, username, password string) error
    Login(username, password string) error
    RegisterNode(name, pubKey string) (protocol.NodeResponse, error)
    ListNodes() (protocol.NodeListResponse, error)
    ServerInfo() (protocol.ServerInfoResponse, error)
    Token() string // 新增：返回 Login 后保存在 client 上的会话 token
}
```

- `*apiclient.Client` 已实现 `Token() string`（client.go:154），无需改实现。
- 测试用的 fake api 须实现 `Token()`（登录后返回一个非空固定串）。

## C2. `Provisioner.Provision` 行为契约（修订）

签名不变：`Provision(c Credentials, nodeName string) (state.Record, error)`。

**前置**：`Authenticate`（必要时先 `Register` 再 `Login`）→ 生成 WG 密钥 → `Keys.Set(DeviceKeyName, priv)` → `registerDevice` → `ServerInfo` → `state.Save`，均**不变**。

**新增后置（成功路径最后一步）**：在 `state.Save` 成功**之后**，MUST 执行：

```
Keys.Set(SessionTokenName, []byte(API.Token()))
```

- **成功**：返回 `(rec, nil)`，此时 keyring 同时含 `DeviceKeyName` 与 `SessionTokenName`，且 `SessionTokenName` 的值能驱动后续受保护调用（`Me()` / `ListNodes()`）成功。
- **该步失败**：MUST 返回包装错误（如 `fmt.Errorf("cache session: %w", err)`），不返回成功记录（与现有 `state.Save` 失败处理一致）。
- **早期失败**（认证 / 注册设备 / ServerInfo / state.Save 任一失败）：MUST NOT 写 `SessionTokenName`（token 仅在全成功结尾写）。

**不变量**：`Provision` 返回 `nil` error ⟺ `SessionTokenName` 已写入。

## C3. `Provisioner.Cleanup` 行为契约（修订）

签名不变：`Cleanup() error`。

现有：删除 `DeviceKeyName` + `state.Clear(StatePath)`。

**新增**：同时删除 `SessionTokenName`。三者用 `errors.Join` 合并：

```
errors.Join(
    Keys.Delete(keyName()),
    Keys.Delete(SessionTokenName),
    state.Clear(StatePath),
)
```

- 调用 `Cleanup` 后，`Keys.Get(SessionTokenName)` MUST 返回 `keyring.ErrNotFound`；`DeviceKeyName` 同理；`state` 已清。
- 缺项删除（本就不存在）MUST 视为成功（幂等），不返回错误——`keyring.Delete` 既有实现对不存在项应 no-op（与现有 `DeviceKeyName` 删除语义一致）。

## C4. UI 层（不改，作为契约消费者记录）

- `panel.Controller.LoadSession`（既有）：读 `SessionTokenName` → `SetToken` → `Me()`。本切片使其在「向导刚结束」与「冷启动」两种进入面板的情形下都能读到有效 token，从而 `needSignIn=false`。
- `wizard.go` / `ui/panel.go`：无代码改动。契约保证：只要 C2 成立，既有 UI 路径即不再二次弹登录。
